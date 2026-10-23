package connectors

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// The custom-webhook mapper (conductor src/domain/webhook-mapping.ts): the operator says, as data
// on the integration, where each field lives in a payload we have never seen. The grammar is
// deliberately tiny, because a hostile payload reaches every operator: dotted paths with numeric
// indices, a fallback chain and literal defaults; no wildcards, filters, predicates, regular
// expressions or arithmetic. A lookup is O(path length), with no backtracking.

// Mapping is the part of an integration's mapping this package reads (the JSON of the service's
// IntegrationMapping, the other members ignored).
type Mapping struct {
	Extract   *Extract          `json:"extract,omitempty"`
	StatusMap map[string]string `json:"statusMap,omitempty"`
	Defaults  *Defaults         `json:"defaults,omitempty"`
}

// Extract is where each field lives: a chain is tried in order, the first scalar wins.
type Extract struct {
	Events      string   `json:"events,omitempty"`
	Fingerprint []string `json:"fingerprint,omitempty"`
	Title       []string `json:"title,omitempty"`
	Severity    []string `json:"severity,omitempty"`
	Status      []string `json:"status,omitempty"`
	Description []string `json:"description,omitempty"`
	OccurredAt  []string `json:"occurredAt,omitempty"`
	Labels      string   `json:"labels,omitempty"`
	Annotations string   `json:"annotations,omitempty"`
	Links       []string `json:"links,omitempty"`
	DedupKey    []string `json:"dedupKey,omitempty"`
}

// Defaults stand in when a field's whole chain misses; Status defaults to firing.
type Defaults struct {
	Title    string `json:"title,omitempty"`
	Severity string `json:"severity,omitempty"`
	Status   string `json:"status,omitempty"`
}

// The caps on a mapping and on what it extracts (conductor MAPPING_LIMITS).
const (
	maxPathDepth   = 12
	maxPathLength  = 200
	maxChain       = 6
	maxPairs       = 50
	maxValueLength = 1024
	maxLinks       = 10
	maxEvents      = 100
	maxStatusMap   = 50
)

var sigils = map[string]bool{"$body_hash": true, "$now": true}

// segment is one step of a path: a key, or an index when isIndex.
type segment struct {
	key     string
	index   int
	isIndex bool
}

// splitPath splits a.b[0].c into its segments in one left-to-right pass, ok false when malformed.
func splitPath(body string) ([]segment, bool) {
	var out []segment
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, segment{key: cur.String()})
			cur.Reset()
		}
	}
	for i := 0; i < len(body); {
		switch body[i] {
		case '.':
			flush()
			i++
		case '[':
			flush()
			end := strings.IndexByte(body[i:], ']')
			if end < 0 {
				return nil, false
			}
			digits := body[i+1 : i+end]
			if len(digits) < 1 || len(digits) > 9 || strings.Trim(digits, "0123456789") != "" {
				return nil, false
			}
			n, _ := strconv.Atoi(digits)
			out = append(out, segment{index: n, isIndex: true})
			i += end + 1
			// a[0]b is a typo, not an expression.
			if i < len(body) && body[i] != '.' && body[i] != '[' {
				return nil, false
			}
		default:
			cur.WriteByte(body[i])
			i++
		}
	}
	flush()
	return out, true
}

// ValidatePath is "" for a path the mapper can evaluate, else why not.
func ValidatePath(path string) string {
	switch {
	case path == "":
		return "path must be a non-empty string"
	case len([]rune(path)) > maxPathLength:
		return fmt.Sprintf("path exceeds %d characters", maxPathLength)
	case sigils[path]:
		return ""
	}
	body := strings.TrimPrefix(path, "$.")
	if strings.HasPrefix(body, "$") {
		return "unknown sigil '" + strings.SplitN(path, ".", 2)[0] + "'"
	}
	if body == "" {
		return "'$.' must be followed by a path"
	}
	segs, ok := splitPath(body)
	switch {
	case !ok:
		return "malformed path: check the [index] brackets"
	case len(segs) == 0:
		return "path resolves to no segments"
	case len(segs) > maxPathDepth:
		return fmt.Sprintf("path is deeper than %d segments", maxPathDepth)
	}
	return ""
}

