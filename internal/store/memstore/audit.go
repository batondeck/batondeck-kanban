package memstore

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The audit area of contract_audit.go and the tool-call area of contract_toolcalls.go.

func cloneAudit(r store.AuditRow) store.AuditRow {
	r.Data = cloneBytes(r.Data)
	return r
}

// auditNewestFirst is (ts, id) descending.
func auditNewestFirst(a, b store.AuditRow) int {
	return cmp.Or(cmp.Compare(b.TS, a.TS), cmp.Compare(b.ID, a.ID))
}

func (s *Store) AppendAudit(ctx context.Context, row store.AuditRow) (int64, error) {
	if row.Action == "" || row.Actor == "" {
		return 0, domain.Validation("an audit row needs an action and an actor", nil)
	}
	if row.Result != store.AuditOK && row.Result != store.AuditError {
		return 0, domain.Validation("unknown audit result: "+string(row.Result), map[string]any{"result": row.Result})
	}
	data, err := object("data", row.Data)
	if err != nil {
		return 0, err
	}
	return get(ctx, s, func(st *state) (int64, error) {
		st.lastAudit++
		row.ID, row.Data = st.lastAudit, data
		st.audit = append(st.audit, cloneAudit(row))
		return row.ID, nil
	})
}

// auditCursor is a listing cursor: base64url {order: the decimal ts, id: the decimal id}.
func auditCursor(cursor string) (ts, id int64, err error) {
	order, rawID, err := store.DecodeCursor(cursor)
	if err != nil {
		return 0, 0, err
	}
	ts, terr := strconv.ParseInt(order, 10, 64)
	id, ierr := strconv.ParseInt(rawID, 10, 64)
	if terr != nil || ierr != nil {
		return 0, 0, domain.Validation("Invalid cursor", nil)
	}
	return ts, id, nil
}

func auditMatches(f store.AuditFilter, r store.AuditRow) bool {
	return (f.ProjectID == "" || r.ProjectID == f.ProjectID) &&
		(f.Actor == "" || r.Actor == f.Actor) &&
		(f.Action == "" || r.Action == f.Action) &&
		(f.Subject == "" || r.Subject == f.Subject || (f.SubjectAlias != "" && r.Subject == f.SubjectAlias)) &&
		(f.Since == 0 || r.TS >= f.Since) &&
		(f.Until == 0 || r.TS <= f.Until)
}

func (s *Store) ListAuditLog(ctx context.Context, f store.AuditFilter, cursor string, limit int) (store.AuditPage, error) {
	limit = clamp(limit, 50, 500)
	var after store.AuditRow
	if cursor != "" {
		ts, id, err := auditCursor(cursor)
		if err != nil {
			return store.AuditPage{}, err
		}
		after = store.AuditRow{TS: ts, ID: id}
	}
	return get(ctx, s, func(st *state) (store.AuditPage, error) {
		var all []store.AuditRow
		for _, r := range st.audit {
			if auditMatches(f, r) {
				all = append(all, r)
			}
		}
		slices.SortFunc(all, auditNewestFirst)
		page := store.AuditPage{Rows: []store.AuditRow{}}
		for _, r := range all {
			if cursor != "" && auditNewestFirst(r, after) <= 0 {
				continue
			}
			if len(page.Rows) == limit {
				last := page.Rows[limit-1]
				page.NextCursor = store.EncodeCursor(strconv.FormatInt(last.TS, 10), strconv.FormatInt(last.ID, 10))
				break
			}
			page.Rows = append(page.Rows, cloneAudit(r))
		}
		return page, nil
	})
}

func validToolCall(c store.ToolCall) error {
	switch {
	case c.Tool == "" || c.Identity == "" || c.Code == "":
		return domain.Validation("a tool call needs a tool, an identity and a code", nil)
	case c.Outcome != store.ToolCallOK && c.Outcome != store.ToolCallError:
		return domain.Validation("unknown tool-call outcome: "+string(c.Outcome), map[string]any{"outcome": c.Outcome})
	case c.LatencyMs < 0:
		return domain.Validation("a tool call's latency cannot be negative", map[string]any{"latencyMs": c.LatencyMs})
	case strings.ContainsRune(c.Tool+c.Identity+c.Agent+c.Code+c.ProjectID, 0):
		return domain.Validation("a tool call's text cannot carry the NUL character (U+0000)", nil)
	}
	return nil
}

func (s *Store) InsertToolCalls(ctx context.Context, batch []store.ToolCall) error {
	for _, c := range batch {
		if err := validToolCall(c); err != nil {
			return err
		}
	}
	return s.do(ctx, func(st *state) error {
		st.toolCalls = append(st.toolCalls, batch...)
		return nil
	})
}

func (s *Store) DeleteToolCallsOlderThan(ctx context.Context, before int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a delete needs a positive limit", map[string]any{"limit": limit})
	}
	return get(ctx, s, func(st *state) (int64, error) {
		// Oldest first: the rows past the window, by ts then insertion order, up to limit.
		idx := make([]int, 0, len(st.toolCalls))
		for i, c := range st.toolCalls {
			if c.TS < before {
				idx = append(idx, i)
			}
		}
		slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(st.toolCalls[a].TS, st.toolCalls[b].TS) })
		if len(idx) > limit {
			idx = idx[:limit]
		}
		gone := make(map[int]bool, len(idx))
		for _, i := range idx {
			gone[i] = true
		}
		kept := st.toolCalls[:0:0]
		for i, c := range st.toolCalls {
			if !gone[i] {
				kept = append(kept, c)
			}
		}
		st.toolCalls = kept
		return int64(len(idx)), nil
	})
}

func (s *Store) SummarizeToolCalls(ctx context.Context, projectID string, since int64) ([]store.ToolCallGroup, error) {
	if projectID == "" {
		return nil, domain.Validation("a summary needs a project id", map[string]any{"field": "projectId"})
	}
	return get(ctx, s, func(st *state) ([]store.ToolCallGroup, error) {
		type key struct {
			tool    string
			outcome store.ToolCallOutcome
			agent   string
			ident   string
			day     string
		}
		groups := map[key]*store.ToolCallGroup{}
		for _, c := range st.toolCalls {
			if c.ProjectID != projectID || c.TS < since {
				continue
			}
			k := key{c.Tool, c.Outcome, c.Agent, c.Identity, time.UnixMilli(c.TS).UTC().Format(time.DateOnly)}
			g := groups[k]
			if g == nil {
				g = &store.ToolCallGroup{Tool: k.tool, Outcome: k.outcome, Agent: k.agent, Identity: k.ident, Day: k.day}
				groups[k] = g
			}
			g.Calls++
			g.LatencySum += c.LatencyMs
		}
		out := make([]store.ToolCallGroup, 0, len(groups))
		for _, g := range groups {
			out = append(out, *g)
		}
		slices.SortFunc(out, func(a, b store.ToolCallGroup) int {
			return cmp.Or(cmp.Compare(a.Tool, b.Tool), cmp.Compare(a.Outcome, b.Outcome), cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Identity, b.Identity), cmp.Compare(a.Day, b.Day))
		})
		return out, nil
	})
}
