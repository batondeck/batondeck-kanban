package conformance

import (
	"context"
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("profiles", profiles) }

// profiles is the capability registry and skill stats: an upsert keeps the first creator and
// creation time and replaces the rest (an omitted field resets), the stats roll up one sample per
// capability, the refusals, and both leaving with their project.
func profiles(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListSkillStats(ctx, "P-probe", "a")
		return err
	}
	area(t, "AgentProfiles", newStore, probe, func(t *testing.T) {
		ptr := func(s string) *string { return &s }

		t.Run("UpsertKeepsTheCreatorAndReplacesTheRest", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			four := int64(4)
			first, err := s.UpsertAgentProfile(ctx, store.UpsertAgentProfileInput{ProjectID: p, AgentKey: "builder", Capabilities: []string{"go", "sql"},
				Tags: []string{"backend"}, MaxConcurrency: &four, CostClass: ptr("premium"), Models: []string{"opus"}, MaxEffort: ptr("high"),
				CreatedBy: "U-alice", Now: 1000})
			noErr(t, err)
			if first.CreatedAt != 1000 || first.UpdatedAt != 1000 || *first.MaxConcurrency != 4 || *first.CostClass != "premium" ||
				!slices.Equal(first.Capabilities, []string{"go", "sql"}) || !slices.Equal(first.Models, []string{"opus"}) || *first.MaxEffort != "high" {
				t.Fatalf("first upsert %+v does not read back as written", first)
			}
			second, err := s.UpsertAgentProfile(ctx, store.UpsertAgentProfileInput{ProjectID: p, AgentKey: "builder", Capabilities: []string{"ts"},
				CreatedBy: "U-bob", Now: 2000})
			noErr(t, err)
			got, err := s.GetAgentProfile(ctx, p, "builder")
			noErr(t, err)
			for _, g := range []store.AgentProfile{second, got} {
				if g.CreatedBy != "U-alice" || g.CreatedAt != 1000 || g.UpdatedAt != 2000 || g.MaxConcurrency != nil || g.CostClass != nil ||
					g.MaxEffort != nil || !slices.Equal(g.Capabilities, []string{"ts"}) || g.Tags == nil || len(g.Tags) != 0 || g.Models == nil {
					t.Fatalf("after re-register %+v, want alice's creation kept and every other field replaced", g)
				}
			}
			_, err = s.GetAgentProfile(ctx, p, "nobody")
			wantErr(t, err, domain.CodeNotFound, "AgentProfile")
		})

		t.Run("StatsRollUpPerCapability", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			noErr(t, s.RecordSkillOutcome(ctx, store.SkillOutcome{ProjectID: p, AgentKey: "a", Capabilities: []string{"sql", "go"}, Completed: true, ElapsedMs: 100, Now: 10}))
			noErr(t, s.RecordSkillOutcome(ctx, store.SkillOutcome{ProjectID: p, AgentKey: "a", Capabilities: []string{"go"}, ElapsedMs: -5, Now: 20}))
			noErr(t, s.RecordSkillOutcome(ctx, store.SkillOutcome{ProjectID: p, AgentKey: "b", Capabilities: []string{"go"}, Completed: true, ElapsedMs: 7, Now: 30}))
			noErr(t, s.RecordSkillOutcome(ctx, store.SkillOutcome{ProjectID: p, AgentKey: "a", Now: 40}))
			got, err := s.ListSkillStats(ctx, p, "a")
			noErr(t, err)
			want := []store.SkillStat{
				{ProjectID: p, AgentKey: "a", Capability: "go", Completed: 1, Blocked: 1, TotalMs: 100, Samples: 2, UpdatedAt: 20},
				{ProjectID: p, AgentKey: "a", Capability: "sql", Completed: 1, TotalMs: 100, Samples: 1, UpdatedAt: 10},
			}
			if !slices.Equal(got, want) {
				t.Fatalf("stats = %+v, want %+v", got, want)
			}
			none, err := s.ListSkillStats(ctx, p, "nobody")
			noErr(t, err)
			if none == nil || len(none) != 0 {
				t.Fatalf("no stats = %#v, want []", none)
			}
		})

		t.Run("Refusals", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			ok := store.UpsertAgentProfileInput{ProjectID: p, AgentKey: "a", CreatedBy: "U-a", Now: 1}
			for name, mut := range map[string]func(in *store.UpsertAgentProfileInput){
				"key":     func(in *store.UpsertAgentProfileInput) { in.AgentKey = "" },
				"creator": func(in *store.UpsertAgentProfileInput) { in.CreatedBy = "" },
				"now":     func(in *store.UpsertAgentProfileInput) { in.Now = 0 },
				"cost":    func(in *store.UpsertAgentProfileInput) { in.CostClass = ptr("free") },
				"effort":  func(in *store.UpsertAgentProfileInput) { in.MaxEffort = ptr("max") },
			} {
				in := ok
				mut(&in)
				_, err := s.UpsertAgentProfile(ctx, in)
				if e, okk := domain.AsBatonDeckError(err); !okk || e.Code != domain.CodeValidation {
					t.Fatalf("%s: got %v, want VALIDATION", name, err)
				}
			}
			in := ok
			in.ProjectID = "P-missing"
			_, err := s.UpsertAgentProfile(ctx, in)
			wantErr(t, err, domain.CodeNotFound, "Project")
		})

		t.Run("BothLeaveWithTheirProject", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			_, err := s.UpsertAgentProfile(ctx, store.UpsertAgentProfileInput{ProjectID: p, AgentKey: "a", CreatedBy: "U-a", Now: 1})
			noErr(t, err)
			noErr(t, s.RecordSkillOutcome(ctx, store.SkillOutcome{ProjectID: p, AgentKey: "a", Capabilities: []string{"go"}, Completed: true, Now: 1}))
			noErr(t, s.DeleteProjectRecursive(ctx, p))
			_, err = s.GetAgentProfile(ctx, p, "a")
			wantErr(t, err, domain.CodeNotFound, "AgentProfile")
			stats, err := s.ListSkillStats(ctx, p, "a")
			noErr(t, err)
			if len(stats) != 0 {
				t.Fatalf("stats survived their project: %+v", stats)
			}
		})
	})
}