// ValidateMapping is every problem with m, each naming its field: what the integration tools refuse
// on save, so a path that cannot be evaluated is not silently missed on a real alert.
func ValidateMapping(m Mapping) []string {
	var problems []string
	if x := m.Extract; x != nil {
		for _, c := range []struct {
			field string
			chain []string
		}{{"fingerprint", x.Fingerprint}, {"title", x.Title}, {"severity", x.Severity}, {"status", x.Status},
			{"description", x.Description}, {"occurredAt", x.OccurredAt}, {"links", x.Links}, {"dedupKey", x.DedupKey}} {
			if len(c.chain) > maxChain {
				problems = append(problems, fmt.Sprintf("extract.%s: more than %d paths", c.field, maxChain))
			}
			for i, p := range c.chain {
				if why := ValidatePath(p); why != "" {
					problems = append(problems, fmt.Sprintf("extract.%s[%d] ('%s'): %s", c.field, i, p, why))
				}
			}
		}
		for _, c := range []struct{ field, path string }{{"events", x.Events}, {"labels", x.Labels}, {"annotations", x.Annotations}} {
			if c.path == "" {
				continue
			}
			if why := ValidatePath(c.path); why != "" {
				problems = append(problems, fmt.Sprintf("extract.%s ('%s'): %s", c.field, c.path, why))
			} else if sigils[c.path] {
				problems = append(problems, fmt.Sprintf("extract.%s: '%s' is not a container", c.field, c.path))
			}
		}
	}
	if len(m.StatusMap) > maxStatusMap {
		problems = append(problems, fmt.Sprintf("statusMap: more than %d entries", maxStatusMap))
	}
	return problems
}

// resolve is path against root ($.-prefixed: against the whole body), nil for a miss. A member
// holding null is a miss too. Own members only: a Go map has no prototype to walk.
func resolve(path string, root, body any, c Context) any {
	switch path {
	case "$body_hash":
		return c.BodyHash
	case "$now":
		return isoTime(c.Now)
	}
	cur := root
	if strings.HasPrefix(path, "$.") {
		path, cur = path[2:], body
	}
	segs, ok := splitPath(path)
	if !ok || len(segs) == 0 {
		return nil
	}
	for _, s := range segs {
		if s.isIndex {
			arr, ok := cur.([]any)
			if !ok || s.index >= len(arr) {
				return nil
			}
			cur = arr[s.index]
			continue
		}
		m, ok := record(cur)
		if !ok {
			return nil
		}
		if cur, ok = m[s.key]; !ok {
			return nil
		}
	}
	return cur
}

// scalar is v as a bounded string, ok false when v is not a scalar (or is an empty string).
func scalar(v any) (string, bool) {
	if f, ok := v.(interface{ Float64() (float64, error) }); ok {
		if n, err := f.Float64(); err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return "", false
		}
	}
	s, ok := scalarString(v)
	if !ok {
		return "", false
	}
	if _, isStr := v.(string); isStr {
		s = cut(s, maxValueLength)
	}
	return s, s != ""
}

func firstScalar(chain []string, root, body any, c Context) string {
	for i, p := range chain {
		if i >= maxChain {
			break
		}
		if s, ok := scalar(resolve(p, root, body, c)); ok {
			return s
		}
	}
	return ""
}

// pairs reads one path as a string map: an object of scalars, or an array of "key:value" or
// "key=value" strings (the first separator wins, so url:https://x keeps the whole URL).
func pairs(path string, root, body any, c Context) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	switch v := resolve(path, root, body, c).(type) {
	case map[string]any:
		for _, k := range sortedKeys(v) {
			if len(out) >= maxPairs {
				break
			}
			if s, ok := scalar(v[k]); ok {
				out[cut(k, maxValueLength)] = s
			}
		}
	case []any:
		for _, entry := range v {
			if len(out) >= maxPairs {
				break
			}
			s, ok := entry.(string)
			if !ok {
				continue
			}
			at := strings.IndexAny(s, ":=")
			if at <= 0 {
				continue
			}
			if k := strings.TrimSpace(s[:at]); k != "" {
				out[cut(k, maxValueLength)] = cut(strings.TrimSpace(s[at+1:]), maxValueLength)
			}
		}
	}
	return out
}

