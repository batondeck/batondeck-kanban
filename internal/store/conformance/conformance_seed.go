package conformance

import (
	"context"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// What the batteries of store contract II (conformance_{context,memory,summaries,dependencies,
// subtasks,audit,toolcalls,agentsessions,wakesessions,attachments,blobs,search}.go) share. Each runs
// as one subtest named for its area, which SKIPS, naming the task that builds the area, while the
// engine's methods are unbuilt (store.UnbuiltTask), so an engine is never failed for a lane not
// merged yet. A battery whose rows hang off a task stands on a seeder; no engine offers one until
// store.Tasks exists (R0.18), and until then such a battery skips saying so.

// seedTaskInput is a task to put in place. Status "" is READY.
type seedTaskInput struct {
	ProjectID    string
	BoardID      string
	Title        string
	Description  string
	Status       domain.Status
	ParentTaskID string
}

// seededTask is what a battery reads back from a task it seeded: its version and its two
// dependency caches.
type seededTask struct {
	Version   int64
	BlockedBy []string
	Blocks    []string
}

// seeder puts the projects, boards and tasks a battery stands on in place, through areas other
// lanes own (Projects, Boards, Tasks), and reads a task's version and caches back.
type seeder interface {
	seedProject(ctx context.Context) (projectID, boardID string, err error)
	seedBoard(ctx context.Context, projectID string) (string, error)
	seedTask(ctx context.Context, in seedTaskInput) (string, error)
	taskState(ctx context.Context, projectID, taskID string) (seededTask, error)
}

// area runs battery as the subtest name, or skips it when probe's call, on a migrated store of its
// own, is unbuilt on the engine.
func area(t *testing.T, name string, newStore Factory, probe func(store.Store) error, battery func(t *testing.T)) {
	t.Run(name, func(t *testing.T) {
		if task, unbuilt := store.UnbuiltTask(probe(migrated(t, newStore))); unbuilt {
			t.Skipf("%s is not built on this engine yet: %s builds it", name, task)
		}
		battery(t)
	})
}

// noErr fails on err, or skips when the method is unbuilt on the engine.
func noErr(t *testing.T, err error) {
	t.Helper()
	if task, unbuilt := store.UnbuiltTask(err); unbuilt {
		t.Skipf("unbuilt on this engine (%s): %v", task, err)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// wantErr fails unless err is a BatonDeckError with code and, when entity is given, names that
// entity: the error shape a caller branches on, the same on every engine.
func wantErr(t *testing.T, err error, code domain.Code, entity string) *domain.BatonDeckError {
	t.Helper()
	if task, unbuilt := store.UnbuiltTask(err); unbuilt {
		t.Skipf("unbuilt on this engine (%s): %v", task, err)
	}
	e, ok := domain.AsBatonDeckError(err)
	if !ok {
		t.Fatalf("got %v, want a %s BatonDeckError", err, code)
	}
	if e.Code != code {
		t.Fatalf("got %s (%s), want %s", e.Code, e.Message, code)
	}
	if entity != "" && e.Details["entity"] != entity {
		t.Fatalf("%s names entity %v, want %s", code, e.Details["entity"], entity)
	}
	return e
}

// seeded is a migrated store and its seeder, with one project and board in place; it skips when
// the store cannot seed.
func seeded(t *testing.T, newStore Factory) (store.Store, seeder, string, string) {
	t.Helper()
	s := migrated(t, newStore)
	sd, ok := s.(seeder)
	if !ok {
		t.Skip("this battery stands on projects, boards and tasks, which this store cannot seed until store.Tasks exists (R0.18)")
	}
	p, b, err := sd.seedProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, sd, p, b
}

// seedTasks puts one READY task per title on the board and returns their ids in order.
func seedTasks(t *testing.T, sd seeder, projectID, boardID string, titles ...string) []string {
	t.Helper()
	ids := make([]string, len(titles))
	for i, title := range titles {
		id, err := sd.seedTask(context.Background(), seedTaskInput{ProjectID: projectID, BoardID: boardID, Title: title})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	return ids
}

func stateOf(t *testing.T, sd seeder, projectID, taskID string) seededTask {
	t.Helper()
	st, err := sd.taskState(context.Background(), projectID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
