package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// meRoutes are the Settings page's own-account routes (app.ts:2696-2733): the settings over
// get_my_settings and set_my_settings, the CLI tokens over the TokenService (ADR-0004: these
// routes replace the dropped create/list/revoke_cli_token tools), and the user's own password
// change (auth.Service.PasswordChangeHandler). A token is the caller's own: no route takes an
// identity. No marketplace-key routes (the plan dropped them).
func meRoutes(o *Options) []route {
	m := o.Services.Me
	return []route{
		{Method: "POST", Path: "/api/me/password", h: o.Auth.PasswordChangeHandler()},
		{Method: "GET", Path: "/api/me/settings", h: call(o, m.GetMySettings, in{})},
		{Method: "PATCH", Path: "/api/me/settings", h: call(o, m.SetMySettings, in{})},
		{Method: "GET", Path: "/api/me/cli-tokens", h: tokenRoute(o, listCLITokens)},
		{Method: "POST", Path: "/api/me/cli-tokens", h: tokenRoute(o, createCLIToken)},
		{Method: "POST", Path: "/api/me/cli-tokens/{id}/revoke", h: tokenRoute(o, revokeCLIToken)},
	}
}

// cliTokenVia is the audit rows' via for a token minted or revoked on the Settings page.
const cliTokenVia = "settings"

// maxLiveCLITokens is the live tokens an identity may hold before the page refuses another mint.
const maxLiveCLITokens = 10

// cliTokenRow is one token as the page lists it (web/src/api/me.ts CliTokenRow): metadata only;
// the secret is not on this shape. It has no last use: none is recorded, and a field that was
// always null read as "never used".
type cliTokenRow struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	CreatedAt string  `json:"createdAt"`
	ExpiresAt string  `json:"expiresAt"`
	RevokedAt *string `json:"revokedAt"`
	Status    string  `json:"status"`
}

func tokenRow(i auth.TokenInfo) cliTokenRow {
	r := cliTokenRow{ID: i.ID, Label: i.Label, CreatedAt: i.CreatedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt: i.ExpiresAt.UTC().Format(time.RFC3339Nano), Status: i.Status}
	if !i.RevokedAt.IsZero() {
		at := i.RevokedAt.UTC().Format(time.RFC3339Nano)
		r.RevokedAt = &at
	}
	return r
}

// tokenRoute runs h for the caller; with no TokenService it answers INTERNAL.
func tokenRoute(o *Options, h func(*Options, domain.Caller, http.ResponseWriter, *http.Request) error) http.Handler {
	logger := o.logger()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := auth.CallerFrom(r.Context())
		if !ok {
			auth.WriteError(w, http.StatusUnauthorized, domain.CodeUnauthenticated, "unauthenticated")
			return
		}
		if o.Tokens == nil {
			writeServiceError(w, errors.New("api: Options.Tokens is nil"), logger)
			return
		}
		if err := h(o, c, w, r); err != nil {
			writeServiceError(w, err, logger)
		}
	})
}

func listCLITokens(o *Options, c domain.Caller, w http.ResponseWriter, r *http.Request) error {
	infos, err := o.Tokens.List(r.Context(), c.Identity)
	if err != nil {
		return err
	}
	out := make([]cliTokenRow, 0, len(infos))
	for _, i := range infos {
		out = append(out, tokenRow(i))
	}
	writeJSON(w, http.StatusOK, struct {
		Tokens []cliTokenRow `json:"tokens"`
	}{out})
	return nil
}

// createCLIToken mints a token acting for the caller. The body is {label, ttlSeconds?}; any other
// key is VALIDATION. The answer carries the plaintext once: it is stored only as its hash.
func createCLIToken(o *Options, c domain.Caller, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Label      string `json:"label"`
		TTLSeconds int64  `json:"ttlSeconds"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return domain.Validation("the body is {label, ttlSeconds?}: "+err.Error(), nil)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return domain.Validation("the body holds more than one JSON value", nil)
	}
	if in.TTLSeconds < 0 {
		return domain.Validation("ttlSeconds cannot be negative", map[string]any{"field": "ttlSeconds"})
	}
	// Conductor's blast-radius cap (CLI_TOKEN_MAX_PER_IDENTITY, me.ts:247-252): a leaked token cannot
	// mint an unbounded fan of tokens that outlive it. The host's `token create` is not capped.
	infos, err := o.Tokens.List(r.Context(), c.Identity)
	if err != nil {
		return err
	}
	live := 0
	for _, i := range infos {
		if i.Status == "live" {
			live++
		}
	}
	if live >= maxLiveCLITokens {
		return domain.Validation(fmt.Sprintf("You already hold %d live CLI tokens (max %d). Revoke one first.", live, maxLiveCLITokens),
			map[string]any{"live": live, "max": maxLiveCLITokens})
	}
	plain, info, err := o.Tokens.Mint(r.Context(), c.Identity, cliTokenVia, c.Identity, login(o, r, c.Identity), in.Label, time.Duration(in.TTLSeconds)*time.Second)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct {
		cliTokenRow
		Token     string `json:"token"`
		ShownOnce bool   `json:"shownOnce"`
	}{tokenRow(info), plain, true})
	return nil
}

// revokeCLIToken revokes one of the caller's tokens; another identity's is NOT_FOUND.
func revokeCLIToken(o *Options, c domain.Caller, w http.ResponseWriter, r *http.Request) error {
	info, err := o.Tokens.RevokeOwned(r.Context(), c.Identity, cliTokenVia, c.Identity, r.PathValue("id"))
	if err != nil {
		return err
	}
	row := tokenRow(info)
	writeJSON(w, http.StatusOK, struct {
		Revoked bool         `json:"revoked"`
		Token   *cliTokenRow `json:"token"`
	}{true, &row})
	return nil
}

// login is the caller's account login, the token's subject as `token create` records it; "" when
// the account list is not wired or does not hold the caller.
func login(o *Options, r *http.Request, identity string) string {
	if o.Users == nil {
		return ""
	}
	us, err := o.Users.List(r.Context())
	if err != nil {
		return ""
	}
	for _, u := range us {
		if u.ID == identity {
			return u.Email
		}
	}
	return ""
}
