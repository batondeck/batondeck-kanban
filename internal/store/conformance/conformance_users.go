package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

func testUser(id, email string, at int64) store.User {
	return store.User{ID: id, Email: email, DisplayName: "Name " + id, PasswordHash: "$argon2id$hash-" + id, CreatedAt: at}
}

// users judges the users table: create and read back by id and by email, the duplicate email,
// listing in creation order, disable (the first time stands) and the password change, and
// ErrNotFound for every missing id.
func users(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("UsersCreateAndReadBack", func(t *testing.T) {
		s := migrated(t, newStore)
		u := testUser("U-1", "alice@example.test", 1000)
		u.DisabledAt = 5 // ignored: a user is created enabled
		if err := s.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		u.DisabledAt = 0
		for name, get := range map[string]func() (store.User, error){
			"by id":    func() (store.User, error) { return s.GetUser(ctx, "U-1") },
			"by email": func() (store.User, error) { return s.GetUserByEmail(ctx, "alice@example.test") },
		} {
			got, err := get()
			if err != nil || got != u {
				t.Fatalf("%s: %+v %v, want %+v", name, got, err, u)
			}
		}
	})

	t.Run("UsersWorkspaceAdminRoundTrips", func(t *testing.T) {
		s := migrated(t, newStore)
		admin := testUser("U-1", "root@x.test", 1)
		admin.WorkspaceAdmin = true
		for _, u := range []store.User{admin, testUser("U-2", "alice@x.test", 2)} {
			if err := s.CreateUser(ctx, u); err != nil {
				t.Fatal(err)
			}
		}
		a, _ := s.GetUser(ctx, "U-1")
		b, _ := s.GetUserByEmail(ctx, "alice@x.test")
		all, _ := s.ListUsers(ctx)
		if !a.WorkspaceAdmin || b.WorkspaceAdmin || len(all) != 2 || !all[0].WorkspaceAdmin || all[1].WorkspaceAdmin {
			t.Fatalf("admin flags: %v %v %+v", a.WorkspaceAdmin, b.WorkspaceAdmin, all)
		}
	})

	t.Run("UsersDuplicateEmailIsErrEmailTaken", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.CreateUser(ctx, testUser("U-1", "alice@example.test", 1)); err != nil {
			t.Fatal(err)
		}
		err := s.CreateUser(ctx, testUser("U-2", "alice@example.test", 2))
		if !errors.Is(err, store.ErrEmailTaken) {
			t.Fatalf("a second alice: %v, want ErrEmailTaken", err)
		}
		// Positive control: another email is accepted.
		if err := s.CreateUser(ctx, testUser("U-2", "bob@example.test", 2)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("UsersListInCreationOrder", func(t *testing.T) {
		s := migrated(t, newStore)
		if got, err := s.ListUsers(ctx); err != nil || len(got) != 0 {
			t.Fatalf("empty store: %v %v", got, err)
		}
		for _, u := range []store.User{testUser("U-b", "b@x.test", 20), testUser("U-a", "a@x.test", 10), testUser("U-c", "c@x.test", 20)} {
			if err := s.CreateUser(ctx, u); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.ListUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, u := range got {
			ids = append(ids, u.ID)
		}
		if len(ids) != 3 || ids[0] != "U-a" || ids[1] != "U-b" || ids[2] != "U-c" {
			t.Fatalf("order %v, want [U-a U-b U-c]", ids)
		}
	})

	t.Run("UsersDisableKeepsTheFirstTime", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.CreateUser(ctx, testUser("U-1", "a@x.test", 1)); err != nil {
			t.Fatal(err)
		}
		if err := s.DisableUser(ctx, "U-1", 100); err != nil {
			t.Fatal(err)
		}
		if err := s.DisableUser(ctx, "U-1", 200); err != nil {
			t.Fatalf("disabling twice: %v", err)
		}
		if u, _ := s.GetUser(ctx, "U-1"); u.DisabledAt != 100 {
			t.Fatalf("disabled_at %d, want 100", u.DisabledAt)
		}
	})

	t.Run("UsersSetPassword", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.CreateUser(ctx, testUser("U-1", "a@x.test", 1)); err != nil {
			t.Fatal(err)
		}
		if err := s.SetUserPassword(ctx, "U-1", "$argon2id$new"); err != nil {
			t.Fatal(err)
		}
		if u, _ := s.GetUser(ctx, "U-1"); u.PasswordHash != "$argon2id$new" {
			t.Fatalf("hash %q", u.PasswordHash)
		}
	})

	t.Run("UsersMissingIsErrNotFound", func(t *testing.T) {
		s := migrated(t, newStore)
		if _, err := s.GetUser(ctx, "U-none"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetUser: %v", err)
		}
		if _, err := s.GetUserByEmail(ctx, "none@x.test"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetUserByEmail: %v", err)
		}
		if err := s.DisableUser(ctx, "U-none", 1); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("DisableUser: %v", err)
		}
		if err := s.SetUserPassword(ctx, "U-none", "h"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("SetUserPassword: %v", err)
		}
	})

	t.Run("UsersReadInsideAtomicallySeesItsOwnWrite", func(t *testing.T) {
		s := migrated(t, newStore)
		err := s.Atomically(ctx, func(tx store.Store) error {
			if err := tx.CreateUser(ctx, testUser("U-1", "a@x.test", 1)); err != nil {
				return err
			}
			_, err := tx.GetUserByEmail(ctx, "a@x.test")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		// And a rolled-back create leaves nothing.
		boom := errors.New("boom")
		_ = s.Atomically(ctx, func(tx store.Store) error {
			_ = tx.CreateUser(ctx, testUser("U-2", "b@x.test", 2))
			return boom
		})
		if _, err := s.GetUser(ctx, "U-2"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("after rollback: %v", err)
		}
	})
}
