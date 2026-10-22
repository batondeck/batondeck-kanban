package conformance

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// wantCode fails unless err is a BatonDeckError with code, and, for NOT_FOUND with entity given,
// names that entity: the error shape a caller branches on, the same on both engines.
func wantCode(t *testing.T, err error, code domain.Code, entity string) {
	t.Helper()
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
}

func newProject(t *testing.T, s store.Store, creator string) store.Project {
	t.Helper()
	p, err := s.CreateProject(context.Background(), store.CreateProjectInput{Name: "p-" + creator, CreatedBy: creator}, store.WriteOptions{Identity: creator})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// projects is the project and membership areas: create, read, list, settings, recursive delete,
// and members added, updated, listed and removed, each with its refusals.
func projects(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("CreateProjectMakesItsCreatorTheAdminWithDefaultSettings", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		got, err := s.GetProject(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != p.ID || got.Name != "p-alice" || got.CreatedBy != "alice" || got.CreatedAt == 0 {
			t.Fatalf("GetProject = %+v after CreateProject = %+v", got, p)
		}
		if !got.Settings.ReviewEnabled || got.Settings.RetryPolicy != nil {
			t.Fatalf("new project settings %+v, want reviewEnabled and the default retry policy", got.Settings)
		}
		if string(got.Settings.Other["quotaEnabled"]) != "false" || string(got.Settings.Other["txnCap"]) != "null" {
			t.Fatalf("new project settings %v, want quotaEnabled false and txnCap null", got.Settings.Other)
		}
		if _, ok := got.Settings.Other["transitionTable"]; ok {
			t.Fatal("a new project carries a transitionTable")
		}
		members, err := s.ListMembers(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(members) != 1 || members[0].IdentityID != "alice" || members[0].Role != domain.RoleAdmin {
			t.Fatalf("members %+v, want alice as the one admin", members)
		}
	})

	t.Run("CreateProjectRefusesAnEmptyNameOrCreator", func(t *testing.T) {
		s := migrated(t, newStore)
		_, err := s.CreateProject(ctx, store.CreateProjectInput{Name: " ", CreatedBy: "alice"}, store.WriteOptions{Identity: "alice"})
		wantCode(t, err, domain.CodeValidation, "")
		_, err = s.CreateProject(ctx, store.CreateProjectInput{Name: "p"}, store.WriteOptions{})
		wantCode(t, err, domain.CodeValidation, "")
	})

	t.Run("GetProjectOfAMissingIDIsNotFound", func(t *testing.T) {
		s := migrated(t, newStore)
		_, err := s.GetProject(ctx, "P-missing")
		wantCode(t, err, domain.CodeNotFound, "Project")
	})

	t.Run("ListProjectsIsTheIdentitysMembershipsOldestFirst", func(t *testing.T) {
		s := migrated(t, newStore)
		a := newProject(t, s, "alice")
		time.Sleep(2 * time.Millisecond)
		b := newProject(t, s, "bob")
		if _, err := s.AddMember(ctx, b.ID, "alice", domain.RoleWorker, "bob"); err != nil {
			t.Fatal(err)
		}
		got, err := s.ListProjects(ctx, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Project.ID != a.ID || got[0].Role != domain.RoleAdmin || got[1].Project.ID != b.ID || got[1].Role != domain.RoleWorker {
			t.Fatalf("alice's projects %+v, want %s as admin then %s as worker", got, a.ID, b.ID)
		}
		none, err := s.ListProjects(ctx, "carol")
		if err != nil || len(none) != 0 {
			t.Fatalf("carol's projects %+v, %v: want none", none, err)
		}
	})

	t.Run("UpdateSettingsSetsAndRemovesByName", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		got, err := s.UpdateSettings(ctx, p.ID, store.SettingsPatch{
			"reviewEnabled": json.RawMessage(`false`),
			"retryPolicy":   json.RawMessage(`{"baseDelayMs":1000,"maxDelayMs":5000,"jitter":0,"poisonAfterAttempts":3}`),
			"color":         json.RawMessage(`"#ff0000"`),
		})
		if err != nil {
			t.Fatal(err)
		}
		want := domain.RetryPolicy{BaseDelayMs: 1000, MaxDelayMs: 5000, PoisonAfterAttempts: 3}
		if got.Settings.ReviewEnabled || got.Settings.RetryPolicy == nil || *got.Settings.RetryPolicy != want || string(got.Settings.Other["color"]) != `"#ff0000"` {
			t.Fatalf("settings after the patch %+v", got.Settings)
		}
		read, err := s.GetProject(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if read.Settings.ReviewEnabled || read.Settings.RetryPolicy == nil || string(read.Settings.Other["color"]) != `"#ff0000"` {
			t.Fatalf("GetProject after the patch %+v", read.Settings)
		}
		got, err = s.UpdateSettings(ctx, p.ID, store.SettingsPatch{"color": json.RawMessage(`null`), "retryPolicy": json.RawMessage(`null`)})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.Settings.Other["color"]; ok || got.Settings.RetryPolicy != nil || string(got.Settings.Other["quotaEnabled"]) != "false" {
			t.Fatalf("settings after removing color and retryPolicy %+v", got.Settings)
		}
	})

	t.Run("UpdateSettingsRefusesATransitionTableAndChangesNothing", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		_, err := s.UpdateSettings(ctx, p.ID, store.SettingsPatch{
			"transitionTable": json.RawMessage(`{"BACKLOG":["DONE"]}`),
			"color":           json.RawMessage(`"#00ff00"`),
		})
		wantCode(t, err, domain.CodeValidation, "")
		read, err := s.GetProject(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := read.Settings.Other["color"]; ok {
			t.Fatal("a refused patch changed a setting")
		}
		if _, ok := read.Settings.Other["transitionTable"]; ok {
			t.Fatal("a refused patch stored a transitionTable")
		}
	})

	t.Run("UpdateSettingsRefusesMalformedColumnsAndAMissingProject", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		for _, patch := range []store.SettingsPatch{
			{"reviewEnabled": json.RawMessage(`"yes"`)},
			{"reviewEnabled": json.RawMessage(`null`)},
			{"retryPolicy": json.RawMessage(`{"baseDelayMs":"soon"}`)},
			{"retryPolicy": json.RawMessage(`{"unknown":1}`)},
		} {
			_, err := s.UpdateSettings(ctx, p.ID, patch)
			wantCode(t, err, domain.CodeValidation, "")
		}
		_, err := s.UpdateSettings(ctx, "P-missing", store.SettingsPatch{"color": json.RawMessage(`"#000"`)})
		wantCode(t, err, domain.CodeNotFound, "Project")
	})

	t.Run("DeleteProjectRecursiveRemovesWhatTheProjectOwnsAndNothingElse", func(t *testing.T) {
		s := migrated(t, newStore)
		gone := newProject(t, s, "alice")
		kept := newProject(t, s, "alice")
		opts := store.WriteOptions{Identity: "alice", IdempotencyKey: "board-1", IdempotencyTTL: time.Hour}
		if _, err := s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: gone.ID, Name: "b", CreatedBy: "alice"}, opts); err != nil {
			t.Fatal(err)
		}
		keptBoard, err := s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: kept.ID, Name: "b", CreatedBy: "alice"}, opts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddMember(ctx, gone.ID, "bob", domain.RoleWorker, "alice"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.NextTaskID(ctx, gone.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteProjectRecursive(ctx, gone.ID); err != nil {
			t.Fatal(err)
		}
		_, err = s.GetProject(ctx, gone.ID)
		wantCode(t, err, domain.CodeNotFound, "Project")
		_, err = s.ListBoards(ctx, gone.ID)
		wantCode(t, err, domain.CodeNotFound, "Project")
		_, err = s.GetMember(ctx, gone.ID, "bob")
		wantCode(t, err, domain.CodeNotFound, "Member")
		_, err = s.NextTaskID(ctx, gone.ID)
		wantCode(t, err, domain.CodeNotFound, "Project")
		// The idempotency key went with the project: the same key is not a replay of the deleted board.
		_, err = s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: gone.ID, Name: "b", CreatedBy: "alice"}, opts)
		wantCode(t, err, domain.CodeNotFound, "Project")
		boards, err := s.ListBoards(ctx, kept.ID)
		if err != nil || len(boards) != 1 || boards[0].ID != keptBoard.ID {
			t.Fatalf("the other project's boards %+v, %v: want %s alone", boards, err, keptBoard.ID)
		}
		replay, err := s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: kept.ID, Name: "b", CreatedBy: "alice"}, opts)
		if err != nil || replay.ID != keptBoard.ID {
			t.Fatalf("the other project's key replayed %+v, %v: want %s", replay, err, keptBoard.ID)
		}
	})

	t.Run("DeleteProjectRecursiveOfAMissingProjectIsNotFound", func(t *testing.T) {
		s := migrated(t, newStore)
		wantCode(t, s.DeleteProjectRecursive(ctx, "P-missing"), domain.CodeNotFound, "Project")
	})

	t.Run("AddMemberAddsAndThenUpdatesTheRole", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		first, err := s.AddMember(ctx, p.ID, "bob", domain.RoleWorker, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if first.Role != domain.RoleWorker || first.AddedBy != "alice" || first.ProjectID != p.ID {
			t.Fatalf("AddMember = %+v", first)
		}
		second, err := s.AddMember(ctx, p.ID, "bob", domain.RoleMaster, "carol")
		if err != nil {
			t.Fatal(err)
		}
		if second.Role != domain.RoleMaster || second.AddedBy != "alice" || second.AddedAt != first.AddedAt {
			t.Fatalf("a second AddMember = %+v, want the role changed and the first add kept", second)
		}
		members, err := s.ListMembers(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		bobs := slices.DeleteFunc(slices.Clone(members), func(m store.Member) bool { return m.IdentityID != "bob" })
		if len(members) != 2 || len(bobs) != 1 || bobs[0].Role != domain.RoleMaster {
			t.Fatalf("members %+v, want alice and one bob as master", members)
		}
		got, err := s.GetMember(ctx, p.ID, "bob")
		if err != nil || got.Role != domain.RoleMaster {
			t.Fatalf("GetMember = %+v, %v", got, err)
		}
	})

	t.Run("AddMemberRefusesAnUnknownRoleAndAMissingProject", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		_, err := s.AddMember(ctx, p.ID, "bob", "owner", "alice")
		wantCode(t, err, domain.CodeValidation, "")
		_, err = s.AddMember(ctx, "P-missing", "bob", domain.RoleWorker, "alice")
		wantCode(t, err, domain.CodeNotFound, "Project")
	})

	t.Run("RemoveMemberRemovesOnceThenIsNotFound", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		if _, err := s.AddMember(ctx, p.ID, "bob", domain.RoleWorker, "alice"); err != nil {
			t.Fatal(err)
		}
		if err := s.RemoveMember(ctx, p.ID, "bob"); err != nil {
			t.Fatal(err)
		}
		_, err := s.GetMember(ctx, p.ID, "bob")
		wantCode(t, err, domain.CodeNotFound, "Member")
		wantCode(t, s.RemoveMember(ctx, p.ID, "bob"), domain.CodeNotFound, "Member")
		wantCode(t, s.RemoveMember(ctx, "P-missing", "bob"), domain.CodeNotFound, "Project")
	})

	t.Run("MembershipIsKeyedByProject", func(t *testing.T) {
		s := migrated(t, newStore)
		a := newProject(t, s, "alice")
		b := newProject(t, s, "bob")
		_, err := s.GetMember(ctx, b.ID, "alice")
		wantCode(t, err, domain.CodeNotFound, "Member")
		if _, err := s.GetMember(ctx, a.ID, "alice"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ListMembersOfAMissingProjectIsNotFound", func(t *testing.T) {
		s := migrated(t, newStore)
		_, err := s.ListMembers(ctx, "P-missing")
		wantCode(t, err, domain.CodeNotFound, "Project")
	})
}
