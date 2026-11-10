package conformance

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() {
	register("artifacts", artifacts)
	register("comments", comments)
	register("followups", followUps)
}

func sp(s string) *string { return &s }

// artifacts is the artifact area: append, list oldest first with tombstones, retract once, the
// live-artifact set over a page of tasks, project isolation, the task's cascade, and the refusals.
func artifacts(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListArtifacts(ctx, "P-probe", "T-1")
		return err
	}
	area(t, "Artifacts", newStore, probe, func(t *testing.T) {
		add := func(t *testing.T, s store.Store, p, task string, kind store.ArtifactKind, url, ref *string) store.Artifact {
			t.Helper()
			a, err := s.AddArtifact(ctx, store.AddArtifactInput{ProjectID: p, TaskID: task, Kind: kind, URL: url, Ref: ref, AddedBy: "alice"})
			noErr(t, err)
			time.Sleep(2 * time.Millisecond)
			return a
		}
		retraction := store.ArtifactRetraction{By: "bob", ByAgent: sp("w-1"), At: 1700000000000, Reason: "wrong pr"}

		t.Run("AddReturnsTheStampedLiveEntry", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			a, err := s.AddArtifact(ctx, store.AddArtifactInput{ProjectID: p, TaskID: task, Kind: store.ArtifactPR,
				URL: sp("https://example.com/pr/1"), Repo: sp("org/repo"), Title: sp("the fix"), AddedBy: "alice", AddedByAgent: sp("w-1")})
			noErr(t, err)
			if a.ID == "" || a.TS == 0 || a.ProjectID != p || a.TaskID != task || a.Kind != store.ArtifactPR || a.Retraction != nil ||
				a.URL == nil || *a.URL != "https://example.com/pr/1" || a.Ref != nil || a.Repo == nil || *a.Repo != "org/repo" ||
				a.Title == nil || a.AddedBy != "alice" || a.AddedByAgent == nil || *a.AddedByAgent != "w-1" {
				t.Fatalf("AddArtifact = %+v", a)
			}
		})

		t.Run("ListIsOldestFirstWithTombstonesAndRetractIsOnce", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			first := add(t, s, p, task, store.ArtifactBranch, nil, sp("feat/x"))
			add(t, s, p, task, store.ArtifactPR, sp("https://example.com/pr/2"), nil)
			got, err := s.RetractArtifact(ctx, p, task, first.ID, retraction)
			noErr(t, err)
			if got.Retraction == nil || got.Retraction.By != "bob" || got.Retraction.At != retraction.At || got.Retraction.Reason != "wrong pr" ||
				got.Retraction.ByAgent == nil || *got.Retraction.ByAgent != "w-1" {
				t.Fatalf("RetractArtifact = %+v", got)
			}
			_, err = s.RetractArtifact(ctx, p, task, first.ID, retraction)
			wantErr(t, err, domain.CodeNotFound, "Artifact")
			list, err := s.ListArtifacts(ctx, p, task)
			noErr(t, err)
			if len(list) != 2 || list[0].ID != first.ID || list[0].Retraction == nil || list[1].Kind != store.ArtifactPR || list[1].Retraction != nil {
				t.Fatalf("ListArtifacts = %+v; want the tombstoned branch then the live pr", list)
			}
		})

		t.Run("LiveArtifactTasksCountsLiveEntriesOnly", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			tasks := seedTasks(t, sd, p, b, "live", "tombstoned", "none")
			add(t, s, p, tasks[0], store.ArtifactPR, sp("https://example.com/pr/3"), nil)
			gone := add(t, s, p, tasks[1], store.ArtifactPR, sp("https://example.com/pr/4"), nil)
			_, err := s.RetractArtifact(ctx, p, tasks[1], gone.ID, retraction)
			noErr(t, err)
			live, err := s.LiveArtifactTasks(ctx, p, tasks)
			noErr(t, err)
			if len(live) != 1 || !live[tasks[0]] {
				t.Fatalf("LiveArtifactTasks = %v; want only %s", live, tasks[0])
			}
			if none, err := s.LiveArtifactTasks(ctx, p, nil); err != nil || len(none) != 0 {
				t.Fatalf("no ids: %v, %v; want none", none, err)
			}
		})

		t.Run("ATaskIDIsScopedByItsProject", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			pt := seedTasks(t, sd, p, b, "p")[0]
			qt := seedTasks(t, sd, q, qb, "q")[0]
			add(t, s, p, pt, store.ArtifactPR, sp("https://example.com/pr/5"), nil)
			list, err := s.ListArtifacts(ctx, q, qt)
			noErr(t, err)
			live, err := s.LiveArtifactTasks(ctx, q, []string{qt})
			noErr(t, err)
			if len(list) != 0 || len(live) != 0 {
				t.Fatalf("%s/%s lists %v and live %v: another project's artifact leaked", q, qt, list, live)
			}
		})

		t.Run("TheTasksDeleteTakesItsArtifacts", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			add(t, s, p, task, store.ArtifactPR, sp("https://example.com/pr/6"), nil)
			noErr(t, s.DeleteTask(ctx, p, task, 1))
			live, err := s.LiveArtifactTasks(ctx, p, []string{task})
			noErr(t, err)
			if len(live) != 0 {
				t.Fatalf("after the task's delete LiveArtifactTasks = %v", live)
			}
		})

		t.Run("AddListAndRetractRefuseWhatIsMissing", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			_, err := s.AddArtifact(ctx, store.AddArtifactInput{ProjectID: p, TaskID: "T-404", Kind: store.ArtifactPR, URL: sp("https://x.y/1"), AddedBy: "alice"})
			wantErr(t, err, domain.CodeNotFound, "Task")
			_, err = s.ListArtifacts(ctx, p, "T-404")
			wantErr(t, err, domain.CodeNotFound, "Task")
			_, err = s.RetractArtifact(ctx, p, task, "AR-404", retraction)
			wantErr(t, err, domain.CodeNotFound, "Artifact")
		})

		t.Run("AddRefusesABadKindNoTargetOrNoAuthor", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			for _, in := range []store.AddArtifactInput{
				{ProjectID: p, TaskID: task, Kind: "tarball", URL: sp("https://x.y/1"), AddedBy: "alice"},
				{ProjectID: p, TaskID: task, Kind: store.ArtifactPR, AddedBy: "alice"},
				{ProjectID: p, TaskID: task, Kind: store.ArtifactPR, URL: sp(""), Ref: sp(""), AddedBy: "alice"},
				{ProjectID: p, TaskID: task, Kind: store.ArtifactPR, URL: sp("https://x.y/1")},
			} {
				_, err := s.AddArtifact(ctx, in)
				wantErr(t, err, domain.CodeValidation, "")
			}
			list, err := s.ListArtifacts(ctx, p, task)
			noErr(t, err)
			if len(list) != 0 {
				t.Fatalf("refused adds left %d artifacts", len(list))
			}
		})
	})
}

