package store

import "context"

// IdentitySettings is one identity's own preferences (get_my_settings / set_my_settings): the
// identities row's max_ticket_tokens, token_policy and starred_assets. MaxTicketTokens 0 and
// TokenPolicy "" are unset (NULL); StarredAssets is never nil on a read.
type IdentitySettings struct {
	IdentityID      string
	MaxTicketTokens int64
	TokenPolicy     string
	StarredAssets   []string
}

// Settings is the per-identity settings area, over the identities table. Errors: an empty identity,
// kind or display, a negative MaxTicketTokens and a TokenPolicy other than "", off, warn and enforce
// are domain.Validation; a missing row is domain.NotFound("Identity", id).
type Settings interface {
	// GetIdentitySettings is identityID's settings.
	GetIdentitySettings(ctx context.Context, identityID string) (IdentitySettings, error)
	// PutIdentitySettings writes the three settings, all of them: an existing row keeps its kind,
	// display and capabilities; a missing one is created with kind and display.
	PutIdentitySettings(ctx context.Context, s IdentitySettings, kind, display string) error
}

var (
	_ Settings = (*SQLite)(nil)
	_ Settings = (*Postgres)(nil)
)
