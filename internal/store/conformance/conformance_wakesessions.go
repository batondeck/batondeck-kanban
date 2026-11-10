package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("wakesessions", wakeSessions) }

// wakeSessions is the wake-session area: put, get, list, the cursor and delete, a replace keeping its
// creation time, expiry judged at the read (and not by the list), one pid per session of a device,
// and the refusals.
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

		t.Run("TheDirectoryFieldsListAndCursor", func(t *testing.T) {
			s := migrated(t, newStore)
			a := live("W-a", 200)
			a.PID, a.Role, a.ClientSessionID, a.ProcStart, a.BearerSealed = 4242, "worker", "f73a2587", "Fri Oct 23 08:36:23 2026", []byte{9, 8}
			noErr(t, s.PutWakeSession(ctx, a))
			b := live("W-b", 100)
			b.ExpiresAt = time.Now().Add(-time.Second).UnixMilli() // expired: still listed
			noErr(t, s.PutWakeSession(ctx, b))
			c := live("W-c", 100) // no pid, like b: two of them are allowed
			noErr(t, s.PutWakeSession(ctx, c))
			list, err := s.ListWakeSessions(ctx)
			noErr(t, err)
			if len(list) != 3 || list[0].ID != "W-b" || list[1].ID != "W-c" || list[2].ID != "W-a" {
				t.Fatalf("ListWakeSessions = %+v, want W-b, W-c (created 100, by id) then W-a", list)
			}
			got := list[2]
			if got.PID != 4242 || got.Role != "worker" || got.ClientSessionID != "f73a2587" || got.ProcStart != a.ProcStart || string(got.BearerSealed) != string(a.BearerSealed) {
				t.Fatalf("the directory fields read back %+v, want %+v", got, a)
			}
			taken := live("W-d", 300)
			taken.PID = 4242
			wantErr(t, s.PutWakeSession(ctx, taken), domain.CodeValidation, "")
			// P-167: a pid is unique per device. Another device's session holds the same pid number,
			// and reads back its device; a second one there is refused.
			there := live("W-e", 300)
			there.PID, there.DeviceID = 4242, "dev-b"
			noErr(t, s.PutWakeSession(ctx, there))
			if got, err := s.GetWakeSession(ctx, "W-e"); err != nil || got.DeviceID != "dev-b" || got.PID != 4242 {
				t.Fatalf("another device's session of pid 4242 read back %+v, %v", got, err)
			}
			again := live("W-f", 300)
			again.PID, again.DeviceID = 4242, "dev-b"
			wantErr(t, s.PutWakeSession(ctx, again), domain.CodeValidation, "")
			noErr(t, s.DeleteWakeSession(ctx, "W-e"))
			a.Role = "master" // the same id may keep its pid
			noErr(t, s.PutWakeSession(ctx, a))
			noErr(t, s.SetWakeSessionCursor(ctx, "W-a", 77, 900))
			got, err = s.GetWakeSession(ctx, "W-a")
			noErr(t, err)
			if got.LastCursor != 77 || got.LastSeenAt != 900 || got.Role != "master" || got.PID != 4242 {
				t.Fatalf("after SetWakeSessionCursor %+v", got)
			}
			wantErr(t, s.SetWakeSessionCursor(ctx, "W-none", 1, 1), domain.CodeNotFound, "WakeSession")
		})

		t.Run("TheRefusals", func(t *testing.T) {
			s := migrated(t, newStore)
			for _, in := range []store.WakeSession{
				{IdentityID: "alice", AgentID: "a1", ExpiresAt: 1},
				{ID: "W-3", AgentID: "a1", ExpiresAt: 1},
				{ID: "W-3", IdentityID: "alice", ExpiresAt: 1},
				{ID: "W-3", IdentityID: "alice", AgentID: "a1"},
				{ID: "W-3", IdentityID: "alice", AgentID: "a1", ExpiresAt: 1, Role: "admin"},
				{ID: "W-3", IdentityID: "alice", AgentID: "a1", ExpiresAt: 1, PID: -1},
			} {
				wantErr(t, s.PutWakeSession(ctx, in), domain.CodeValidation, "")
			}
			wantErr(t, s.DeleteWakeSession(ctx, "W-3"), domain.CodeNotFound, "WakeSession")
		})
	})
}
