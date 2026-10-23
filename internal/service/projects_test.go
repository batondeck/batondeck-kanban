package service

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// addMember adds identity to the fixture's project with role, through the service, as alice.
func (f *fixture) addMember(t *testing.T, identity string, role domain.Role) domain.Caller {
	t.Helper()
	if _, err := f.c.Projects().AddMember(f.ctx, f.alice, AddMemberReq{ProjectID: f.project.ID, IdentityID: identity, Role: role}); err != nil {
		t.Fatal(err)
	}
	return domain.Caller{Identity: identity, TokenScope: identity}
}

// AC1: the same idempotencyKey twice is one project; another key is another.
func TestProjectsCreateReplaysOnIdempotencyKey(t *testing.T) {
	f := newFixture(t, Options{})
	carol := domain.Caller{Identity: "carol", TokenScope: "carol"}
	before := len(f.changes(t))
	first, err := f.c.Projects().Create(f.ctx, carol, CreateProjectReq{Name: "Q", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.c.Projects().Create(f.ctx, carol, CreateProjectReq{Name: "Q", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	mine, err := f.c.Projects().List(f.ctx, carol, ListProjectsReq{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("create_project k twice -> %s, %s; carol's projects %d; change rows +%d", first.ProjectID, second.ProjectID, len(mine.Projects), len(f.changes(t))-before)
	if first.ProjectID != second.ProjectID || len(mine.Projects) != 1 || len(f.changes(t))-before != 1 {
		t.Fatalf("the replay made a second project or row: %s vs %s, %d projects", first.ProjectID, second.ProjectID, len(mine.Projects))
	}
	if r := mine.Projects[0]; r.Role != domain.RoleAdmin || r.Owner == nil || *r.Owner != "carol" {
		t.Fatalf("the creator is not the admin owner: %+v", r)
	}
	other, err := f.c.Projects().Create(f.ctx, carol, CreateProjectReq{Name: "Q", IdempotencyKey: "k2"})
	if err != nil || other.ProjectID == first.ProjectID {
		t.Fatalf("another key replayed the first project: %s (err %v)", other.ProjectID, err)
	}
	// The change row of a create is scoped to the new project.
	ch := f.changes(t)
	if last := ch[len(ch)-1]; last.Scope != BroadcastScope(other.ProjectID) {
		t.Fatalf("create_project's change row is scoped %q", last.Scope)
	}
}

// AC1: transitionTable cannot reach the store. UpdateSettingsReq has no such field, so a strict
// decode (the transports' additionalProperties:false) refuses it, and the store refuses a patch
// naming it; the positive control updates real settings and writes the audit row.
func TestUpdateSettingsRefusesTransitionTable(t *testing.T) {
	f := newFixture(t, Options{})
	body := `{"projectId":"` + f.project.ID + `","transitionTable":{"BACKLOG":["DONE"]}}`
	d := json.NewDecoder(strings.NewReader(body))
	d.DisallowUnknownFields()
	var req UpdateSettingsReq
	derr := d.Decode(&req)
	_, serr := f.st.UpdateSettings(f.ctx, f.project.ID, store.SettingsPatch{"transitionTable": json.RawMessage(`{"BACKLOG":["DONE"]}`)})
	t.Logf("strict decode: %v; store patch: %v", derr, serr)
	if derr == nil || !strings.Contains(derr.Error(), "transitionTable") {
		t.Fatalf("UpdateSettingsReq accepted a transitionTable: %v", derr)
	}
	wantCode(t, serr, domain.CodeValidation)

	got, err := f.c.Projects().UpdateSettings(f.ctx, f.alice, UpdateSettingsReq{ProjectID: f.project.ID, MaxRunsPerTask: 7, ArtifactPolicy: "warn", Color: Value("#aabbcc")})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got.Settings)
	t.Logf("update_settings -> %s", raw)
	if got.Settings.MaxRunsPerTask != 7 || got.Settings.ArtifactPolicy != "warn" || got.Settings.Color != "#aabbcc" || bytes.Contains(raw, []byte("transitionTable")) {
		t.Fatalf("settings %s", raw)
	}
	page, _ := f.st.ListAuditLog(f.ctx, store.AuditFilter{Action: "settings.updated"}, "", 0)
	if len(page.Rows) != 1 || page.Rows[0].Subject != f.project.ID {
		t.Fatalf("audit rows %+v", page.Rows)
	}
}

func TestUpdateSettingsRefusals(t *testing.T) {
	f := newFixture(t, Options{})
	bob := f.addMember(t, "bob", domain.RoleMember)
	cases := []struct {
		name   string
		caller domain.Caller
		req    UpdateSettingsReq
		code   domain.Code
	}{
		{"a member is not admin", bob, UpdateSettingsReq{MaxRunsPerTask: 3}, domain.CodeForbidden},
		{"quota from a project admin", f.alice, UpdateSettingsReq{QuotaEnabled: Some(true)}, domain.CodeForbidden},
		{"quota from a workspace admin", domain.Caller{Identity: "alice", WorkspaceAdmin: true}, UpdateSettingsReq{TxnCap: Value(int64(5))}, domain.CodeValidation},
		{"maxRunsPerTask over 50", f.alice, UpdateSettingsReq{MaxRunsPerTask: 51}, domain.CodeValidation},
		{"maxTicketTokens under 100", f.alice, UpdateSettingsReq{MaxTicketTokens: 99}, domain.CodeValidation},
		{"an unknown policy", f.alice, UpdateSettingsReq{TokenPolicy: "loud"}, domain.CodeValidation},
		{"a bad colour", f.alice, UpdateSettingsReq{Color: Value("red")}, domain.CodeValidation},
		{"retry max below base", f.alice, UpdateSettingsReq{RetryPolicy: RetryPolicyPatch{BaseDelayMs: 10, MaxDelayMs: 5, PoisonAfterAttempts: 1}}, domain.CodeValidation},
		{"a negative weight", f.alice, UpdateSettingsReq{SelectionWeights: SelectionWeightsPatch{Aging: -1, Priority: 2}}, domain.CodeValidation},
	}
	for _, c := range cases {
		c.req.ProjectID = f.project.ID
		_, err := f.c.Projects().UpdateSettings(f.ctx, c.caller, c.req)
		t.Logf("%s: %v", c.name, err)
		wantCode(t, err, c.code)
	}
	// Positive control: a retry policy and weights that pass are stored.
	got, err := f.c.Projects().UpdateSettings(f.ctx, f.alice, UpdateSettingsReq{ProjectID: f.project.ID,
		RetryPolicy:      RetryPolicyPatch{BaseDelayMs: 10, MaxDelayMs: 50, Jitter: 0.5, PoisonAfterAttempts: 3},
		SelectionWeights: SelectionWeightsPatch{Priority: 1}})
	if err != nil || got.Settings.RetryPolicy.MaxDelayMs != 50 || got.Settings.SelectionWeights.Priority != 1 {
		t.Fatalf("settings %+v (err %v)", got.Settings, err)
	}
}

// AC1: delete_project is the project admin's (the posture's admin tier): a worker and a master are
// FORBIDDEN, the admin deletes the project and its boards.
func TestDeleteProjectTiers(t *testing.T) {
	f := newFixture(t, Options{})
	bob := f.addMember(t, "bob", domain.RoleWorker)
	dave := f.addMember(t, "dave", domain.RoleMaster)
	for _, c := range []domain.Caller{bob, dave} {
		_, err := f.c.Projects().Delete(f.ctx, c, DeleteProjectReq{ProjectID: f.project.ID})
		t.Logf("delete_project by %s: %v", c.Identity, err)
		wantCode(t, err, domain.CodeForbidden)
	}
	if _, err := f.st.GetProject(f.ctx, f.project.ID); err != nil {
		t.Fatalf("a refused delete removed the project: %v", err)
	}
	audit, _ := f.st.ListAuditLog(f.ctx, store.AuditFilter{}, "", 0)
	nAudit := len(audit.Rows)
	if _, err := f.c.Projects().Delete(f.ctx, f.alice, DeleteProjectReq{ProjectID: f.project.ID}); err != nil {
		t.Fatal(err)
	}
	_, perr := f.st.GetProject(f.ctx, f.project.ID)
	_, berr := f.st.ListBoards(f.ctx, f.project.ID)
	_, gerr := f.c.Boards().Get(f.ctx, f.alice, GetBoardReq{ProjectID: f.project.ID, BoardID: f.board.ID})
	t.Logf("after delete by alice: project %v; boards %v; get_board %v", perr, berr, gerr)
	wantCode(t, perr, domain.CodeNotFound)
	wantCode(t, berr, domain.CodeNotFound)
	wantCode(t, gerr, domain.CodeForbidden)
	audit, _ = f.st.ListAuditLog(f.ctx, store.AuditFilter{}, "", 0)
	if len(audit.Rows) != nAudit+1 || audit.Rows[0].Action != "project.deleted" || audit.Rows[0].Subject != f.project.ID {
		t.Fatalf("audit %+v", audit.Rows)
	}
}

// delete_project's conditional branch: a workspace admin needs no membership; a missing project is
// NOT_FOUND for it, never a silent ok.
func TestDeleteProjectByWorkspaceAdmin(t *testing.T) {
	f := newFixture(t, Options{})
	root := domain.Caller{Identity: "root", WorkspaceAdmin: true}
	_, err := f.c.Projects().Delete(f.ctx, root, DeleteProjectReq{ProjectID: "P-missing"})
	wantCode(t, err, domain.CodeNotFound)
	if _, err := f.c.Projects().Delete(f.ctx, root, DeleteProjectReq{ProjectID: f.project.ID}); err != nil {
		t.Fatal(err)
	}
	_, err = f.st.GetProject(f.ctx, f.project.ID)
	wantCode(t, err, domain.CodeNotFound)
}

// The R0.21 hook: a removed member is refused on its very next call, with the access TTL not
// elapsed (the clock does not move).
func TestRemoveMemberIsRefusedAtOnce(t *testing.T) {
	f := newFixture(t, Options{})
	bob := f.addMember(t, "bob", domain.RoleWorker)
	if _, err := f.c.Boards().List(f.ctx, bob, ListBoardsReq{ProjectID: f.project.ID}); err != nil {
		t.Fatal(err) // caches bob's membership
	}
	if _, err := f.c.Projects().RemoveMember(f.ctx, f.alice, RemoveMemberReq{ProjectID: f.project.ID, IdentityID: "bob"}); err != nil {
		t.Fatal(err)
	}
	_, err := f.c.Boards().List(f.ctx, bob, ListBoardsReq{ProjectID: f.project.ID})
	t.Logf("bob after remove_member, clock unmoved: %v", err)
	wantCode(t, err, domain.CodeForbidden)
}

// A role change holds at once both ways, and a non-member's cached refusal is dropped when it is
// added.
func TestAddMemberRoleChangeHoldsAtOnce(t *testing.T) {
	f := newFixture(t, Options{})
	erin := domain.Caller{Identity: "erin", TokenScope: "erin"}
	_, err := f.c.Boards().List(f.ctx, erin, ListBoardsReq{ProjectID: f.project.ID})
	wantCode(t, err, domain.CodeForbidden) // caches the non-membership
	f.addMember(t, "erin", domain.RoleWorker)
	if _, err := f.c.Boards().List(f.ctx, erin, ListBoardsReq{ProjectID: f.project.ID}); err != nil {
		t.Fatalf("erin, just added, is refused: %v", err)
	}
	if _, err := f.c.Boards().Create(f.ctx, erin, CreateBoardReq{ProjectID: f.project.ID, Name: "w"}); err != nil {
		t.Fatalf("a worker cannot create a board: %v", err)
	}
	f.addMember(t, "erin", domain.RoleViewer)
	_, err = f.c.Boards().Create(f.ctx, erin, CreateBoardReq{ProjectID: f.project.ID, Name: "v"})
	t.Logf("erin demoted to viewer, create_board: %v", err)
	wantCode(t, err, domain.CodeForbidden)
}

func TestLastAdminGuard(t *testing.T) {
	f := newFixture(t, Options{})
	_, rerr := f.c.Projects().RemoveMember(f.ctx, f.alice, RemoveMemberReq{ProjectID: f.project.ID, IdentityID: "alice"})
	_, derr := f.c.Projects().AddMember(f.ctx, f.alice, AddMemberReq{ProjectID: f.project.ID, IdentityID: "alice", Role: domain.RoleMember})
	t.Logf("remove the last admin: %v; demote it: %v", rerr, derr)
	wantCode(t, rerr, domain.CodeValidation)
	wantCode(t, derr, domain.CodeValidation)
	// Positive control: with a second admin, alice can step down.
	f.addMember(t, "bob", domain.RoleAdmin)
	got, err := f.c.Projects().AddMember(f.ctx, f.alice, AddMemberReq{ProjectID: f.project.ID, IdentityID: "alice", Role: domain.RoleMember})
	if err != nil || got.Members["alice"] != domain.RoleMember || got.Members["bob"] != domain.RoleAdmin {
		t.Fatalf("members %+v (err %v)", got.Members, err)
	}
	_, err = f.c.Projects().RemoveMember(f.ctx, f.alice, RemoveMemberReq{ProjectID: f.project.ID, IdentityID: "nobody"})
	wantCode(t, err, domain.CodeForbidden) // alice is no longer admin
	bob := domain.Caller{Identity: "bob"}
	_, err = f.c.Projects().RemoveMember(f.ctx, bob, RemoveMemberReq{ProjectID: f.project.ID, IdentityID: "nobody"})
	wantCode(t, err, domain.CodeNotFound)
}

// add_member keys the membership by the account id a login names, so the token that resolves to
// that account is the member; get_project labels it.
func TestAddMemberResolvesALogin(t *testing.T) {
	f := newFixture(t, Options{})
	if err := f.st.CreateUser(f.ctx, store.User{ID: "U-0a1b", Email: "bob@example.com", DisplayName: "Bob", PasswordHash: "x", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := f.c.Projects().AddMember(f.ctx, f.alice, AddMemberReq{ProjectID: f.project.ID, IdentityID: " Bob@Example.com ", Role: domain.RoleWorker})
	if err != nil {
		t.Fatal(err)
	}
	if got.Members["U-0a1b"] != domain.RoleWorker {
		t.Fatalf("members %+v", got.Members)
	}
	if _, err := f.c.Boards().List(f.ctx, domain.Caller{Identity: "U-0a1b"}, ListBoardsReq{ProjectID: f.project.ID}); err != nil {
		t.Fatalf("the account is not the member: %v", err)
	}
	p, err := f.c.Projects().Get(f.ctx, f.alice, GetProjectReq{ProjectID: f.project.ID})
	if err != nil || p.MemberDisplay["U-0a1b"] != "Bob" || p.Project.Members["alice"] != domain.RoleAdmin {
		t.Fatalf("get_project %+v (err %v)", p, err)
	}
}

func TestGetProjectSettingsShape(t *testing.T) {
	f := newFixture(t, Options{})
	got, err := f.c.Projects().Get(f.ctx, f.alice, GetProjectReq{ProjectID: f.project.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got.Project.Settings)
	t.Logf("a new project's settings: %s", raw)
	if !got.Project.Settings.ReviewEnabled || got.Project.Settings.QuotaEnabled || !bytes.Contains(raw, []byte(`"txnCap":null`)) {
		t.Fatalf("settings %s", raw)
	}
	_, err = f.c.Projects().Get(f.ctx, domain.Caller{Identity: "mallory"}, GetProjectReq{ProjectID: f.project.ID})
	wantCode(t, err, domain.CodeForbidden)
	_, err = f.c.Projects().List(f.ctx, f.alice, ListProjectsReq{All: true})
	wantCode(t, err, domain.CodeForbidden)
}
