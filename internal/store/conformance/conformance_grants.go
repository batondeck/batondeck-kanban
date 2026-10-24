package conformance

import (
	"context"
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("grants", grants) }

// grants is the capability grants area: a set reads back exactly (absent apart from empty), a second
// set replaces the first, a delete reports whether there was one, a non-member has none, the
// membership list carries each grant or nil, and a grant goes with its member and its project.
func grants(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListMemberGrants(ctx, "probe")
		return err
	}
	area(t, "Grants", newStore, probe, func(t *testing.T) {
		member := func(t *testing.T, s store.Store, p, id string, r domain.Role) {
			t.Helper()
			_, err := s.AddMember(ctx, p, id, r, "seeder")
			noErr(t, err)
		}

		t.Run("SetReadsBackAndReplaces", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			member(t, s, p, "U-w", domain.RoleWorker)
			want := domain.CapabilityGrant{Tools: []string{"claim_task"}, Deny: []string{}, Statuses: []domain.Status{domain.Todo},
				MemoryScopes: []domain.MemoryScope{domain.MemoryScopeTask}, MaxPriority: domain.PriorityHigh}
			got, err := s.SetGrant(ctx, store.SetGrantInput{ProjectID: p, IdentityID: "U-w", Grant: want, UpdatedBy: "U-a", Now: 1000})
			noErr(t, err)
			read, err := s.GetGrant(ctx, p, "U-w")
			noErr(t, err)
			for _, g := range []store.Grant{got, read} {
				if g.ProjectID != p || g.IdentityID != "U-w" || g.UpdatedBy != "U-a" || g.UpdatedAt != 1000 ||
					!slices.Equal(g.Grant.Tools, want.Tools) || g.Grant.Deny == nil || len(g.Grant.Deny) != 0 || g.Grant.Labels != nil ||
					g.Grant.Boards != nil || g.Grant.MaxPriority != domain.PriorityHigh || !slices.Equal(g.Grant.Statuses, want.Statuses) ||
					!slices.Equal(g.Grant.MemoryScopes, want.MemoryScopes) {
					t.Fatalf("grant %+v does not read back as written (an empty deny is present, nil labels absent)", g)
				}
			}
			_, err = s.SetGrant(ctx, store.SetGrantInput{ProjectID: p, IdentityID: "U-w", Grant: domain.CapabilityGrant{Labels: []string{"ui"}}, UpdatedBy: "U-b", Now: 2000})
			noErr(t, err)
			read, err = s.GetGrant(ctx, p, "U-w")
			noErr(t, err)
			if read.UpdatedBy != "U-b" || read.UpdatedAt != 2000 || read.Grant.Tools != nil || !slices.Equal(read.Grant.Labels, []string{"ui"}) {
				t.Fatalf("after the second set %+v, want it replaced whole", read)
			}
		})

		t.Run("Refusals", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			_, err := s.SetGrant(ctx, store.SetGrantInput{ProjectID: p, IdentityID: "U-nobody", UpdatedBy: "U-a", Now: 1})
			wantErr(t, err, domain.CodeNotFound, "Member")
			_, err = s.GetGrant(ctx, p, "U-nobody")
			wantErr(t, err, domain.CodeNotFound, "Grant")
			member(t, s, p, "U-w", domain.RoleWorker)
			_, err = s.GetGrant(ctx, p, "U-w")
			wantErr(t, err, domain.CodeNotFound, "Grant")
			_, err = s.SetGrant(ctx, store.SetGrantInput{ProjectID: p, IdentityID: "U-w", Now: 1})
			wantErr(t, err, domain.CodeValidation, "")
			_, err = s.SetGrant(ctx, store.SetGrantInput{ProjectID: p, IdentityID: "U-w", UpdatedBy: "U-a"})
			wantErr(t, err, domain.CodeValidation, "")
		})

		t.Run("DeleteReportsWhetherOneWasHeld", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			member(t, s, p, "U-w", domain.RoleWorker)
			_, err := s.SetGrant(ctx, store.SetGrantInput{ProjectID: p, IdentityID: "U-w", Grant: domain.WorkerDefaultGrant(), UpdatedBy: "U-a", Now: 1})
			noErr(t, err)
			first, err := s.DeleteGrant(ctx, p, "U-w")
			noErr(t, err)
			second, err := s.DeleteGrant(ctx, p, "U-w")
			noErr(t, err)
			if !first || second {
				t.Fatalf("delete reported %v then %v, want true then false", first, second)
			}
			_, err = s.GetGrant(ctx, p, "U-w")
			wantErr(t, err, domain.CodeNotFound, "Grant")
		})

		t.Run("ListMemberGrantsCarriesEachMembership", func(t *testing.T) {
			s, sd, p, _ := seeded(t, newStore)
			q, _, err := sd.seedProject(ctx)
			noErr(t, err)
			r, _, err := sd.seedProject(ctx)
			noErr(t, err)
			member(t, s, p, "U-w", domain.RoleWorker)
			member(t, s, q, "U-w", domain.RoleMaster)
			member(t, s, r, "U-other", domain.RoleWorker)
			_, err = s.SetGrant(ctx, store.SetGrantInput{ProjectID: p, IdentityID: "U-w", Grant: domain.CapabilityGrant{Tools: []string{"claim_task"}}, UpdatedBy: "U-a", Now: 1})
			noErr(t, err)
			_, err = s.SetGrant(ctx, store.SetGrantInput{ProjectID: r, IdentityID: "U-other", Grant: domain.CapabilityGrant{Deny: []string{"x"}}, UpdatedBy: "U-a", Now: 1})
			noErr(t, err)
			got, err := s.ListMemberGrants(ctx, "U-w")
			noErr(t, err)
			ids := []string{p, q}
			slices.Sort(ids)
			if len(got) != 2 || got[0].ProjectID != ids[0] || got[1].ProjectID != ids[1] {
				t.Fatalf("U-w's memberships %+v, want %v by project id", got, ids)
			}
			for _, m := range got {
				switch m.ProjectID {
				case p:
					if m.Role != domain.RoleWorker || m.Grant == nil || !slices.Equal(m.Grant.Tools, []string{"claim_task"}) {
						t.Fatalf("membership of %s %+v, want worker with its grant", p, m)
					}
				case q:
					if m.Role != domain.RoleMaster || m.Grant != nil {
						t.Fatalf("membership of %s %+v, want master with no grant", q, m)
					}
				}
			}
			none, err := s.ListMemberGrants(ctx, "U-nobody")
			noErr(t, err)
			if none == nil || len(none) != 0 {
				t.Fatalf("a non-member's list %v, want []", none)
			}
		})

		t.Run("GoesWithTheMemberAndTheProject", func(t *testing.T) {
			s, sd, p, _ := seeded(t, newStore)
			member(t, s, p, "U-w", domain.RoleWorker)
			set := func(project string) {
				_, err := s.SetGrant(ctx, store.SetGrantInput{ProjectID: project, IdentityID: "U-w", Grant: domain.CapabilityGrant{Tools: []string{"claim_task"}}, UpdatedBy: "U-a", Now: 1})
				noErr(t, err)
			}
			set(p)
			noErr(t, s.RemoveMember(ctx, p, "U-w"))
			member(t, s, p, "U-w", domain.RoleWorker)
			_, err := s.GetGrant(ctx, p, "U-w")
			wantErr(t, err, domain.CodeNotFound, "Grant")

			q, _, err := sd.seedProject(ctx)
			noErr(t, err)
			member(t, s, q, "U-w", domain.RoleWorker)
			set(q)
			noErr(t, s.DeleteProjectRecursive(ctx, q))
			got, err := s.ListMemberGrants(ctx, "U-w")
			noErr(t, err)
			if len(got) != 1 || got[0].ProjectID != p || got[0].Grant != nil {
				t.Fatalf("after the member's removal and the project's deletion %+v, want only %s with no grant", got, p)
			}
		})
	})
}
