package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func (f *fixture) member(t *testing.T, identity string, role domain.Role) domain.Caller {
	t.Helper()
	if _, err := f.st.AddMember(f.ctx, f.project.ID, identity, role, "alice"); err != nil {
		t.Fatal(err)
	}
	return domain.Caller{Identity: identity, TokenScope: identity}
}

func (f *fixture) authorize(c domain.Caller, tool string) error {
	_, err := f.c.authorize(f.ctx, c, f.project.ID, tool, domain.GrantSubject{}, false)
	return err
}

// The tier ladder: each role passes its own tier and is refused the next one up.
func TestAuthorizeTiers(t *testing.T) {
	f := newFixture(t, Options{})
	for _, c := range []struct {
		role       domain.Role
		pass, deny string
	}{
		{domain.RoleViewer, "get_task", "claim_task"},
		{domain.RoleWorker, "claim_task", "create_task"},
		{domain.RoleMaster, "create_task", "remove_column"},
		{domain.RoleMember, "remove_column", "add_member"},
		{domain.RoleAdmin, "add_member", ""},
	} {
		caller := f.member(t, "u-"+string(c.role), c.role)
		if err := f.authorize(caller, c.pass); err != nil {
			t.Fatalf("%s refused %s: %v", c.role, c.pass, err)
		}
		if c.deny != "" {
			e := wantCode(t, f.authorize(caller, c.deny), domain.CodeForbidden)
			if e.Details["role"] != c.role {
				t.Fatalf("%s on %s: details %v", c.role, c.deny, e.Details)
			}
		}
	}
}

// A non-member, a missing project and an unclassified tool are FORBIDDEN; a project id is required.
func TestAuthorizeRefusals(t *testing.T) {
	f := newFixture(t, Options{})
	stranger := domain.Caller{Identity: "mallory"}
	wantCode(t, f.authorize(stranger, "get_task"), domain.CodeForbidden)
	_, err := f.c.authorize(f.ctx, f.alice, "P-nope", "get_task", domain.GrantSubject{}, false)
	wantCode(t, err, domain.CodeForbidden)
	wantCode(t, f.authorize(f.alice, "no_such_tool"), domain.CodeForbidden)
	_, err = f.c.authorize(f.ctx, f.alice, "", "get_task", domain.GrantSubject{}, false)
	wantCode(t, err, domain.CodeValidation)
	// Positive control: alice, the admin member, passes the same calls on her project.
	if err := f.authorize(f.alice, "get_task"); err != nil {
		t.Fatal(err)
	}
}

