package connectors

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

// The size caps every event leaves a connector under (conductor connectors/util.ts): a value is
// truncated, never refused.
const (
	TitleMax       = 300
	DescriptionMax = 4000
	MapEntriesMax  = 64
	MapKeyMax      = 200
	MapValueMax    = 512
	LinksMax       = 10
	LinkMax        = 2000
	SeverityMax    = 64
	FingerprintMax = 512
	AssigneeMax    = 64
)

func record(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// str is v when it is a non-empty string, else "".
func str(v any) string {
	s, _ := v.(string)
	return s
}

// idString is a non-empty string, or a number's digits.
func idString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}

// scalarString coerces a string, number or boolean; anything else is not a scalar.
func scalarString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

// stringMap is an untrusted object's scalar members as strings, the rest dropped.
func stringMap(v any) map[string]string {
	out := map[string]string{}
	m, ok := record(v)
	if !ok {
		return out
	}
	for k, raw := range m {
		if s, ok := scalarString(raw); ok {
			out[k] = s
		}
	}
	return out
}

// isoTime is an RFC 3339 UTC time with milliseconds, as the wire writes one.
func isoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05", "2006-01-02 15:04:05Z07:00", "2006-01-02"}

// realTime parses v as a time strictly after the epoch (Grafana and Alertmanager send
// 0001-01-01T00:00:00Z for "no endsAt").
func realTime(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			if t.UnixMilli() <= 0 {
				return "", false
			}
			return isoTime(t), true
		}
	}
	return "", false
}

// timeOrNow is v's time, or now for a malformed one.
func timeOrNow(v any, now time.Time) string {
	if s, ok := realTime(v); ok {
		return s
	}
	return isoTime(now)
}

// Sha256Hex is the hex sha256 of s.
func Sha256Hex(s []byte) string {
	h := sha256.Sum256(s)
	return hex.EncodeToString(h[:])
}

// StripNUL is s without its NUL characters (U+0000). Postgres cannot store one in text (the
// transports refuse it in arguments, domain.CheckText), and a sender's payload is not refused for
// one: every string an event keeps loses them (Cap), as do the delivery id and a refusal's reason.
func StripNUL(s string) string { return strings.ReplaceAll(s, "\x00", "") }

// cut is s without NULs (StripNUL), at most n runes.
func cut(s string, n int) string {
	s = StripNUL(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// capMap keeps at most MapEntriesMax members, in key order so the cut is the same every time, each
// key and value truncated (and without NULs); an empty key is dropped (it names nothing a reader
// could show), and of keys that become one the first in key order is kept (the one sent without a
// NUL, when there is one).
func capMap(m map[string]string) map[string]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		if StripNUL(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := map[string]string{}
	for _, k := range keys {
		if len(out) >= MapEntriesMax {
			break
		}
		ck := cut(k, MapKeyMax)
		if _, dup := out[ck]; !dup {
			out[ck] = cut(m[k], MapValueMax)
		}
	}
	return out
}

// Cap applies the size caps to e, which every connector routes its events through, and drops every
// NUL from what it keeps (StripNUL).
func Cap(e oncall.Event) oncall.Event {
	e.Source, e.Status, e.OccurredAt, e.DeliveryKey = StripNUL(e.Source), StripNUL(e.Status), StripNUL(e.OccurredAt), StripNUL(e.DeliveryKey)
	e.Title = cut(e.Title, TitleMax)
	e.Description = cut(e.Description, DescriptionMax)
	e.Severity = cut(e.Severity, SeverityMax)
	e.Fingerprint = cut(e.Fingerprint, FingerprintMax)
	e.UpstreamAssignee = cut(e.UpstreamAssignee, AssigneeMax)
	e.Labels, e.Annotations = capMap(e.Labels), capMap(e.Annotations)
	links := []string{}
	for _, l := range e.Links {
		if l = cut(l, LinkMax); l != "" && len(links) < LinksMax {
			links = append(links, l)
		}
	}
	e.Links = links
	return e
}

// sortedJSON is m as JSON with sorted keys: the stable identity a missing fingerprint derives from.
func sortedJSON(m map[string]string) []byte {
	b, _ := json.Marshal(m)
	return b
}

func lower(s string) string { return strings.ToLower(s) }
