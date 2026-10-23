package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// userRoutes are Settings' users page (R1.15): the workspace admin lists the local accounts and
// adds one. Nothing else is here by design: disable, password reset and role changes are the host's
// `batondeck-kanban user disable|passwd`. Both answer FORBIDDEN to anyone but a workspace admin.
func userRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: "/api/users", h: adminOnly(o, listUsers)},
		{Method: "POST", Path: "/api/users", h: adminOnly(o, addUser)},
	}
}

// userResp is one account as the page shows it: never the password hash.
type userResp struct {
	ID          string `json:"id"`
	Login       string `json:"login"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"` // "admin" or "member"
	Disabled    bool   `json:"disabled"`
	CreatedAt   int64  `json:"createdAt"`
}

// userOf takes the row's fields, not the row: api does not import internal/store (layering rule 6).
func userOf(id, login, displayName string, admin bool, disabledAt, createdAt int64) userResp {
	role := "member"
	if admin {
		role = "admin"
	}
	return userResp{ID: id, Login: login, DisplayName: displayName, Role: role, Disabled: disabledAt != 0, CreatedAt: createdAt}
}

// adminOnly runs h for a workspace admin and answers FORBIDDEN to anyone else, before the body is
// read. The flag is the caller's, from the account cache (60 s, as every grant).
func adminOnly(o *Options, h func(*Options, http.ResponseWriter, *http.Request) error) http.Handler {
	logger := o.logger()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := auth.CallerFrom(r.Context())
		if !ok {
			auth.WriteError(w, http.StatusUnauthorized, domain.CodeUnauthenticated, "unauthenticated")
			return
		}
		if !c.WorkspaceAdmin {
			writeServiceError(w, domain.NewError(domain.CodeForbidden, "only the workspace admin manages users", nil), logger)
			return
		}
		if o.Users == nil {
			writeServiceError(w, errors.New("api: Options.Users is nil"), logger)
			return
		}
		if err := h(o, w, r); err != nil {
			writeServiceError(w, err, logger)
		}
	})
}

func listUsers(o *Options, w http.ResponseWriter, r *http.Request) error {
	us, err := o.Users.List(r.Context())
	if err != nil {
		return err
	}
	out := make([]userResp, 0, len(us))
	for _, u := range us {
		out = append(out, userOf(u.ID, u.Email, u.DisplayName, u.WorkspaceAdmin, u.DisabledAt, u.CreatedAt))
	}
	writeJSON(w, http.StatusOK, struct {
		Users []userResp `json:"users"`
	}{out})
	return nil
}

// addUser creates a member with a generated password, which this answer carries once: it is stored
// only as its argon2id hash. The body is {login, displayName?}; any other key is VALIDATION, so a
// role cannot be asked for here.
func addUser(o *Options, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Login       string `json:"login"`
		DisplayName string `json:"displayName"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return domain.Validation("the body is {login, displayName?}: "+err.Error(), nil)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return domain.Validation("the body holds more than one JSON value", nil)
	}
	password, err := initialPassword()
	if err != nil {
		return err
	}
	u, err := o.Users.Add(r.Context(), in.Login, strings.TrimSpace(in.DisplayName), password, false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct {
		User     userResp `json:"user"`
		Password string   `json:"password"`
	}{userOf(u.ID, u.Email, u.DisplayName, u.WorkspaceAdmin, u.DisabledAt, u.CreatedAt), password})
	return nil
}

// initialPassword is 18 random bytes, 24 characters of unpadded base64url.
func initialPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