// Cross-project: membership of one project reaches nothing in another.
func TestAuthorizeCrossProjectIsForbidden(t *testing.T) {
	f := newFixture(t, Options{})
	other, err := f.st.CreateProject(f.ctx, store.CreateProjectInput{Name: "Q", CreatedBy: "bob"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.c.authorize(f.ctx, f.alice, other.ID, "get_task", domain.GrantSubject{}, false)
	wantCode(t, err, domain.CodeForbidden)
	_, err = f.c.authorize(f.ctx, domain.Caller{Identity: "bob"}, other.ID, "get_task", domain.GrantSubject{}, false)
	if err != nil {
		t.Fatalf("bob on his own project: %v", err)
	}
}

// AdminOnly "only" needs the workspace-admin flag; a tool that is not tenant-scoped needs no membership.
func TestAuthorizeAdminOnlyAndUnscoped(t *testing.T) {
	f := newFixture(t, Options{})
	wantCode(t, f.authorize(f.alice, "list_audit_log"), domain.CodeForbidden)
	admin := f.alice
	admin.WorkspaceAdmin = true
	if err := f.authorize(admin, "list_audit_log"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.authorize(f.ctx, domain.Caller{Identity: "nobody"}, "", "create_project", domain.GrantSubject{}, false); err != nil {
		t.Fatalf("create_project needs no membership: %v", err)
	}
}

// A token role caps the member's role; an unknown one caps it to nothing.
func TestAuthorizeTokenRoleCaps(t *testing.T) {
	f := newFixture(t, Options{})
	capped := f.alice
	capped.Role = string(domain.RoleWorker)
	if err := f.authorize(capped, "claim_task"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.authorize(capped, "create_task"), domain.CodeForbidden)
	capped.Role = "superuser"
	wantCode(t, f.authorize(capped, "get_task"), domain.CodeForbidden)
}

// AC4: a member removed (or demoted) after a successful call keeps passing while the entry is
// cached and fails once the lowered TTL (2 s) has passed; InvalidateAccess refuses at once.
func TestAuthorizeCacheTTL(t *testing.T) {
	f := newFixture(t, Options{AccessTTL: 2 * time.Second})
	bob := f.member(t, "bob", domain.RoleMaster)
	if err := f.authorize(bob, "create_task"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RemoveMember(f.ctx, f.project.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	f.clock.advance(1999 * time.Millisecond)
	if err := f.authorize(bob, "create_task"); err != nil {
		t.Fatalf("within the TTL the cached membership should stand: %v", err)
	}
	f.clock.advance(time.Millisecond)
	e := wantCode(t, f.authorize(bob, "create_task"), domain.CodeForbidden)
	t.Logf("removed member at +2.000s: %v", e)

	carol := f.member(t, "carol", domain.RoleMaster)
	if err := f.authorize(carol, "create_task"); err != nil {
		t.Fatal(err)
	}
	f.member(t, "carol", domain.RoleViewer)
	f.clock.advance(2 * time.Second)
	e = wantCode(t, f.authorize(carol, "create_task"), domain.CodeForbidden)
	t.Logf("demoted member at +2.000s: %v", e)

	dan := f.member(t, "dan", domain.RoleMaster)
	if err := f.authorize(dan, "create_task"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RemoveMember(f.ctx, f.project.ID, "dan"); err != nil {
		t.Fatal(err)
	}
	f.c.InvalidateAccess(f.project.ID, "dan")
	e = wantCode(t, f.authorize(dan, "create_task"), domain.CodeForbidden)
	t.Logf("removed member after InvalidateAccess, no clock step: %v", e)
}

// A non-member is cached too, and a membership added later is seen once invalidated.
func TestAuthorizeInvalidateAdmitsANewMember(t *testing.T) {
	f := newFixture(t, Options{})
	erin := domain.Caller{Identity: "erin"}
	wantCode(t, f.authorize(erin, "get_task"), domain.CodeForbidden)
	f.member(t, "erin", domain.RoleViewer)
	wantCode(t, f.authorize(erin, "get_task"), domain.CodeForbidden)
	f.c.InvalidateAccess(f.project.ID, "")
	if err := f.authorize(erin, "get_task"); err != nil {
		t.Fatalf("after the project's invalidation: %v", err)
	}
}

// The cache is what it claims: the second authorize within the TTL reads no membership.
func TestAuthorizeCachesTheLookup(t *testing.T) {
	calls := 0
	f := newFixture(t, Options{Grants: func(context.Context, string, string) (domain.CapabilityGrant, bool, error) {
		calls++
		return domain.CapabilityGrant{}, false, nil
	}})
	for range 3 {
		if err := f.authorize(f.alice, "create_task"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("three calls within the TTL read the grant %d times, want 1", calls)
	}
}

// A capability grant narrows the role: a tool-list grant refuses an unlisted governed tool with
// requiredCapability, and never narrows an exempt read.
func TestAuthorizeGrantRefusesAndExemptsReads(t *testing.T) {
	grant := domain.WorkerDefaultGrant()
	f := newFixture(t, Options{Grants: func(_ context.Context, _, identity string) (domain.CapabilityGrant, bool, error) {
		return grant, identity == "wally", nil
	}})
	wally := f.member(t, "wally", domain.RoleWorker)
	e := wantCode(t, f.authorize(wally, "fail_task"), domain.CodeForbidden)
	if e.Details["requiredCapability"] != "tool:fail_task" {
		t.Fatalf("details %v", e.Details)
	}
	if err := f.authorize(wally, "claim_task"); err != nil {
		t.Fatalf("a granted tool: %v", err)
	}
	if err := f.authorize(wally, "get_task"); err != nil {
		t.Fatalf("an exempt read: %v", err)
	}
	// Positive control: a worker with no grant may fail_task (the role permits it).
	if err := f.authorize(f.member(t, "wendy", domain.RoleWorker), "fail_task"); err != nil {
		t.Fatalf("the role alone: %v", err)
	}
}

// A grant on task attributes is finished inside mutate against the task it loads: a label grant
// refuses a task without the label, writes nothing, and admits one with it.
func TestAuthorizeTaskGrantIsCheckedInMutate(t *testing.T) {
	f := newFixture(t, Options{Grants: func(_ context.Context, _, identity string) (domain.CapabilityGrant, bool, error) {
		return domain.CapabilityGrant{Labels: []string{"frontend"}}, identity == "mona", nil
	}})
	mona := f.member(t, "mona", domain.RoleMaster)
	plain := f.task(t, domain.Done, nil)
	labelled := f.task(t, domain.Done, nil)
	if _, err := f.st.UpdateTask(f.ctx, f.project.ID, labelled.ID, labelled.Version, store.TaskPatch{Labels: &[]string{"frontend"}, UpdatedBy: "alice", UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	try := func(taskID string) error {
		a, err := f.c.authorize(f.ctx, mona, f.project.ID, "update_task", domain.GrantSubject{}, true)
		if err != nil {
			return err
		}
		_, err = mutate(f.ctx, f.c, mona, mutation{Tool: "update_task", ProjectID: f.project.ID, TaskID: taskID, Access: a}, func(t *txn) (int, error) {
			t.Event(store.EventInput{Type: domain.EventTaskUpdated})
			return 0, nil
		})
		return err
	}
	before := f.count(t)
	e := wantCode(t, try(plain.ID), domain.CodeForbidden)
	if e.Details["requiredCapability"] != "label:frontend" || f.count(t) != before {
		t.Fatalf("details %v; rows %+v→%+v", e.Details, before, f.count(t))
	}
	if err := try(labelled.ID); err != nil {
		t.Fatalf("the labelled task: %v", err)
	}
}

// P-438: a grant stored before grant_capabilities checked names, holding an unknown one in tools and
// in deny, still decides by the names it holds: the unknown names restrict nothing on their own, the
// listed real tool is admitted and an unlisted governed tool is refused with requiredCapability.
func TestAuthorizeStoredGrantWithUnknownNamesDecides(t *testing.T) {
	legacy := domain.CapabilityGrant{Tools: []string{"claim_taks", "claim_task"}, Deny: []string{"add_coment"}}
	f := newFixture(t, Options{Grants: func(_ context.Context, _, identity string) (domain.CapabilityGrant, bool, error) {
		return legacy, identity == "grace", nil
	}})
	grace := f.member(t, "grace", domain.RoleWorker)
	if err := f.authorize(grace, "claim_task"); err != nil {
		t.Fatalf("claim_task, listed: %v", err)
	}
	e := wantCode(t, f.authorize(grace, "write_memory"), domain.CodeForbidden)
	if e.Details["requiredCapability"] != "tool:write_memory" {
		t.Fatalf("write_memory, unlisted: details %v", e.Details)
	}
	if err := f.authorize(grace, "get_task"); err != nil {
		t.Fatalf("an exempt read: %v", err)
	}
}

// A grant reader's failure is INTERNAL, logged, and not cached.
func TestAuthorizeGrantReaderErrorIsInternal(t *testing.T) {
	fail := true
	f := newFixture(t, Options{Grants: func(context.Context, string, string) (domain.CapabilityGrant, bool, error) {
		if fail {
			return domain.CapabilityGrant{}, false, errors.New("grant store down")
		}
		return domain.CapabilityGrant{}, false, nil
	}})
	wantCode(t, f.authorize(f.alice, "create_task"), domain.CodeInternal)
	fail = false
	if err := f.authorize(f.alice, "create_task"); err != nil {
		t.Fatalf("the failure was cached: %v", err)
	}
}

func (c *Core) accessEntries() int {
	c.access.mu.Lock()
	defer c.access.mu.Unlock()
	return len(c.access.entries)
}

// A projectId no project can have (longer than any id the server makes) is VALIDATION before any
// lookup, and nothing is cached for it: the caller chooses it, so caching it would let any token
// grow the cache by the request's size.
func TestAuthorizeRefusesAnOverlongProjectID(t *testing.T) {
	f := newFixture(t, Options{})
	stranger := domain.Caller{Identity: "mallory"}
	long := "P-" + strings.Repeat("a", 200_000)
	_, err := f.c.authorize(f.ctx, stranger, long, "get_project", domain.GrantSubject{}, false)
	wantCode(t, err, domain.CodeValidation)
	_, err = f.c.authorize(f.ctx, f.alice, long, "get_project", domain.GrantSubject{}, false)
	wantCode(t, err, domain.CodeValidation)
	// recall_memory is not tenant-scoped: it checks a named project's membership itself.
	_, err = f.c.Context().RecallMemory(f.ctx, stranger, RecallMemoryReq{ProjectID: long})
	wantCode(t, err, domain.CodeValidation)
	if n := f.c.accessEntries(); n != 0 {
		t.Fatalf("%d access entries cached for an overlong projectId, want 0", n)
	}
	// Control: the longest id allowed is still looked up (and refused as no membership).
	_, err = f.c.authorize(f.ctx, stranger, "P-"+strings.Repeat("a", maxIDLen-2), "get_project", domain.GrantSubject{}, false)
	wantCode(t, err, domain.CodeForbidden)
	if err := f.authorize(f.alice, "get_project"); err != nil {
		t.Fatal(err)
	}
}

// The access cache stays bounded while its entries are all live: a caller naming a new project id
// on every call cannot grow it past accessCacheSweep within the TTL.
func TestAccessCacheIsBounded(t *testing.T) {
	f := newFixture(t, Options{})
	stranger := domain.Caller{Identity: "mallory"}
	for i := range 3 * accessCacheSweep {
		_, err := f.c.authorize(f.ctx, stranger, fmt.Sprintf("P-%012x", i), "get_project", domain.GrantSubject{}, false)
		wantCode(t, err, domain.CodeForbidden)
	}
	if n := f.c.accessEntries(); n > accessCacheSweep {
		t.Fatalf("%d access entries within the TTL, want at most %d", n, accessCacheSweep)
	}
	// Control: the cache still serves (alice's entry is read once, then cached).
	if err := f.authorize(f.alice, "get_project"); err != nil {
		t.Fatal(err)
	}
}
