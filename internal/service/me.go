package service

import (
	"context"
	"regexp"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// me is service.Me over store.Settings.
type me struct{ c *Core }

var _ Me = me{}

// Me is the caller's own settings service.
func (c *Core) Me() Me { return me{c} }

// Conductor's constants (src/domain/tokens.ts, src/util/tokens.ts) and set_my_settings's bounds.
const (
	defaultMaxTicketTokens = 50_000
	defaultTokenPolicy     = TokenPolicy("warn")
	charsPerToken          = 4
	minTicketTokens        = 100
	maxTicketTokens        = 1_000_000
	maxStarredAssets       = 200
)

// starredAsset is conductor's registry key shape: a typo'd star cannot put an arbitrary string on
// the identity row.
var starredAsset = regexp.MustCompile(`^(batondeck|org)/(skill|agent|command|prompt|hook|integration-template)/[A-Za-z0-9][\w.-]*$`)

// identityKind is the kind an identities row gets when set_my_settings creates it: every caller is a
// local account (a cookie session or that account's CLI token).
const identityKind = "user"

func (m me) GetMySettings(ctx context.Context, caller domain.Caller, _ GetMySettingsReq) (resp MySettings, err error) {
	defer m.c.recovered("get_my_settings", &err)
	if _, err := m.c.authorize(ctx, caller, "", "get_my_settings", domain.GrantSubject{}, false); err != nil {
		return resp, err
	}
	cur, err := m.load(ctx, caller.Identity)
	if err != nil {
		return resp, err
	}
	return mySettings(cur), nil
}

func (m me) SetMySettings(ctx context.Context, caller domain.Caller, req SetMySettingsReq) (resp MySettings, err error) {
	defer m.c.recovered("set_my_settings", &err)
	if _, err := m.c.authorize(ctx, caller, "", "set_my_settings", domain.GrantSubject{}, false); err != nil {
		return resp, err
	}
	if v := req.MaxTicketTokens; v.Set && !v.Null && (v.V < minTicketTokens || v.V > maxTicketTokens) {
		return resp, domain.Validation("maxTicketTokens is 100 to 1000000", map[string]any{"field": "maxTicketTokens", "min": minTicketTokens, "max": maxTicketTokens})
	}
	if v := req.TokenPolicy; v.Set && !v.Null && !slices.Contains(TokenPolicyValues, v.V) {
		return resp, domain.Validation("unknown tokenPolicy: "+string(v.V), map[string]any{"field": "tokenPolicy", "allowed": TokenPolicyValues})
	}
	if len(req.StarredAssets) > maxStarredAssets {
		return resp, domain.Validation("starredAssets holds at most 200 keys", map[string]any{"field": "starredAssets", "max": maxStarredAssets})
	}
	for _, k := range req.StarredAssets {
		if !starredAsset.MatchString(k) {
			return resp, domain.Validation("starredAssets: not a registry key (scope/type/name): "+k, map[string]any{"field": "starredAssets"})
		}
	}
	cur, err := m.load(ctx, caller.Identity)
	if err != nil {
		return resp, err
	}
	if v := req.MaxTicketTokens; v.Set {
		cur.MaxTicketTokens = 0
		if !v.Null {
			cur.MaxTicketTokens = v.V
		}
	}
	if v := req.TokenPolicy; v.Set {
		cur.TokenPolicy = ""
		if !v.Null {
			cur.TokenPolicy = string(v.V)
		}
	}
	if req.StarredAssets != nil {
		cur.StarredAssets = req.StarredAssets
	}
	if err := m.c.store.PutIdentitySettings(ctx, cur, identityKind, caller.Identity); err != nil {
		return resp, m.c.mapErr("set_my_settings", err)
	}
	return mySettings(cur), nil
}

// load is the caller's row, or an empty one when it has none yet.
func (m me) load(ctx context.Context, identity string) (store.IdentitySettings, error) {
	cur, err := m.c.store.GetIdentitySettings(ctx, identity)
	if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
		return store.IdentitySettings{IdentityID: identity, StarredAssets: []string{}}, nil
	}
	if err != nil {
		return cur, m.c.mapErr("my_settings", err)
	}
	return cur, nil
}

func mySettings(s store.IdentitySettings) MySettings {
	out := MySettings{
		Defaults:      MySettingsDefaults{CharsPerToken: charsPerToken, MaxTicketTokens: defaultMaxTicketTokens, TokenPolicy: defaultTokenPolicy},
		Estimated:     true,
		StarredAssets: s.StarredAssets,
	}
	if out.StarredAssets == nil {
		out.StarredAssets = []string{}
	}
	if s.MaxTicketTokens != 0 {
		v := s.MaxTicketTokens
		out.MaxTicketTokens = &v
	}
	if s.TokenPolicy != "" {
		v := TokenPolicy(s.TokenPolicy)
		out.TokenPolicy = &v
	}
	return out
}
