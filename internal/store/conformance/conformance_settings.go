package conformance

import (
	"context"
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("settings", settings) }

// settings is the per-identity settings area: a missing row, the upsert that creates and then
// replaces the three settings, unset as zero values, and the refusals.
func settings(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.GetIdentitySettings(ctx, "probe")
		if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
			return nil
		}
		return err
	}
	area(t, "Settings", newStore, probe, func(t *testing.T) {
		t.Run("MissingRowIsNotFound", func(t *testing.T) {
			s := migrated(t, newStore)
			_, err := s.GetIdentitySettings(ctx, "U-nobody")
			wantErr(t, err, domain.CodeNotFound, "Identity")
		})

		t.Run("PutCreatesThenReplaces", func(t *testing.T) {
			s := migrated(t, newStore)
			in := store.IdentitySettings{IdentityID: "U-a", MaxTicketTokens: 5000, TokenPolicy: "enforce", StarredAssets: []string{"batondeck/skill/x"}}
			noErr(t, s.PutIdentitySettings(ctx, in, "user", "U-a"))
			got, err := s.GetIdentitySettings(ctx, "U-a")
			noErr(t, err)
			if got.IdentityID != "U-a" || got.MaxTicketTokens != 5000 || got.TokenPolicy != "enforce" || !slices.Equal(got.StarredAssets, in.StarredAssets) {
				t.Fatalf("after the first put: %+v", got)
			}
			noErr(t, s.PutIdentitySettings(ctx, store.IdentitySettings{IdentityID: "U-a"}, "user", "U-a"))
			got, err = s.GetIdentitySettings(ctx, "U-a")
			noErr(t, err)
			if got.MaxTicketTokens != 0 || got.TokenPolicy != "" || got.StarredAssets == nil || len(got.StarredAssets) != 0 {
				t.Fatalf("after clearing every setting: %+v, want unset and starredAssets []", got)
			}
			// Another identity's row is its own.
			noErr(t, s.PutIdentitySettings(ctx, store.IdentitySettings{IdentityID: "U-b", TokenPolicy: "off"}, "user", "U-b"))
			if a, _ := s.GetIdentitySettings(ctx, "U-a"); a.TokenPolicy != "" {
				t.Fatalf("U-b's put changed U-a: %+v", a)
			}
		})

		t.Run("Refusals", func(t *testing.T) {
			s := migrated(t, newStore)
			for name, c := range map[string]struct {
				in            store.IdentitySettings
				kind, display string
			}{
				"empty identity": {store.IdentitySettings{}, "user", "d"},
				"empty kind":     {store.IdentitySettings{IdentityID: "U-a"}, "", "d"},
				"empty display":  {store.IdentitySettings{IdentityID: "U-a"}, "user", ""},
				"negative max":   {store.IdentitySettings{IdentityID: "U-a", MaxTicketTokens: -1}, "user", "d"},
				"unknown policy": {store.IdentitySettings{IdentityID: "U-a", TokenPolicy: "strict"}, "user", "d"},
			} {
				t.Run(name, func(t *testing.T) {
					wantErr(t, s.PutIdentitySettings(ctx, c.in, c.kind, c.display), domain.CodeValidation, "")
				})
			}
			_, err := s.GetIdentitySettings(ctx, "U-a")
			wantErr(t, err, domain.CodeNotFound, "Identity")
		})
	})
}
