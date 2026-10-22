package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// wakeSessions is the wake-session area: put, get and delete, a replace keeping its creation time,
// expiry judged at the read, and the refusals.
func wakeSessions(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.GetWakeSession(ctx, "probe")
		return err
	}
	area(t, "WakeSessions", newStore, probe, func(t *testing.T) {
		live := func(id string, created int64) store.WakeSession {
			return store.WakeSession{ID: id, IdentityID: "alice", AgentID: "a1", CreatedAt: created, LastSeenAt: created, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}
		}

		t.Run("PutGetReplaceDelete", func(t *testing.T) {
			s := migrated(t, newStore)
			in := live("W-1", 100)
			in.SocketPath, in.SocketTokenSealed, in.LastCursor = "/tmp/inbox.sock", []byte{1, 2, 3}, 7
			noErr(t, s.PutWakeSession(ctx, in))
			got, err := s.GetWakeSession(ctx, "W-1")
			noErr(t, err)
			if got.ID != "W-1" || got.IdentityID != "alice" || got.AgentID != "a1" || got.SocketPath != in.SocketPath || string(got.SocketTokenSealed) != string(in.SocketTokenSealed) || got.LastCursor != 7 || got.CreatedAt != 100 || got.ExpiresAt != in.ExpiresAt {
				t.Fatalf("GetWakeSession = %+v, want %+v", got, in)
			}
			again := live("W-1", 999)
			again.LastSeenAt, again.LastCursor = 500, 9
			noErr(t, s.PutWakeSession(ctx, again))
			got, err = s.GetWakeSession(ctx, "W-1")
			noErr(t, err)
			if got.CreatedAt != 100 || got.LastSeenAt != 500 || got.LastCursor != 9 || got.SocketPath != "" || got.SocketTokenSealed != nil {
				t.Fatalf("after the replace %+v: created at kept, every other field replaced", got)
			}
			noErr(t, s.DeleteWakeSession(ctx, "W-1"))
			_, err = s.GetWakeSession(ctx, "W-1")
			wantErr(t, err, domain.CodeNotFound, "WakeSession")
			wantErr(t, s.DeleteWakeSession(ctx, "W-1"), domain.CodeNotFound, "WakeSession")
		})

		t.Run("AnExpiredSessionReadsAsMissingAndCanBeDeleted", func(t *testing.T) {
			s := migrated(t, newStore)
			in := live("W-2", 100)
			in.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
			noErr(t, s.PutWakeSession(ctx, in))
			_, err := s.GetWakeSession(ctx, "W-2")
			wantErr(t, err, domain.CodeNotFound, "WakeSession")
			noErr(t, s.DeleteWakeSession(ctx, "W-2"))
		})

		t.Run("TheRefusals", func(t *testing.T) {
			s := migrated(t, newStore)
			for _, in := range []store.WakeSession{
				{IdentityID: "alice", AgentID: "a1", ExpiresAt: 1},
				{ID: "W-3", AgentID: "a1", ExpiresAt: 1},
				{ID: "W-3", IdentityID: "alice", ExpiresAt: 1},
				{ID: "W-3", IdentityID: "alice", AgentID: "a1"},
			} {
				wantErr(t, s.PutWakeSession(ctx, in), domain.CodeValidation, "")
			}
			wantErr(t, s.DeleteWakeSession(ctx, "W-3"), domain.CodeNotFound, "WakeSession")
		})
	})
}
