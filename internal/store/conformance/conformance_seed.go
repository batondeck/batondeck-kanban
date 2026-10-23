package conformance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// What the batteries of store contract II (conformance_{context,memory,summaries,dependencies,
// subtasks,audit,toolcalls,agentsessions,wakesessions,attachments,blobs,search}.go) share. Each
// registers with Run, which gives it the subtest named for its area; that subtest SKIPS, naming the
// task that builds the area, while the store's methods are unbuilt (store.UnbuiltTask), so a store
// is never failed for a lane not merged yet. A battery whose rows hang off a task stands on a
// seeder; a store gets one through WithSeeder, the adapter over its own store.Projects, Boards and
// Tasks, and a built area on a store without one FAILS.

// seedTaskInput is a task to put in place. Status "" is TODO.
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

// WithSeeder is the seeder adapter: s, with the seeder the contract II batteries stand on, which
// seeds through s's own Projects, Boards and Tasks and reads a task back through Tasks.GetTask.
// An engine's or memstore's factory returns WithSeeder(store); every other call goes to s.
func WithSeeder(s Migratable) Migratable { return storeSeeder{s} }

type storeSeeder struct{ Migratable }

// seedIdentity creates and owns every seeded row.
const seedIdentity = "seeder"

func (s storeSeeder) seedProject(ctx context.Context) (string, string, error) {
	p, err := s.CreateProject(ctx, store.CreateProjectInput{Name: "seeded", CreatedBy: seedIdentity}, store.WriteOptions{Identity: seedIdentity})
	if err != nil {
		return "", "", err
	}
	b, err := s.seedBoard(ctx, p.ID)
	return p.ID, b, err
}

// seedBoard is a board of the default columns, one per status but REVIEW's when the project has
// review off: a task is seeded into the column of its status.
func (s storeSeeder) seedBoard(ctx context.Context, projectID string) (string, error) {
	b, err := s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: projectID, Name: "seeded", CreatedBy: seedIdentity}, store.WriteOptions{Identity: seedIdentity})
	return b.ID, err
}

func (s storeSeeder) seedTask(ctx context.Context, in seedTaskInput) (string, error) {
	status := in.Status
	if status == "" {
		status = domain.Todo
	}
	b, _, err := s.GetBoard(ctx, in.ProjectID, in.BoardID)
	if err != nil {
		return "", err
	}
	column := ""
	for _, c := range b.Columns {
		if c.Status == status {
			column = c.ID
			break
		}
	}
	if column == "" {
		return "", fmt.Errorf("seeder: board %s has no %s column", in.BoardID, status)
	}
	var parent *string
	if in.ParentTaskID != "" {
		parent = &in.ParentTaskID
	}
	t, err := s.CreateTask(ctx, store.CreateTaskInput{
		ProjectID: in.ProjectID, BoardID: in.BoardID, ColumnID: column, Title: in.Title, Description: in.Description,
		Status: status, ParentTaskID: parent, OrderKey: "a0", CreatedBy: seedIdentity, CreatedAt: time.Now().UnixMilli(),
	})
	return t.ID, err
}

func (s storeSeeder) taskState(ctx context.Context, projectID, taskID string) (seededTask, error) {
	t, err := s.GetTask(ctx, projectID, taskID)
	return seededTask{Version: t.Version, BlockedBy: t.BlockedBy, Blocks: t.Blocks}, err
}

// area runs battery inside the subtest Run gave the area, or skips that subtest when probe's call, on
// a migrated store of its own, is unbuilt on the store.
func area(t *testing.T, name string, newStore Factory, probe func(store.Store) error, battery func(t *testing.T)) {
	if task, unbuilt := store.UnbuiltTask(probe(migrated(t, newStore))); unbuilt {
		t.Skipf("%s is not built on this store yet: %s builds it", name, task)
	}
	battery(t)
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

// seeded is a migrated store and its seeder, with one project and board in place. It fails when
// the store cannot seed: area() has already found the area built, so a missing seeder would
// otherwise pass the battery as a skip.
func seeded(t *testing.T, newStore Factory) (store.Store, seeder, string, string) {
	t.Helper()
	s := migrated(t, newStore)
	sd, ok := s.(seeder)
	if !ok {
		t.Fatalf("the area is built, but this store has no seeder for the projects, boards and tasks the battery stands on: its factory must return conformance.WithSeeder(store)")
	}
	p, b, err := sd.seedProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, sd, p, b
}

// seedTasks puts one TODO task per title on the board and returns their ids in order.
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
