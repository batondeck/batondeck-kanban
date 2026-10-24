package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The settings area of contract_settings.go, on both engines: one statement each way.

type settingsQueries interface {
	GetIdentitySettings(ctx context.Context, id string) (sqlitedb.GetIdentitySettingsRow, error)
	PutIdentitySettings(ctx context.Context, arg sqlitedb.PutIdentitySettingsParams) error
}

func (s *SQLite) GetIdentitySettings(ctx context.Context, identityID string) (IdentitySettings, error) {
	return getIdentitySettings(ctx, s, identityID)
}

func (s *Postgres) GetIdentitySettings(ctx context.Context, identityID string) (IdentitySettings, error) {
	return getIdentitySettings(ctx, s, identityID)
}

func (s *SQLite) PutIdentitySettings(ctx context.Context, in IdentitySettings, kind, display string) error {
	return putIdentitySettings(ctx, s, in, kind, display)
}

func (s *Postgres) PutIdentitySettings(ctx context.Context, in IdentitySettings, kind, display string) error {
	return putIdentitySettings(ctx, s, in, kind, display)
}

// TokenPolicies are the token_policy values; "" is unset.
var TokenPolicies = []string{"off", "warn", "enforce"}

// ValidIdentitySettings is the area's refusals, shared with memstore.
func ValidIdentitySettings(in IdentitySettings, kind, display string) error {
	switch {
	case in.IdentityID == "" || kind == "" || display == "":
		return domain.Validation("identity settings need an identity, a kind and a display", nil)
	case in.MaxTicketTokens < 0:
		return domain.Validation("maxTicketTokens cannot be negative", map[string]any{"field": "maxTicketTokens"})
	case in.TokenPolicy != "" && !slices.Contains(TokenPolicies, in.TokenPolicy):
		return domain.Validation("unknown token policy: "+in.TokenPolicy, map[string]any{"field": "tokenPolicy", "allowed": TokenPolicies})
	}
	return nil
}

func getIdentitySettings(ctx context.Context, e engine, identityID string) (IdentitySettings, error) {
	r, err := e.queries(false).GetIdentitySettings(ctx, identityID)
	if isNoRows(err) {
		return IdentitySettings{}, domain.NotFound("Identity", identityID)
	}
	if err != nil {
		return IdentitySettings{}, fmt.Errorf("store: identity settings: %w", err)
	}
	out := IdentitySettings{IdentityID: r.ID, MaxTicketTokens: r.MaxTicketTokens.Int64, TokenPolicy: r.TokenPolicy.String, StarredAssets: []string{}}
	if err := json.Unmarshal([]byte(r.StarredAssets), &out.StarredAssets); err != nil {
		return IdentitySettings{}, fmt.Errorf("store: identity settings: starred_assets: %w", err)
	}
	if out.StarredAssets == nil {
		out.StarredAssets = []string{}
	}
	return out, nil
}

func putIdentitySettings(ctx context.Context, e engine, in IdentitySettings, kind, display string) error {
	if err := ValidIdentitySettings(in, kind, display); err != nil {
		return err
	}
	stars := in.StarredAssets
	if stars == nil {
		stars = []string{}
	}
	b, err := json.Marshal(stars)
	if err != nil {
		return fmt.Errorf("store: identity settings: %w", err)
	}
	ceiling := sql.NullInt64{Int64: in.MaxTicketTokens, Valid: in.MaxTicketTokens != 0}
	err = e.queries(true).PutIdentitySettings(ctx, sqlitedb.PutIdentitySettingsParams{
		ID: in.IdentityID, Kind: kind, Display: display, MaxTicketTokens: ceiling, TokenPolicy: nullText(in.TokenPolicy), StarredAssets: string(b),
	})
	if err != nil {
		return fmt.Errorf("store: put identity settings: %w", err)
	}
	return nil
}

func (p pgQueries) GetIdentitySettings(ctx context.Context, id string) (sqlitedb.GetIdentitySettingsRow, error) {
	r, err := p.q.GetIdentitySettings(ctx, id)
	return sqlitedb.GetIdentitySettingsRow(r), err
}

func (p pgQueries) PutIdentitySettings(ctx context.Context, arg sqlitedb.PutIdentitySettingsParams) error {
	return p.q.PutIdentitySettings(ctx, pgdb.PutIdentitySettingsParams(arg))
}
