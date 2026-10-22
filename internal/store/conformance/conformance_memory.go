package conformance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// memory is the memory area: routing by scope, the agent scope's privacy, durable recall by project
// and by identity, the TTL judged at the read before any sweep, the bounded sweep, the per-task
// delete, and the refusals.
func memory(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.RecallMemory(ctx, store.RecallMemoryQuery{Scope: store.MemoryAgentGlobal, Identity: "probe", Limit: 1})
		return err
	}
	area(t, "Memory", newStore, probe, func(t *testing.T) {
		write := func(t *testing.T, s store.Store, in store.WriteMemoryInput) store.MemoryEntry {
			t.Helper()
			if in.Author == "" {
				in.Author = "alice"
			}
			if in.Value == nil {
				in.Value = json.RawMessage(`"v"`)
			}
			e, err := s.WriteMemory(ctx, in)
			noErr(t, err)
			time.Sleep(2 * time.Millisecond)
			return e
		}
		keys := func(es []store.MemoryEntry) []string {
			out := make([]string, len(es))
			for i, e := range es {
				out[i] = e.Key
			}
			return out
		}

		t.Run("TaskScopesReadBackNewestFirstAndAgentEntriesArePrivate", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			first := write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryTask, Key: "k-task", Value: json.RawMessage(`{"n":1}`)})
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryShared, Key: "k-shared"})
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryAgent, Key: "k-agent", Author: "alice"})
			if first.ID == "" || first.TS == 0 || first.ProjectID != p || first.TaskID != task || first.Scope != store.MemoryTask || string(first.Value) != `{"n":1}` || first.ExpiresAt != 0 {
				t.Fatalf("WriteMemory = %+v", first)
			}
			alice, err := s.ReadMemory(ctx, p, task, store.ReadMemoryQuery{Identity: "alice"})
			noErr(t, err)
			if got := keys(alice); !slices.Equal(got, []string{"k-agent", "k-shared", "k-task"}) {
				t.Fatalf("alice reads %v, want [k-agent k-shared k-task]", got)
			}
			bob, err := s.ReadMemory(ctx, p, task, store.ReadMemoryQuery{Identity: "bob"})
			noErr(t, err)
			if got := keys(bob); !slices.Equal(got, []string{"k-shared", "k-task"}) {
				t.Fatalf("bob reads %v, want [k-shared k-task]: alice's agent entry is hers", got)
			}
			byScope, err := s.ReadMemory(ctx, p, task, store.ReadMemoryQuery{Identity: "alice", Scope: store.MemoryShared})
			noErr(t, err)
			byKey, err := s.ReadMemory(ctx, p, task, store.ReadMemoryQuery{Identity: "alice", Key: "k-task"})
			noErr(t, err)
			if !slices.Equal(keys(byScope), []string{"k-shared"}) || !slices.Equal(keys(byKey), []string{"k-task"}) {
				t.Fatalf("by scope %v, by key %v", keys(byScope), keys(byKey))
			}
			limited, err := s.ReadMemory(ctx, p, task, store.ReadMemoryQuery{Identity: "alice", Limit: 1})
			noErr(t, err)
			if !slices.Equal(keys(limited), []string{"k-agent"}) {
				t.Fatalf("limit 1 reads %v, want the newest", keys(limited))
			}
		})

		t.Run("ReadTakesTheNewestWindowFirstThenFilters", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryTask, Key: "old"})
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryShared, Key: "n1"})
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryAgent, Key: "bobs", Author: "bob"})
			read := func(q store.ReadMemoryQuery) []string {
				t.Helper()
				q.Identity = "alice"
				es, err := s.ReadMemory(ctx, p, task, q)
				noErr(t, err)
				return keys(es)
			}
			if got := read(store.ReadMemoryQuery{Key: "old", Limit: 2}); len(got) != 0 {
				t.Fatalf("key old in a window of 2 reads %v, want none: the window [bobs n1] is cut before the key filter", got)
			}
			if got := read(store.ReadMemoryQuery{Key: "old", Limit: 3}); !slices.Equal(got, []string{"old"}) {
				t.Fatalf("key old in a window of 3 reads %v, want [old]", got)
			}
			if got := read(store.ReadMemoryQuery{Limit: 1}); len(got) != 0 {
				t.Fatalf("alice with a window of 1 reads %v, want none: bob's private entry holds the slot", got)
			}
		})

		t.Run("RecallWithNoKeyIsTheNewestKThenLiveAndWithAKeyLiveThenK", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			past := time.Now().Add(-time.Second).UnixMilli()
			write(t, s, store.WriteMemoryInput{ProjectID: p, Scope: store.MemoryProject, Key: "k"})
			write(t, s, store.WriteMemoryInput{ProjectID: p, Scope: store.MemoryProject, Key: "p2"})
			write(t, s, store.WriteMemoryInput{ProjectID: p, Scope: store.MemoryProject, Key: "k", ExpiresAt: past})
			recall := func(key string, limit int) []string {
				t.Helper()
				es, err := s.RecallMemory(ctx, store.RecallMemoryQuery{Scope: store.MemoryProject, ProjectID: p, Key: key, Limit: limit})
				noErr(t, err)
				return keys(es)
			}
			if got := recall("", 2); !slices.Equal(got, []string{"p2"}) {
				t.Fatalf("no key, k 2 recalls %v, want [p2]: the newest two, then the expired one left out", got)
			}
			if got := recall("", 3); !slices.Equal(got, []string{"p2", "k"}) {
				t.Fatalf("no key, k 3 recalls %v, want [p2 k] newest first", got)
			}
			if got := recall("k", 1); !slices.Equal(got, []string{"k"}) {
				t.Fatalf("key k, k 1 recalls %v, want the live k: a keyed recall drops the expired before k", got)
			}
		})

		t.Run("ATTLRowIsReadableAtOnceAndAbsentAfterExpiryBeforeAnySweep", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			exp := time.Now().Add(time.Second).UnixMilli()
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryTask, Key: "ttl-task", ExpiresAt: exp})
			write(t, s, store.WriteMemoryInput{ProjectID: p, Scope: store.MemoryProject, Key: "ttl-project", ExpiresAt: exp})
			write(t, s, store.WriteMemoryInput{Scope: store.MemoryAgentGlobal, Key: "ttl-global", Author: "alice", ExpiresAt: exp})
			read := func() (onTask, onProject, global []string) {
				a, err := s.ReadMemory(ctx, p, task, store.ReadMemoryQuery{Identity: "alice"})
				noErr(t, err)
				b, err := s.RecallMemory(ctx, store.RecallMemoryQuery{Scope: store.MemoryProject, ProjectID: p, Limit: 10})
				noErr(t, err)
				c, err := s.RecallMemory(ctx, store.RecallMemoryQuery{Scope: store.MemoryAgentGlobal, Identity: "alice", Limit: 10})
				noErr(t, err)
				return keys(a), keys(b), keys(c)
			}
			a, b2, c := read()
			if !slices.Equal(a, []string{"ttl-task"}) || !slices.Equal(b2, []string{"ttl-project"}) || !slices.Equal(c, []string{"ttl-global"}) {
				t.Fatalf("at once: task %v, project %v, agent_global %v; want each entry", a, b2, c)
			}
			time.Sleep(2 * time.Second)
			a, b2, c = read()
			if len(a) != 0 || len(b2) != 0 || len(c) != 0 {
				t.Fatalf("after 2 s, before any sweep: task %v, project %v, agent_global %v; want none", a, b2, c)
			}
			n, err := s.DeleteExpiredMemory(ctx, time.Now().UnixMilli(), 10)
			noErr(t, err)
			if n != 3 {
				t.Fatalf("the sweep after the reads deleted %d, want the 3 expired entries", n)
			}
		})

		t.Run("RecallReadsOneProjectOrOneIdentity", func(t *testing.T) {
			s, sd, p, _ := seeded(t, newStore)
			q, _, err := sd.seedProject(ctx)
			noErr(t, err)
			write(t, s, store.WriteMemoryInput{ProjectID: p, Scope: store.MemoryProject, Key: "p-old"})
			write(t, s, store.WriteMemoryInput{ProjectID: p, Scope: store.MemoryProject, Key: "p-new"})
			write(t, s, store.WriteMemoryInput{ProjectID: q, Scope: store.MemoryProject, Key: "q-only"})
			write(t, s, store.WriteMemoryInput{ProjectID: p, Scope: store.MemoryAgentGlobal, Key: "alice-g", Author: "alice"})
			write(t, s, store.WriteMemoryInput{Scope: store.MemoryAgentGlobal, Key: "bob-g", Author: "bob"})
			recall := func(q store.RecallMemoryQuery) []string {
				t.Helper()
				es, err := s.RecallMemory(ctx, q)
				noErr(t, err)
				return keys(es)
			}
			if got := recall(store.RecallMemoryQuery{Scope: store.MemoryProject, ProjectID: p, Limit: 10}); !slices.Equal(got, []string{"p-new", "p-old"}) {
				t.Fatalf("project %s recalls %v, want [p-new p-old]", p, got)
			}
			if got := recall(store.RecallMemoryQuery{Scope: store.MemoryProject, ProjectID: p, Key: "p-old", Limit: 10}); !slices.Equal(got, []string{"p-old"}) {
				t.Fatalf("by key %v, want [p-old]", got)
			}
			if got := recall(store.RecallMemoryQuery{Scope: store.MemoryProject, ProjectID: p, Limit: 1}); !slices.Equal(got, []string{"p-new"}) {
				t.Fatalf("limit 1 %v, want [p-new]", got)
			}
			if got := recall(store.RecallMemoryQuery{Scope: store.MemoryAgentGlobal, Identity: "alice", Limit: 10}); !slices.Equal(got, []string{"alice-g"}) {
				t.Fatalf("alice recalls %v, want [alice-g]", got)
			}
			es, err := s.RecallMemory(ctx, store.RecallMemoryQuery{Scope: store.MemoryAgentGlobal, Identity: "alice", Limit: 10})
			noErr(t, err)
			if es[0].ProjectID != "" || es[0].TaskID != "" {
				t.Fatalf("an agent_global entry keeps project %q task %q, want neither", es[0].ProjectID, es[0].TaskID)
			}
			if got := recall(store.RecallMemoryQuery{Scope: store.MemoryProject, ProjectID: "P-missing", Limit: 10}); len(got) != 0 {
				t.Fatalf("a missing project recalls %v, want none", got)
			}
		})

		t.Run("TheSweepIsBoundedByItsLimit", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			past := time.Now().Add(-time.Second).UnixMilli()
			for _, k := range []string{"x", "y", "z"} {
				write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryTask, Key: k, ExpiresAt: past})
			}
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryTask, Key: "live"})
			now := time.Now().UnixMilli()
			for _, want := range []int64{2, 1, 0} {
				n, err := s.DeleteExpiredMemory(ctx, now, 2)
				noErr(t, err)
				if n != want {
					t.Fatalf("a sweep of limit 2 deleted %d, want %d", n, want)
				}
			}
			live, err := s.ReadMemory(ctx, p, task, store.ReadMemoryQuery{Identity: "alice"})
			noErr(t, err)
			if !slices.Equal(keys(live), []string{"live"}) {
				t.Fatalf("after the sweeps %v, want [live]", keys(live))
			}
			_, err = s.DeleteExpiredMemory(ctx, now, 0)
			wantErr(t, err, domain.CodeValidation, "")
		})

		t.Run("DeleteByTaskKeepsOtherTasksAndProjectEntries", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			tasks := seedTasks(t, sd, p, b, "a", "b")
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: tasks[0], Scope: store.MemoryTask, Key: "a1"})
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: tasks[0], Scope: store.MemoryAgent, Key: "a2"})
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: tasks[1], Scope: store.MemoryTask, Key: "b1"})
			write(t, s, store.WriteMemoryInput{ProjectID: p, TaskID: tasks[0], Scope: store.MemoryProject, Key: "learned-on-a"})
			n, err := s.DeleteMemoryByTask(ctx, p, tasks[0])
			noErr(t, err)
			if n != 2 {
				t.Fatalf("deleted %d, want 2", n)
			}
			other, err := s.ReadMemory(ctx, p, tasks[1], store.ReadMemoryQuery{Identity: "alice"})
			noErr(t, err)
			durable, err := s.RecallMemory(ctx, store.RecallMemoryQuery{Scope: store.MemoryProject, ProjectID: p, Limit: 10})
			noErr(t, err)
			if !slices.Equal(keys(other), []string{"b1"}) || !slices.Equal(keys(durable), []string{"learned-on-a"}) || durable[0].TaskID != tasks[0] {
				t.Fatalf("other task %v, project entries %+v", keys(other), durable)
			}
		})

		t.Run("WriteRefusesBadRouting", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			for _, in := range []store.WriteMemoryInput{
				{ProjectID: p, Scope: store.MemoryTask, Key: "k", Author: "alice"},
				{ProjectID: p, TaskID: task, Scope: "team", Key: "k", Author: "alice"},
				{Scope: store.MemoryProject, Key: "k", Author: "alice"},
				{ProjectID: p, TaskID: task, Scope: store.MemoryTask, Key: "", Author: "alice"},
				{ProjectID: p, TaskID: task, Scope: store.MemoryTask, Key: "k", Author: ""},
			} {
				_, err := s.WriteMemory(ctx, in)
				wantErr(t, err, domain.CodeValidation, "")
			}
			_, err := s.WriteMemory(ctx, store.WriteMemoryInput{ProjectID: p, TaskID: "T-404", Scope: store.MemoryTask, Key: "k", Author: "alice"})
			wantErr(t, err, domain.CodeNotFound, "Task")
			_, err = s.WriteMemory(ctx, store.WriteMemoryInput{ProjectID: "P-missing", Scope: store.MemoryProject, Key: "k", Author: "alice"})
			wantErr(t, err, domain.CodeNotFound, "Project")
			_, err = s.WriteMemory(ctx, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryTask, Key: "k", Author: "alice", BlobSHA256: strings.Repeat("f", 64)})
			wantErr(t, err, domain.CodeNotFound, "Blob")
			left, err := s.ReadMemory(ctx, p, task, store.ReadMemoryQuery{Identity: "alice"})
			noErr(t, err)
			if len(left) != 0 {
				t.Fatalf("refused writes left %v", keys(left))
			}
		})

		t.Run("ReadAndRecallRefuseTheirOtherScopes", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			_, err := s.ReadMemory(ctx, p, "T-404", store.ReadMemoryQuery{Identity: "alice"})
			wantErr(t, err, domain.CodeNotFound, "Task")
			_, err = s.ReadMemory(ctx, p, task, store.ReadMemoryQuery{Identity: "alice", Scope: store.MemoryProject})
			wantErr(t, err, domain.CodeValidation, "")
			for _, q := range []store.RecallMemoryQuery{
				{Scope: store.MemoryTask, ProjectID: p, Limit: 10},
				{Scope: store.MemoryProject, ProjectID: p, Limit: 0},
				{Scope: store.MemoryProject, ProjectID: p, Limit: 1001},
			} {
				_, err = s.RecallMemory(ctx, q)
				wantErr(t, err, domain.CodeValidation, "")
			}
		})
	})
}