// comments is the comment area: append with mentions, page oldest first, project isolation, the
// task's cascade, and the refusals.
func comments(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListComments(ctx, "P-probe", "T-1", store.CommentsQuery{})
		return err
	}
	area(t, "Comments", newStore, probe, func(t *testing.T) {
		add := func(t *testing.T, s store.Store, p, task, body string) store.Comment {
			t.Helper()
			c, err := s.AddComment(ctx, store.AddCommentInput{ProjectID: p, TaskID: task, Author: "alice", Body: body})
			noErr(t, err)
			time.Sleep(2 * time.Millisecond)
			return c
		}
		bodies := func(cs []store.Comment) []string {
			out := make([]string, len(cs))
			for i, c := range cs {
				out[i] = c.Body
			}
			return out
		}

		t.Run("AddReturnsTheStampedComment", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			parent := add(t, s, p, task, "first")
			c, err := s.AddComment(ctx, store.AddCommentInput{ProjectID: p, TaskID: task, Author: "bob", Body: "@alice ok",
				ParentCommentID: &parent.ID, Mentions: []string{"alice"}})
			noErr(t, err)
			if c.ID == "" || c.TS == 0 || c.Author != "bob" || c.Body != "@alice ok" || c.ParentCommentID == nil || *c.ParentCommentID != parent.ID ||
				len(c.Mentions) != 1 || c.Mentions[0] != "alice" {
				t.Fatalf("AddComment = %+v", c)
			}
			if parent.ParentCommentID != nil || parent.Mentions == nil || len(parent.Mentions) != 0 {
				t.Fatalf("a top-level comment with no mentions = %+v; want a nil parent and [] mentions", parent)
			}
		})

		t.Run("ListIsOldestFirstAndPagesByCursor", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			for _, body := range []string{"one", "two", "three"} {
				add(t, s, p, task, body)
			}
			page, err := s.ListComments(ctx, p, task, store.CommentsQuery{Limit: 2})
			noErr(t, err)
			if got := bodies(page.Items); len(got) != 2 || got[0] != "one" || got[1] != "two" || page.NextCursor == "" {
				t.Fatalf("first page %v, cursor %q: want [one two] and a cursor", got, page.NextCursor)
			}
			page, err = s.ListComments(ctx, p, task, store.CommentsQuery{Limit: 2, Cursor: page.NextCursor})
			noErr(t, err)
			if got := bodies(page.Items); len(got) != 1 || got[0] != "three" || page.NextCursor != "" {
				t.Fatalf("second page %v, cursor %q: want [three] and no cursor", got, page.NextCursor)
			}
		})

		t.Run("ATaskIDIsScopedByItsProject", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			pt := seedTasks(t, sd, p, b, "p")[0]
			qt := seedTasks(t, sd, q, qb, "q")[0]
			add(t, s, p, pt, "p's comment")
			page, err := s.ListComments(ctx, q, qt, store.CommentsQuery{})
			noErr(t, err)
			if len(page.Items) != 0 {
				t.Fatalf("%s/%s lists %v: another project's comment leaked", q, qt, bodies(page.Items))
			}
		})

		t.Run("TheTasksDeleteTakesItsComments", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			add(t, s, p, task, "gone with it")
			noErr(t, s.DeleteTask(ctx, p, task, 1))
			_, err := s.ListComments(ctx, p, task, store.CommentsQuery{})
			wantErr(t, err, domain.CodeNotFound, "Task")
		})

		t.Run("AReplyNamesACommentOfTheSameTask", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			tasks := seedTasks(t, sd, p, b, "a", "b")
			elsewhere := add(t, s, p, tasks[1], "on b")
			for _, parent := range []string{"CM-missing", elsewhere.ID} {
				_, err := s.AddComment(ctx, store.AddCommentInput{ProjectID: p, TaskID: tasks[0], Author: "alice", Body: "reply", ParentCommentID: &parent})
				wantErr(t, err, domain.CodeNotFound, "Comment")
			}
			page, err := s.ListComments(ctx, p, tasks[0], store.CommentsQuery{})
			noErr(t, err)
			if len(page.Items) != 0 {
				t.Fatalf("a refused reply was written: %v", bodies(page.Items))
			}
			// A missing task is still NotFound Task, whatever the parent.
			parent := "CM-missing"
			_, err = s.AddComment(ctx, store.AddCommentInput{ProjectID: p, TaskID: "T-404", Author: "alice", Body: "x", ParentCommentID: &parent})
			wantErr(t, err, domain.CodeNotFound, "Task")
		})

		t.Run("AddAndListRefuseAMissingTask", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			_, err := s.AddComment(ctx, store.AddCommentInput{ProjectID: p, TaskID: "T-404", Author: "alice", Body: "x"})
			wantErr(t, err, domain.CodeNotFound, "Task")
			_, err = s.ListComments(ctx, p, "T-404", store.CommentsQuery{})
			wantErr(t, err, domain.CodeNotFound, "Task")
		})

		t.Run("AddRefusesAnEmptyBodyOrAuthorAndListAMalformedCursor", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			for _, in := range []store.AddCommentInput{
				{ProjectID: p, TaskID: task, Author: "alice", Body: ""},
				{ProjectID: p, TaskID: task, Author: "", Body: "x"},
			} {
				_, err := s.AddComment(ctx, in)
				wantErr(t, err, domain.CodeValidation, "")
			}
			_, err := s.ListComments(ctx, p, task, store.CommentsQuery{Cursor: "not a cursor!"})
			wantErr(t, err, domain.CodeValidation, "")
		})
	})
}

