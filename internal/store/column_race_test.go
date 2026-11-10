package store

import (
	"context"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// heldWrite runs write inside a transaction on s and holds the transaction open until release is
// closed: started is closed once write has run. The transaction's error arrives on the result.
func heldWrite(s Store, write func(tx Store) error) (started, release chan struct{}, result chan error) {
	started, release, result = make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		result <- s.Atomically(context.Background(), func(tx Store) error {
			if err := write(tx); err != nil {
				close(started)
				return err
			}
			close(started)
			<-release
			return nil
		})
	}()
	return started, release, result
}

// columnRace is a Postgres store holding one TODO task and, beside the default columns, an empty
// extra column of status.
func columnRace(t *testing.T, status domain.Status) (s Store, q FrontierQuery, task Task, extra Column) {
	t.Helper()
	s = openPostgresStore(t)
	q, _ = raceBoard(t, s, 1)
	ctx := context.Background()
	extra, err := s.AddColumn(ctx, q.ProjectID, q.BoardID, ColumnInput{Name: "extra", Status: status}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if task, err = s.GetTask(ctx, q.ProjectID, "T-1"); err != nil {
		t.Fatal(err)
	}
	return s, q, task, extra
}

// stranded fails when the task sits in a column its board no longer has.
func stranded(t *testing.T, s Store, q FrontierQuery, taskID string) {
	t.Helper()
	ctx := context.Background()
	got, err := s.GetTask(ctx, q.ProjectID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.GetBoard(ctx, q.ProjectID, q.BoardID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range b.Columns {
		if c.ID == got.ColumnID {
			return
		}
	}
	t.Fatalf("STRANDED: task %s sits in column %s, which the board no longer has", taskID, got.ColumnID)
}

// On Postgres, remove_column and a write that puts a task into that column take turns on the column
// row: whichever commits first, the column is either refused for removal (it holds the task) or the
// write is NOT_FOUND (the column is gone), and no task is left in a column the board no longer has.
func TestPostgresRemoveColumnRacesAWriteIntoIt(t *testing.T) {
	ctx := context.Background()
	writes := map[string]func(tx Store, q FrontierQuery, task Task, col string) error{
		"set_status": func(tx Store, q FrontierQuery, task Task, col string) error {
			_, err := tx.SetStatus(ctx, q.ProjectID, task.ID, task.Version, StatusChange{Status: domain.Todo, ColumnID: col, UpdatedBy: "bob", UpdatedAt: raceClock})
			return err
		},
		"set_order_key": func(tx Store, q FrontierQuery, task Task, col string) error {
			_, err := tx.SetOrderKey(ctx, q.ProjectID, task.ID, task.Version, "a1", col, "bob", raceClock)
			return err
		},
		"create_task": func(tx Store, q FrontierQuery, _ Task, col string) error {
			_, err := tx.CreateTask(ctx, CreateTaskInput{ProjectID: q.ProjectID, BoardID: q.BoardID, ColumnID: col, Title: "new",
				Status: domain.Todo, OrderKey: "a0", CreatedBy: "bob", CreatedAt: raceClock})
			return err
		},
	}
	for name, write := range writes {
		t.Run(name+" first", func(t *testing.T) {
			s, q, task, extra := columnRace(t, domain.Todo)
			started, release, result := heldWrite(s, func(tx Store) error { return write(tx, q, task, extra.ID) })
			<-started
			removed := make(chan error, 1)
			go func() { _, err := s.RemoveColumn(ctx, q.ProjectID, q.BoardID, extra.ID); removed <- err }()
			time.Sleep(300 * time.Millisecond) // remove_column reaches the column row while the write holds it
			close(release)
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			err := <-removed
			if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeValidation {
				t.Errorf("remove_column after a write into the column: %v, want VALIDATION (it holds a task)", err)
			}
			page, err := s.ListTasks(ctx, q.ProjectID, TaskFilter{BoardID: q.BoardID, ColumnID: extra.ID}, "", 10)
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range page.Tasks {
				stranded(t, s, q, x.ID)
			}
		})
		t.Run("remove_column first, then "+name, func(t *testing.T) {
			s, q, task, extra := columnRace(t, domain.Todo)
			started, release, result := heldWrite(s, func(tx Store) error {
				_, err := tx.RemoveColumn(ctx, q.ProjectID, q.BoardID, extra.ID)
				return err
			})
			<-started
			wrote := make(chan error, 1)
			go func() {
				wrote <- s.Atomically(ctx, func(tx Store) error { return write(tx, q, task, extra.ID) })
			}()
			time.Sleep(300 * time.Millisecond) // the write reaches the column row while remove_column holds it
			close(release)
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			err := <-wrote
			if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeNotFound {
				t.Errorf("%s into a column removed meanwhile: %v, want NOT_FOUND Column", name, err)
			}
			stranded(t, s, q, task.ID)
		})
	}

	// claim_next lands its task in the IN_PROGRESS column it names, through its own statement.
	t.Run("claim_next first", func(t *testing.T) {
		s, q, _, extra := columnRace(t, domain.InProgress)
		in := ClaimInput{Owner: "bob", LeaseID: "L-1", ExpiresAt: raceClock + 60_000, Now: raceClock, ColumnID: extra.ID}
		started, release, result := heldWrite(s, func(tx Store) error {
			_, ok, err := tx.ClaimNext(ctx, q, in)
			if err == nil && !ok {
				t.Error("claim_next claimed nothing")
			}
			return err
		})
		<-started
		removed := make(chan error, 1)
		go func() { _, err := s.RemoveColumn(ctx, q.ProjectID, q.BoardID, extra.ID); removed <- err }()
		time.Sleep(300 * time.Millisecond)
		close(release)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if e, ok := domain.AsBatonDeckError(<-removed); !ok || e.Code != domain.CodeValidation {
			t.Errorf("remove_column after claim_next into the column: %v, want VALIDATION", e)
		}
		stranded(t, s, q, "T-1")
	})
	t.Run("remove_column first, then claim_next", func(t *testing.T) {
		s, q, _, extra := columnRace(t, domain.InProgress)
		started, release, result := heldWrite(s, func(tx Store) error {
			_, err := tx.RemoveColumn(ctx, q.ProjectID, q.BoardID, extra.ID)
			return err
		})
		<-started
		claimed := make(chan error, 1)
		go func() {
			in := ClaimInput{Owner: "bob", LeaseID: "L-1", ExpiresAt: raceClock + 60_000, Now: raceClock, ColumnID: extra.ID}
			claimed <- s.Atomically(ctx, func(tx Store) error { _, _, err := tx.ClaimNext(ctx, q, in); return err })
		}()
		time.Sleep(300 * time.Millisecond)
		close(release)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if e, ok := domain.AsBatonDeckError(<-claimed); !ok || e.Code != domain.CodeNotFound {
			t.Errorf("claim_next into a column removed meanwhile: %v, want NOT_FOUND Column", e)
		}
		stranded(t, s, q, "T-1")
	})
}
