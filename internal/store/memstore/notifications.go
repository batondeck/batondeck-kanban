package memstore

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The notification and agent-profile areas: a recipient's feed by (identity, id), a profile by
// (project, agent key) and a skill stat by (project, agent key, capability). The project's rows
// leave with it (DeleteProjectRecursive), as the engines' cascade drops them.

func (s *Store) AddNotification(ctx context.Context, in store.AddNotificationInput) (store.Notification, error) {
	if err := store.CheckNotification(in); err != nil {
		return store.Notification{}, err
	}
	return get(ctx, s, func(st *state) (store.Notification, error) {
		if _, ok := st.projects[in.ProjectID]; !ok {
			return store.Notification{}, domain.NotFound("Project", in.ProjectID)
		}
		data := json.RawMessage("{}")
		if len(in.Data) > 0 {
			data = cloneBytes(in.Data)
		}
		n := store.Notification{IdentityID: in.IdentityID, ID: newID("N-", 16), ProjectID: in.ProjectID, BoardID: clonePtr(in.BoardID),
			TaskID: clonePtr(in.TaskID), Type: in.Type, Actor: in.Actor, Data: data, TS: in.TS}
		st.notifications[[2]string{n.IdentityID, n.ID}] = n
		return cloneNotification(n), nil
	})
}

func (s *Store) ListNotifications(ctx context.Context, identityID string, q store.NotificationsQuery) (store.NotificationsPage, error) {
	afterTS, afterID, err := tsCursor(q.Cursor)
	if err != nil {
		return store.NotificationsPage{}, err
	}
	limit := clamp(q.Limit, 50, 200)
	return get(ctx, s, func(st *state) (store.NotificationsPage, error) {
		var rows []store.Notification
		for k, n := range st.notifications {
			if k[0] != identityID {
				continue
			}
			if afterID != "" && (n.TS > afterTS || (n.TS == afterTS && n.ID >= afterID)) {
				continue
			}
			rows = append(rows, n)
		}
		slices.SortFunc(rows, func(a, b store.Notification) int {
			return cmp.Or(cmp.Compare(b.TS, a.TS), strings.Compare(b.ID, a.ID))
		})
		page := store.NotificationsPage{Items: []store.Notification{}}
		for _, n := range rows {
			if len(page.Items) == limit {
				last := page.Items[limit-1]
				page.NextCursor = tsCursorOf(last.TS, last.ID)
				break
			}
			page.Items = append(page.Items, cloneNotification(n))
		}
		return page, nil
	})
}

func (s *Store) CountUnreadNotifications(ctx context.Context, identityID string) (int64, error) {
	return get(ctx, s, func(st *state) (int64, error) {
		var n int64
		for k, v := range st.notifications {
			if k[0] == identityID && !v.Read {
				n++
			}
		}
		return n, nil
	})
}

func (s *Store) MarkNotificationsRead(ctx context.Context, identityID string, ids []string, all bool) (int64, error) {
	if !all && len(ids) == 0 {
		return 0, nil
	}
	return get(ctx, s, func(st *state) (int64, error) {
		var n int64
		for k, v := range st.notifications {
			if k[0] != identityID || v.Read || (!all && !slices.Contains(ids, k[1])) {
				continue
			}
			v.Read = true
			st.notifications[k] = v
			n++
		}
		return n, nil
	})
}

func (s *Store) UpsertAgentProfile(ctx context.Context, in store.UpsertAgentProfileInput) (store.AgentProfile, error) {
	if err := store.CheckAgentProfile(in); err != nil {
		return store.AgentProfile{}, err
	}
	return get(ctx, s, func(st *state) (store.AgentProfile, error) {
		if _, ok := st.projects[in.ProjectID]; !ok {
			return store.AgentProfile{}, domain.NotFound("Project", in.ProjectID)
		}
		k := [2]string{in.ProjectID, in.AgentKey}
		p := store.AgentProfile{ProjectID: in.ProjectID, AgentKey: in.AgentKey, Capabilities: listOf(in.Capabilities), Tags: listOf(in.Tags),
			MaxConcurrency: clonePtr(in.MaxConcurrency), CostClass: clonePtr(in.CostClass), Models: listOf(in.Models),
			MaxEffort: clonePtr(in.MaxEffort), CreatedBy: in.CreatedBy, CreatedAt: in.Now, UpdatedAt: in.Now}
		if old, ok := st.profiles[k]; ok {
			p.CreatedBy, p.CreatedAt = old.CreatedBy, old.CreatedAt
		}
		st.profiles[k] = p
		return cloneProfile(p), nil
	})
}

func (s *Store) GetAgentProfile(ctx context.Context, projectID, agentKey string) (store.AgentProfile, error) {
	return get(ctx, s, func(st *state) (store.AgentProfile, error) {
		p, ok := st.profiles[[2]string{projectID, agentKey}]
		if !ok {
			return store.AgentProfile{}, domain.NotFound("AgentProfile", agentKey)
		}
		return cloneProfile(p), nil
	})
}

func (s *Store) RecordSkillOutcome(ctx context.Context, in store.SkillOutcome) error {
	return s.do(ctx, func(st *state) error {
		if len(in.Capabilities) > 0 {
			if _, ok := st.projects[in.ProjectID]; !ok {
				return domain.NotFound("Project", in.ProjectID)
			}
		}
		for _, c := range in.Capabilities {
			k := [3]string{in.ProjectID, in.AgentKey, c}
			v, ok := st.skillStats[k]
			if !ok {
				v = store.SkillStat{ProjectID: in.ProjectID, AgentKey: in.AgentKey, Capability: c}
			}
			if in.Completed {
				v.Completed++
			} else {
				v.Blocked++
			}
			v.TotalMs += max(0, in.ElapsedMs)
			v.Samples++
			v.UpdatedAt = in.Now
			st.skillStats[k] = v
		}
		return nil
	})
}

func (s *Store) ListSkillStats(ctx context.Context, projectID, agentKey string) ([]store.SkillStat, error) {
	return get(ctx, s, func(st *state) ([]store.SkillStat, error) {
		out := []store.SkillStat{}
		for k, v := range st.skillStats {
			if k[0] == projectID && k[1] == agentKey {
				out = append(out, v)
			}
		}
		slices.SortFunc(out, func(a, b store.SkillStat) int { return strings.Compare(a.Capability, b.Capability) })
		if len(out) > 200 {
			out = out[:200]
		}
		return out, nil
	})
}

func listOf(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return append([]string{}, ss...)
}

func cloneNotification(n store.Notification) store.Notification {
	n.BoardID, n.TaskID, n.Data = clonePtr(n.BoardID), clonePtr(n.TaskID), cloneBytes(n.Data)
	return n
}

func cloneProfile(p store.AgentProfile) store.AgentProfile {
	p.Capabilities, p.Tags, p.Models = listOf(p.Capabilities), listOf(p.Tags), listOf(p.Models)
	p.MaxConcurrency, p.CostClass, p.MaxEffort = clonePtr(p.MaxConcurrency), clonePtr(p.CostClass), clonePtr(p.MaxEffort)
	return p
}
