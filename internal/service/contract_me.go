package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Me is the caller's own preferences: get_my_settings and set_my_settings over the caller's
// identities row (store.Settings). Neither takes an identity, so neither can name another's row.
// The ticket-token ceiling and rung are stored and returned; nothing in this binary enforces them
// yet (conductor's ticket-token governance is not ported). Not tenant-scoped, grant-exempt.
// Event, audit and doorbell: none.
type Me interface {
	// GetMySettings is get_my_settings: the caller's settings, null where unset, and the defaults.
	GetMySettings(ctx context.Context, caller domain.Caller, req GetMySettingsReq) (MySettings, error)
	// SetMySettings is set_my_settings: each field sent replaces the stored one (null clears it);
	// a field not sent keeps its value. maxTicketTokens is 100 to 1,000,000; starredAssets is at
	// most 200 registry keys (scope/type/name). Anything else is VALIDATION.
	SetMySettings(ctx context.Context, caller domain.Caller, req SetMySettingsReq) (MySettings, error)
}

// TokenPolicy is the ticket-token enforcement rung.
type TokenPolicy string

// TokenPolicyValues are TokenPolicy's values, in the schema's order.
var TokenPolicyValues = []TokenPolicy{"enforce", "off", "warn"}

// GetMySettingsReq is get_my_settings's input: nothing.
type GetMySettingsReq struct{}

// SetMySettingsReq is set_my_settings's input. StarredAssets nil is "not sent"; [] clears the list.
type SetMySettingsReq struct {
	MaxTicketTokens Nullable[int64]       `json:"maxTicketTokens,omitzero"`
	StarredAssets   []string              `json:"starredAssets,omitzero"`
	TokenPolicy     Nullable[TokenPolicy] `json:"tokenPolicy,omitzero"`
}

// MySettings is get_my_settings's and set_my_settings's output. TokenPolicy is always Set: Null
// where unset (a Nullable, not a pointer, so its schema admits null beside the enum).
type MySettings struct {
	Defaults        MySettingsDefaults    `json:"defaults"`
	Estimated       True                  `json:"estimated"`
	MaxTicketTokens *int64                `json:"maxTicketTokens"`
	StarredAssets   []string              `json:"starredAssets"`
	TokenPolicy     Nullable[TokenPolicy] `json:"tokenPolicy"`
}

// MySettingsDefaults is what applies where a setting is unset.
type MySettingsDefaults struct {
	CharsPerToken   float64     `json:"charsPerToken"`
	MaxTicketTokens int64       `json:"maxTicketTokens"`
	TokenPolicy     TokenPolicy `json:"tokenPolicy"`
}
