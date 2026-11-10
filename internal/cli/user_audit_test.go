package cli

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// auditRows is every row of st's audit log with this action, oldest first.
func auditRows(t *testing.T, st store.Audit, action string) []store.AuditRow {
	t.Helper()
	page, err := st.ListAuditLog(context.Background(), store.AuditFilter{Action: action}, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	rows := page.Rows
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

func wantAccountRow(t *testing.T, rows []store.AuditRow, actor, subject string, data map[string]any) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("%d rows, want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	var got map[string]any
	if err := json.Unmarshal(r.Data, &got); err != nil {
		t.Fatal(err)
	}
	if r.Actor != actor || r.Subject != subject || r.Result != store.AuditOK {
		t.Fatalf("the row %+v, want actor %q subject %q", r, actor, subject)
	}
	for k, v := range data {
		if got[k] != v {
			t.Fatalf("the row's data %v, want %s=%v", got, k, v)
		}
	}
}

// The host's user add, disable, enable and passwd each leave one audit row, actor host, as token
// create does; enabling a user who was not disabled changes nothing and records nothing. On each
// engine.
func TestUserCommandsAreAudited(t *testing.T) {
	for _, e := range []struct {
		name string
		db   func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store {
			st, err := store.OpenSQLite(migratedDB(t), "FULL")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			return st
		}},
		{"postgres", func(t *testing.T) store.Store {
			st, dsn := storetest.PostgresDSN(t)
			t.Setenv("BATONDECK_DATABASE_URL", dsn)
			return st
		}},
	} {
		t.Run(e.name, func(t *testing.T) {
			st := e.db(t)
			withStdin(t, "s3cret-s3cret\n")
			code, out, errs := run("user", "add", "--admin", "alice@example.test")
			if code != 0 {
				t.Fatalf("add: %d %q", code, errs)
			}
			subject := domain.CanonIdentity(strings.Fields(out)[2])
			wantAccountRow(t, auditRows(t, st, auth.AuditUserAdded), auth.HostActor, subject,
				map[string]any{"login": "alice@example.test", "admin": true, "via": "cli"})
			if code, _, errs := run("user", "enable", "alice@example.test"); code != 0 {
				t.Fatalf("enable of an enabled user: %d %q", code, errs)
			}
			if rows := auditRows(t, st, auth.AuditUserEnabled); len(rows) != 0 {
				t.Fatalf("enabling an enabled user recorded %+v", rows)
			}
			if code, _, errs := run("user", "disable", "alice@example.test"); code != 0 {
				t.Fatalf("disable: %d %q", code, errs)
			}
			wantAccountRow(t, auditRows(t, st, auth.AuditUserDisabled), auth.HostActor, subject, map[string]any{"login": "alice@example.test", "via": "cli"})
			if code, _, errs := run("user", "enable", "alice@example.test"); code != 0 {
				t.Fatalf("enable: %d %q", code, errs)
			}
			wantAccountRow(t, auditRows(t, st, auth.AuditUserEnabled), auth.HostActor, subject, map[string]any{"login": "alice@example.test", "via": "cli"})
			withStdin(t, "new-password-1\n")
			if code, _, errs := run("user", "passwd", "alice@example.test"); code != 0 {
				t.Fatalf("passwd: %d %q", code, errs)
			}
			wantAccountRow(t, auditRows(t, st, auth.AuditUserPasswordChanged), auth.HostActor, subject,
				map[string]any{"login": "alice@example.test", "via": "cli", "sessionsEnded": float64(0)})
		})
	}
}

// An account the workspace admin adds on Settings → Users (POST /api/users) is recorded on the
// composed app: one user.added row names the admin, the new identity and its login; so is the
// admin's own password change (POST /api/me/password). On each engine.
func TestServeAuditsAUserAddedOnSettings(t *testing.T) {
	for _, eng := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		t.Run(eng.name, func(t *testing.T) {
			st := eng.open(t)
			root, err := (&auth.Accounts{Store: st, Params: auth.Argon2Params{Time: 1, Memory: 64, Threads: 1}}).Add(context.Background(), "root", "", "pw-root", true)
			if err != nil {
				t.Fatal(err)
			}
			a, err := compose(st, "x", "", log.New(io.Discard, "", 0), nil, service.MediaOptions{}, core.Retention{})
			if err != nil {
				t.Fatal(err)
			}
			defer a.core.Close(context.Background())
			defer a.bus.Close()
			srv := httptest.NewServer(a.handler("x"))
			defer srv.Close()
			jar, _ := cookiejar.New(nil)
			browser := &http.Client{Jar: jar}
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/login", strings.NewReader(url.Values{"login": {"root"}, "password": {"pw-root"}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			resp, err := browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("sign-in: %d", resp.StatusCode)
			}
			csrf := ""
			for _, c := range resp.Cookies() {
				if c.Name == auth.CSRFCookie {
					csrf = c.Value
				}
			}
			req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/users", strings.NewReader(`{"login":"carol"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set(auth.CSRFHeader, csrf)
			resp, err = browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var added struct {
				User struct {
					ID string `json:"id"`
				} `json:"user"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&added)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || added.User.ID == "" {
				t.Fatalf("add carol: %d %+v", resp.StatusCode, added)
			}
			wantAccountRow(t, auditRows(t, st, auth.AuditUserAdded), root.ID, domain.CanonIdentity(added.User.ID),
				map[string]any{"login": "carol", "admin": false, "via": "settings"})

			// The admin's own password change on Settings is recorded as theirs.
			req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/me/password", strings.NewReader(`{"currentPassword":"pw-root","newPassword":"a-longer-root-password"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set(auth.CSRFHeader, csrf)
			resp, err = browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("password change: %d", resp.StatusCode)
			}
			wantAccountRow(t, auditRows(t, st, auth.AuditUserPasswordChanged), root.ID, domain.CanonIdentity(root.ID),
				map[string]any{"login": "root", "via": "settings", "sessionsEnded": float64(1)})
		})
	}
}
