package memstore

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The agent-session area of contract_agentsessions.go and the wake-session area of
// contract_wakesessions.go.

func cloneAgentSession(a store.AgentSession) store.AgentSession {
	a.ToolCounts = maps.Clone(a.ToolCounts)
	if a.ToolCounts == nil {
		a.ToolCounts = map[string]int64{}
	}
	return a
}

func cloneWakeSession(w store.WakeSession) store.WakeSession {
	w.SocketTokenSealed = cloneBytes(w.SocketTokenSealed)
	w.BearerSealed = cloneBytes(w.BearerSealed)
	return w
}

func validSessionKey(identity, agent, name, kind string) error {
	if identity == "" || agent == "" || name == "" || kind == "" {
		return domain.Validation("an agent session needs an identity, an agent, a name and a kind", nil)
	}
	return nil
}

func (s *Store) UpsertAgentSession(ctx context.Context, in store.AgentSessionStart) (store.AgentSession, error) {
	if err := validSessionKey(in.IdentityID, in.AgentID, in.AgentName, in.Kind); err != nil {
		return store.AgentSession{}, err
	}
	return get(ctx, s, func(st *state) (store.AgentSession, error) {
		k := [2]string{in.IdentityID, in.AgentID}
		a, ok := st.agents[k]
		if !ok {
			a = store.AgentSession{IdentityID: in.IdentityID, AgentID: in.AgentID, FirstSeenAt: in.At, ToolCounts: map[string]int64{}}
		}
		a.AgentName, a.Kind, a.LastSeenAt = in.AgentName, in.Kind, in.At
		a.SessionCount++
		a.ClientName = cmp.Or(in.ClientName, a.ClientName)
		a.ClientVersion = cmp.Or(in.ClientVersion, a.ClientVersion)
		a.OAuthClientID = cmp.Or(in.OAuthClientID, a.OAuthClientID)
		st.agents[k] = cloneAgentSession(a)
		return cloneAgentSession(a), nil
	})
}

func (s *Store) TouchAgentSession(ctx context.Context, in store.AgentSessionTouch) error {
	if err := validSessionKey(in.IdentityID, in.AgentID, in.AgentName, in.Kind); err != nil {
		return err
	}
	if in.Tool == "" || in.Count < 0 {
		return domain.Validation("a touch needs a tool and a count that is not negative", map[string]any{"count": in.Count})
	}
	count := in.Count
	if count == 0 {
		count = 1
	}
	return s.do(ctx, func(st *state) error {
		k := [2]string{in.IdentityID, in.AgentID}
		a, ok := st.agents[k]
		if !ok {
			a = store.AgentSession{IdentityID: in.IdentityID, AgentID: in.AgentID, FirstSeenAt: in.At, ToolCounts: map[string]int64{}}
		}
		a = cloneAgentSession(a)
		a.AgentName, a.Kind, a.LastSeenAt, a.LastTool = in.AgentName, in.Kind, in.At, in.Tool
		a.RequestCount += count
		a.ToolCounts[in.Tool] += count
		if in.ProjectID != "" {
			a.LastProjectID = in.ProjectID
		}
		st.agents[k] = a
		return nil
	})
}

func (s *Store) DeleteAgentSession(ctx context.Context, identityID, agentID string) error {
	return s.do(ctx, func(st *state) error {
		k := [2]string{identityID, agentID}
		if _, ok := st.agents[k]; !ok {
			return domain.NotFound("AgentSession", agentID)
		}
		delete(st.agents, k)
		return nil
	})
}

func (s *Store) ListAgentSessions(ctx context.Context, identityID string, limit int) ([]store.AgentSession, error) {
	limit = clamp(limit, 100, 1000)
	return get(ctx, s, func(st *state) ([]store.AgentSession, error) {
		out := []store.AgentSession{}
		for _, a := range st.agents {
			if identityID == "" || a.IdentityID == identityID {
				out = append(out, cloneAgentSession(a))
			}
		}
		// Newest activity first, then agent id as bytes.
		slices.SortFunc(out, func(a, b store.AgentSession) int {
			return cmp.Or(cmp.Compare(b.LastSeenAt, a.LastSeenAt), strings.Compare(a.AgentID, b.AgentID))
		})
		if len(out) > limit {
			out = out[:limit]
		}
		return out, nil
	})
}

func (s *Store) DeleteIdleAgentSessions(ctx context.Context, before int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a delete needs a positive limit", map[string]any{"limit": limit})
	}
	return get(ctx, s, func(st *state) (int64, error) {
		// The longest idle first, then by (identity, agent), up to limit.
		var idle [][2]string
		for k, a := range st.agents {
			if a.LastSeenAt < before {
				idle = append(idle, k)
			}
		}
		slices.SortFunc(idle, func(x, y [2]string) int {
			return cmp.Or(cmp.Compare(st.agents[x].LastSeenAt, st.agents[y].LastSeenAt), cmp.Compare(x[0], y[0]), cmp.Compare(x[1], y[1]))
		})
		for _, k := range idle[:min(len(idle), limit)] {
			delete(st.agents, k)
		}
		return int64(min(len(idle), limit)), nil
	})
}

func (s *Store) PutWakeSession(ctx context.Context, w store.WakeSession) error {
	if err := store.ValidWakeSession(w); err != nil {
		return err
	}
	return s.do(ctx, func(st *state) error {
		for id, o := range st.wake {
			if w.PID != 0 && o.PID == w.PID && id != w.ID {
				return store.ErrWakePIDTaken(w.PID)
			}
		}
		if old, ok := st.wake[w.ID]; ok {
			w.CreatedAt = old.CreatedAt
		}
		st.wake[w.ID] = cloneWakeSession(w)
		return nil
	})
}

func (s *Store) GetWakeSession(ctx context.Context, id string) (store.WakeSession, error) {
	return get(ctx, s, func(st *state) (store.WakeSession, error) {
		w, ok := st.wake[id]
		if !ok || w.ExpiresAt <= time.Now().UnixMilli() {
			return store.WakeSession{}, domain.NotFound("WakeSession", id)
		}
		return cloneWakeSession(w), nil
	})
}

func (s *Store) ListWakeSessions(ctx context.Context) ([]store.WakeSession, error) {
	return get(ctx, s, func(st *state) ([]store.WakeSession, error) {
		out := make([]store.WakeSession, 0, len(st.wake))
		for _, w := range st.wake {
			out = append(out, cloneWakeSession(w))
		}
		slices.SortFunc(out, func(a, b store.WakeSession) int {
			return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), strings.Compare(a.ID, b.ID))
		})
		return out, nil
	})
}

func (s *Store) SetWakeSessionCursor(ctx context.Context, id string, cursor, lastSeenAt int64) error {
	return s.do(ctx, func(st *state) error {
		w, ok := st.wake[id]
		if !ok {
			return domain.NotFound("WakeSession", id)
		}
		w.LastCursor, w.LastSeenAt = cursor, lastSeenAt
		st.wake[id] = w
		return nil
	})
}

func (s *Store) DeleteWakeSession(ctx context.Context, id string) error {
	return s.do(ctx, func(st *state) error {
		if _, ok := st.wake[id]; !ok {
			return domain.NotFound("WakeSession", id)
		}
		delete(st.wake, id)
		return nil
	})
}