func links(chain []string, root, body any, c Context) []string {
	out := []string{}
	for i, p := range chain {
		if i >= maxChain {
			break
		}
		v := resolve(p, root, body, c)
		candidates, ok := v.([]any)
		if !ok {
			candidates = []any{v}
		}
		for _, x := range candidates {
			if len(out) >= maxLinks {
				return out
			}
			// http(s) only: a javascript: or data: URL from a payload is stored XSS on a ticket.
			if s, ok := scalar(x); ok {
				if l := strings.ToLower(s); strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

var (
	resolvedWords = map[string]bool{"resolved": true, "ok": true, "closed": true, "recovered": true, "clear": true, "cleared": true, "normal": true, "success": true, "up": true}
	firingWords   = map[string]bool{"firing": true, "alerting": true, "alarm": true, "open": true, "opened": true, "triggered": true, "active": true, "problem": true, "critical": true, "down": true}
)

func normalizeStatus(raw string, statusMap map[string]string, fallback string) string {
	if raw != "" {
		l := strings.ToLower(raw)
		for _, k := range sortedKeys(statusMap) {
			if strings.ToLower(k) == l {
				return statusMap[k]
			}
		}
		switch {
		case resolvedWords[l]:
			return "resolved"
		case firingWords[l]:
			return "firing"
		}
	}
	if fallback != "" {
		return fallback
	}
	return "firing"
}

func digitsOnly(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

// timeOr reads epoch seconds, epoch milliseconds or an ISO time, else fallback.
func timeOr(v string, fallback string) string {
	switch {
	case v == "":
		return fallback
	case len(v) == 10 && digitsOnly(v):
		n, _ := strconv.ParseInt(v, 10, 64)
		return isoTime(time.Unix(n, 0))
	case len(v) == 13 && digitsOnly(v):
		n, _ := strconv.ParseInt(v, 10, 64)
		return isoTime(time.UnixMilli(n))
	}
	if t, ok := realTime(v); ok {
		return t
	}
	return fallback
}

// mapped is one event the mapper produced, with the source's own dedup key when it has one.
type mapped struct {
	fingerprint, status, severity, title, description, occurredAt, dedupKey string
	labels, annotations                                                     map[string]string
	links                                                                   []string
}

// applyMapping is one event per element of extract.events (exactly one when unset). It fails only
// when it cannot produce a fingerprint, the correlation key; every other field degrades to a
// default. Only colliding fingerprints are numbered, so one alert keeps one fingerprint whatever the
// batch's size or order.
func applyMapping(payload any, m Mapping, c Context) ([]mapped, error) {
	x := Extract{}
	if m.Extract != nil {
		x = *m.Extract
	}
	d := Defaults{}
	if m.Defaults != nil {
		d = *m.Defaults
	}
	roots := []any{payload}
	if x.Events != "" {
		arr, ok := resolve(x.Events, payload, payload, c).([]any)
		if !ok {
			return nil, fmt.Errorf("custom mapping: extract.events ('%s') did not resolve to an array", x.Events)
		}
		roots = arr[:min(len(arr), maxEvents)]
	}
	fps := make([]string, len(roots))
	seen := map[string]int{}
	for i, root := range roots {
		if fps[i] = firstScalar(x.Fingerprint, root, payload, c); fps[i] == "" {
			return nil, errors.New("custom mapping: no fingerprint. Point extract.fingerprint at a stable id in your payload, or add '$body_hash' to the end of the chain to derive one from the body.")
		}
		seen[fps[i]]++
	}
	used := map[string]int{}
	out := make([]mapped, len(roots))
	for i, root := range roots {
		fp := fps[i]
		if seen[fp] > 1 {
			n := used[fp]
			used[fp]++
			fp = fp + "#" + strconv.Itoa(n)
		}
		e := mapped{
			fingerprint: fp,
			status:      normalizeStatus(firstScalar(x.Status, root, payload, c), m.StatusMap, d.Status),
			severity:    firstScalar(x.Severity, root, payload, c),
			title:       firstScalar(x.Title, root, payload, c),
			description: firstScalar(x.Description, root, payload, c),
			labels:      pairs(x.Labels, root, payload, c),
			annotations: pairs(x.Annotations, root, payload, c),
			links:       links(x.Links, root, payload, c),
			occurredAt:  timeOr(firstScalar(x.OccurredAt, root, payload, c), isoTime(c.Now)),
			dedupKey:    firstScalar(x.DedupKey, root, payload, c),
		}
		if e.severity == "" {
			e.severity = cmpOr(d.Severity, "warning")
		}
		if e.title == "" {
			e.title = cmpOr(d.Title, "Alert")
		}
		out[i] = e
	}
	return out, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