// followUps is the follow-up area: append open, get, ack, page oldest first (all or open only),
// project isolation, the task's cascade, and the refusals.
func followUps(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListFollowUps(ctx, "P-probe", "T-1", store.FollowUpsQuery{})
		return err
	}
	area(t, "FollowUps", newStore, probe, func(t *testing.T) {
		add := func(t *testing.T, s store.Store, p, task, body string) store.FollowUp {
			t.Helper()
			f, err := s.AddFollowUp(ctx, store.AddFollowUpInput{ProjectID: p, TaskID: task, Author: "alice", Body: body})
			noErr(t, err)
			time.Sleep(2 * time.Millisecond)
			return f
		}
		bodies := func(fs []store.FollowUp) []string {
			out := make([]string, len(fs))
			for i, f := range fs {
				out[i] = f.Body
			}
			return out
		}

		t.Run("AddReturnsTheStampedOpenFollowUpAndGetReadsIt", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			f, err := s.AddFollowUp(ctx, store.AddFollowUpInput{ProjectID: p, TaskID: task, Author: "alice", Body: "fix the test",
				Target: sp("w-7"), TargetIdentity: sp("bob"), Reopened: true})
			noErr(t, err)
			if f.ID == "" || f.TS == 0 || f.Status != store.FollowUpOpen || !f.Reopened || f.Target == nil || *f.Target != "w-7" ||
				f.TargetIdentity == nil || *f.TargetIdentity != "bob" || f.Author != "alice" || f.Body != "fix the test" {
				t.Fatalf("AddFollowUp = %+v", f)
			}
			got, err := s.GetFollowUp(ctx, p, task, f.ID)
			noErr(t, err)
			if !reflect.DeepEqual(got, f) {
				t.Fatalf("GetFollowUp = %+v, want %+v", got, f)
			}
			bare := add(t, s, p, task, "untargeted")
			if bare.Target != nil || bare.TargetIdentity != nil || bare.Reopened {
				t.Fatalf("an untargeted follow-up = %+v", bare)
			}
		})

		t.Run("AckSetsAckedAndOpenOnlyLeavesItOut", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			first := add(t, s, p, task, "one")
			add(t, s, p, task, "two")
			acked, err := s.AckFollowUp(ctx, p, task, first.ID)
			noErr(t, err)
			if acked.Status != store.FollowUpAcked {
				t.Fatalf("AckFollowUp = %+v", acked)
			}
			if again, err := s.AckFollowUp(ctx, p, task, first.ID); err != nil || again.Status != store.FollowUpAcked {
				t.Fatalf("a second ack: %+v, %v; want acked and no error", again, err)
			}
			all, err := s.ListFollowUps(ctx, p, task, store.FollowUpsQuery{})
			noErr(t, err)
			open, err := s.ListFollowUps(ctx, p, task, store.FollowUpsQuery{OpenOnly: true})
			noErr(t, err)
			if got := bodies(all.Items); len(got) != 2 || got[0] != "one" || all.Items[0].Status != store.FollowUpAcked {
				t.Fatalf("all %v", all.Items)
			}
			if got := bodies(open.Items); len(got) != 1 || got[0] != "two" {
				t.Fatalf("open only %v, want [two]", got)
			}
		})

		t.Run("ListIsOldestFirstAndPagesByCursor", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			for _, body := range []string{"one", "two", "three"} {
				add(t, s, p, task, body)
			}
			page, err := s.ListFollowUps(ctx, p, task, store.FollowUpsQuery{Limit: 2})
			noErr(t, err)
			if got := bodies(page.Items); len(got) != 2 || got[0] != "one" || got[1] != "two" || page.NextCursor == "" {
				t.Fatalf("first page %v, cursor %q", got, page.NextCursor)
			}
			page, err = s.ListFollowUps(ctx, p, task, store.FollowUpsQuery{Limit: 2, Cursor: page.NextCursor})
			noErr(t, err)
			if got := bodies(page.Items); len(got) != 1 || got[0] != "three" || page.NextCursor != "" {
				t.Fatalf("second page %v, cursor %q", got, page.NextCursor)
			}
		})

		t.Run("ATaskIDIsScopedByItsProject", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			pt := seedTasks(t, sd, p, b, "p")[0]
			qt := seedTasks(t, sd, q, qb, "q")[0]
			f := add(t, s, p, pt, "p's directive")
			page, err := s.ListFollowUps(ctx, q, qt, store.FollowUpsQuery{})
			noErr(t, err)
			if len(page.Items) != 0 {
				t.Fatalf("%s/%s lists %v: another project's follow-up leaked", q, qt, bodies(page.Items))
			}
			_, err = s.GetFollowUp(ctx, q, qt, f.ID)
			wantErr(t, err, domain.CodeNotFound, "FollowUp")
			_, err = s.AckFollowUp(ctx, q, qt, f.ID)
			wantErr(t, err, domain.CodeNotFound, "FollowUp")
		})

		t.Run("TheTasksDeleteTakesItsFollowUps", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			f := add(t, s, p, task, "gone with it")
			noErr(t, s.DeleteTask(ctx, p, task, 1))
			_, err := s.GetFollowUp(ctx, p, task, f.ID)
			wantErr(t, err, domain.CodeNotFound, "FollowUp")
		})

		t.Run("AddListGetAndAckRefuseWhatIsMissing", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			_, err := s.AddFollowUp(ctx, store.AddFollowUpInput{ProjectID: p, TaskID: "T-404", Author: "alice", Body: "x"})
			wantErr(t, err, domain.CodeNotFound, "Task")
			_, err = s.ListFollowUps(ctx, p, "T-404", store.FollowUpsQuery{})
			wantErr(t, err, domain.CodeNotFound, "Task")
			_, err = s.GetFollowUp(ctx, p, task, "FU-404")
			wantErr(t, err, domain.CodeNotFound, "FollowUp")
			_, err = s.AckFollowUp(ctx, p, task, "FU-404")
			wantErr(t, err, domain.CodeNotFound, "FollowUp")
		})

		t.Run("AddRefusesAnEmptyBodyOrAuthorAndListAMalformedCursor", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			for _, in := range []store.AddFollowUpInput{
				{ProjectID: p, TaskID: task, Author: "alice", Body: ""},
				{ProjectID: p, TaskID: task, Author: "", Body: "x"},
			} {
				_, err := s.AddFollowUp(ctx, in)
				wantErr(t, err, domain.CodeValidation, "")
			}
			_, err := s.ListFollowUps(ctx, p, task, store.FollowUpsQuery{Cursor: "not a cursor!"})
			wantErr(t, err, domain.CodeValidation, "")
		})
	})
}
