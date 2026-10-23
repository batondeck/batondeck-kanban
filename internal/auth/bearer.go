package auth

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Bearer resolves the request's `Authorization: Bearer <token>` through r, for the HTTP routes
// outside /mcp (/wake, /internal/wake-session). No bearer, or one r refuses as UNAUTHENTICATED, is
// answered 401 with `WWW-Authenticate: Bearer`; any other error is logged and answered 500. ok is
// false once an answer is written.
func Bearer(r Resolver, logger *log.Logger, w http.ResponseWriter, req *http.Request) (c domain.Caller, ok bool) {
	token, present := BearerToken(req)
	if !present {
		unauthenticated(w, "a bearer token is required")
		return c, false
	}
	c, err := r.ResolveCaller(req.Context(), token)
	if err != nil {
		if e, isBD := domain.AsBatonDeckError(err); isBD && e.Code == domain.CodeUnauthenticated {
			unauthenticated(w, e.Message)
			return c, false
		}
		logger.Printf("auth: resolving the bearer on %s failed: %+v", req.URL.Path, err)
		WriteError(w, http.StatusInternalServerError, domain.CodeInternal, "resolving the bearer failed")
		return c, false
	}
	return c, true
}

// BearerToken is the token of the request's `Authorization: Bearer <token>`; ok false when there is
// none.
func BearerToken(req *http.Request) (token string, ok bool) {
	scheme, token, _ := strings.Cut(strings.TrimSpace(req.Header.Get("Authorization")), " ")
	token = strings.TrimSpace(token)
	return token, strings.EqualFold(scheme, "bearer") && token != ""
}

func unauthenticated(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	WriteError(w, http.StatusUnauthorized, domain.CodeUnauthenticated, message)
}

// WriteError is the error body of the HTTP routes outside /mcp: {"code", "message"}, the code one
// of domain.AllCodes.
func WriteError(w http.ResponseWriter, status int, code domain.Code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    domain.Code `json:"code"`
		Message string      `json:"message"`
	}{code, message})
}
