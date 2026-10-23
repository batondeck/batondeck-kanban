package conformance

import (
	"context"
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("agentsessions", agentSessions) }

// agentSessions is the agent-session area: the connection upsert, the activity touch, the listing,
// the idle delete, the delete and the refusals.
func agentSessions(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListAgentSessions(ctx, "probe", 1)
		return err
	}
	area(t, "AgentSessions", newStore, probe, func(t *testing.T) {
		start := func(identity, agent string, at int64) store.AgentSessionStart {
			return store.AgentSessionStart{IdentityID: identity, AgentID: agent, AgentName: agent + "-name", Kind: "agent", At: at}
		}
		get := func(t *testing.T, s store.Store, identity, agent string) store.AgentSession {
			t.Helper()
			rows, err := s.ListAgentSessions(ctx, identity, 0)
			noErr(t, err)
			for _, r := range rows {
				if r.AgentID == agent {
					return r
				}
			}
			t.Fatalf("no session %s/%s in %+v", identity, agent, rows)
			return store.AgentSession{}
		}

		t.Run("UpsertCountsConnectionsAndKeepsFirstSeen", func(t *testing.T) {
			s := migrated(t, newStore)
			in := start("alice", "a1", 100)
			in.ClientName, in.ClientVersion, in.OAuthClientID = "claude-code", "2.1", "client-1"
			first, err := s.UpsertAgentSession(ctx, in)
			noErr(t, err)
			if first.FirstSeenAt != 100 || first.LastSeenAt != 100 || first.SessionCount != 1 || first.ClientName != "claude-code" || first.AgentName != "a1-name" {
				t.Fatalf("first upsert %+v", first)
			}
			again := start("alice", "a1", 200)
			again.AgentName, again.ClientVersion = "renamed", "2.2"
			second, err := s.UpsertAgentSession(ctx, again)
			noErr(t, err)
			if second.FirstSeenAt != 100 || second.LastSeenAt != 200 || second.SessionCount != 2 || second.AgentName != "renamed" || second.ClientName != "claude-code" || second.ClientVersion != "2.2" || second.OAuthClientID != "client-1" {
				t.Fatalf("second upsert %+v: first seen kept, count 2, empty client fields kept", second)
			}
		})

		t.Run("TouchCountsCallsPerToolAndCreatesAMissingRow", func(t *testing.T) {
			s := migrated(t, newStore)
			_, err := s.UpsertAgentSession(ctx, start("alice", "a1", 100))
			noErr(t, err)
			touch := func(tool, project string, count, at int64) {
				t.Helper()
				noErr(t, s.TouchAgentSession(ctx, store.AgentSessionTouch{IdentityID: "alice", AgentID: "a1", AgentName: "a1-name", Kind: "agent", Tool: tool, ProjectID: project, Count: count, At: at}))
			}
			touch("claim_next", "P-1", 0, 150)
			touch("claim_next", "", 3, 160)
			touch("complete_task", "P-2", 1, 170)
			got := get(t, s, "alice", "a1")
			if got.RequestCount != 5 || got.ToolCounts["claim_next"] != 4 || got.ToolCounts["complete_task"] != 1 || got.LastTool != "complete_task" || got.LastProjectID != "P-2" || got.LastSeenAt != 170 || got.SessionCount != 1 {
				t.Fatalf("after the touches %+v", got)
			}
			noErr(t, s.TouchAgentSession(ctx, store.AgentSessionTouch{IdentityID: "bob", AgentID: "b1", AgentName: "b1-name", Kind: "agent", Tool: "list_tasks", At: 180}))
			made := get(t, s, "bob", "b1")
			if made.FirstSeenAt != 180 || made.SessionCount != 0 || made.RequestCount != 1 || made.LastProjectID != "" {
				t.Fatalf("a touch with no row made %+v", made)
			}
		})

		t.Run("ListIsNewestActivityFirstByIdentityAndBounded", func(t *testing.T) {
			s := migrated(t, newStore)
			for _, in := range []store.AgentSessionStart{start("alice", "a1", 100), start("alice", "a2", 300), start("bob", "b1", 200)} {
				_, err := s.UpsertAgentSession(ctx, in)
				noErr(t, err)
			}
			ids := func(identity string, limit int) []string {
				t.Helper()
				rows, err := s.ListAgentSessions(ctx, identity, limit)
				noErr(t, err)
				out := make([]string, len(rows))
				for i, r := range rows {
					out[i] = r.AgentID
				}
				return out
			}
			if got := ids("", 0); !slices.Equal(got, []string{"a2", "b1", "a1"}) {
				t.Fatalf("every session %v, want [a2 b1 a1]", got)
			}
			if got := ids("alice", 0); !slices.Equal(got, []string{"a2", "a1"}) {
				t.Fatalf("alice's sessions %v, want [a2 a1]", got)
			}
			if got := ids("", 1); !slices.Equal(got, []string{"a2"}) {
				t.Fatalf("limit 1 %v, want [a2]", got)
			}
		})

		t.Run("DeleteIdleDeletesByLastSeen", func(t *testing.T) {
			s := migrated(t, newStore)
			for _, in := range []store.AgentSessionStart{start("alice", "a1", 100), start("alice", "a2", 300)} {
				_, err := s.UpsertAgentSession(ctx, in)
				noErr(t, err)
			}
			n, err := s.DeleteIdleAgentSessions(ctx, 300)
			noErr(t, err)
			if n != 1 {
				t.Fatalf("deleted %d idle before 300, want 1", n)
			}
			rows, err := s.ListAgentSessions(ctx, "", 0)
			noErr(t, err)
			if len(rows) != 1 || rows[0].AgentID != "a2" {
				t.Fatalf("left %+v, want a2", rows)
			}
		})

		t.Run("ATouchedSessionOutlivesAnIdleSweepOfTwoSeconds", func(t *testing.T) {
			s := migrated(t, newStore)
			for _, agent := range []string{"idle", "busy"} {
				_, err := s.UpsertAgentSession(ctx, start("alice", agent, 1_000))
				noErr(t, err)
			}
			noErr(t, s.TouchAgentSession(ctx, store.AgentSessionTouch{IdentityID: "alice", AgentID: "busy", AgentName: "busy-name", Kind: "agent", Tool: "claim_next", At: 5_000}))
			n, err := s.DeleteIdleAgentSessions(ctx, 5_000-2_000)
			noErr(t, err)
			rows, err := s.ListAgentSessions(ctx, "alice", 0)
			noErr(t, err)
			if n != 1 || len(rows) != 1 || rows[0].AgentID != "busy" {
				t.Fatalf("a sweep of sessions idle 2 s at 5000 deleted %d and left %+v, want 1 and busy", n, rows)
			}
		})

		t.Run("DeleteDeletesOneAndAMissingOneIsNotFound", func(t *testing.T) {
			s := migrated(t, newStore)
			_, err := s.UpsertAgentSession(ctx, start("alice", "a1", 100))
			noErr(t, err)
			noErr(t, s.DeleteAgentSession(ctx, "alice", "a1"))
			wantErr(t, s.DeleteAgentSession(ctx, "alice", "a1"), domain.CodeNotFound, "AgentSession")
		})

		t.Run("TheRefusals", func(t *testing.T) {
			s := migrated(t, newStore)
			for _, in := range []store.AgentSessionStart{
				{AgentID: "a", AgentName: "n", Kind: "agent", At: 1},
				{IdentityID: "i", AgentName: "n", Kind: "agent", At: 1},
				{IdentityID: "i", AgentID: "a", Kind: "agent", At: 1},
				{IdentityID: "i", AgentID: "a", AgentName: "n", At: 1},
			} {
				_, err := s.UpsertAgentSession(ctx, in)
				wantErr(t, err, domain.CodeValidation, "")
			}
			for _, in := range []store.AgentSessionTouch{
				{IdentityID: "i", AgentID: "a", AgentName: "n", Kind: "agent", At: 1},
				{IdentityID: "i", AgentID: "a", AgentName: "n", Kind: "agent", Tool: "t", Count: -1, At: 1},
			} {
				wantErr(t, s.TouchAgentSession(ctx, in), domain.CodeValidation, "")
			}
			rows, err := s.ListAgentSessions(ctx, "", 0)
			noErr(t, err)
			if len(rows) != 0 {
				t.Fatalf("refused calls left %+v", rows)
			}
		})
	})
}
