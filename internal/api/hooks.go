package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"

	"github.com/batondeck/batondeck-kanban/internal/service"
)

// Hooks is the service's end of a webhook delivery (service.Core.Hook).
type Hooks interface {
	Hook(ctx context.Context, integrationID string, r service.HookRequest) (service.HookOutcome, error)
}

// HookBodyMax is the route's outer body cap: the largest payloadCapKb an integration may set
// (512 KiB). The integration's own cap is the service's, once it knows which integration it is.
const HookBodyMax = 512 << 10

// HookRoutes is POST /hooks/{integrationId}, the one route that parses attacker-controlled JSON.
// It is mounted outside every bearer, session and CSRF check: the integration id is the address
// and the delivery's signature, key or bearer is the credential, verified by the service before a
// byte is parsed. The body is capped before anything reads it (CapBody).
type HookRoutes struct {
	Hooks  Hooks
	Logger *log.Logger
}

// hookID is an integration id's shape; anything else is the flat 404 without a lookup.
var hookID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Handler is the route, capped.
func (h HookRoutes) Handler() http.Handler {
	return CapBody(http.HandlerFunc(h.serve), HookBodyMax, func(r *http.Request, declared int64) {
		h.refused(r.PathValue("integrationId"), "too_large", fmt.Sprintf("over the route's %d-byte cap (declared length %d)", HookBodyMax, declared))
	})
}

// refused is a refusal's stderr line, as the service writes its own: the id as presented (quoted
// and cut, since it is the sender's), the reason; never the body.
func (h HookRoutes) refused(id, why, detail string) {
	if h.Logger == nil {
		return
	}
	if r := []rune(id); len(r) > 80 {
		id = string(r[:80]) + "…"
	}
	h.Logger.Printf("ingest: refused a delivery to integration %q: %s: %s", id, why, detail)
}

func (h HookRoutes) serve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("integrationId")
	if !hookID.MatchString(id) {
		h.refused(id, "not_found", "the id is not an integration id's shape")
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "NOT_FOUND"})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "VALIDATION", "message": "the request body could not be read"})
		return
	}
	out, err := h.Hooks.Hook(r.Context(), id, service.HookRequest{Header: r.Header.Get, Body: body})
	if err != nil {
		if h.Logger != nil {
			h.Logger.Printf("api: hook %s: %+v", id, err)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "retryable": true})
		return
	}
	switch out.Refusal {
	case "":
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": len(out.Results), "results": out.Results})
	case service.HookNotFound:
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "NOT_FOUND"})
	case service.HookTooLarge:
		tooLarge(w)
	case service.HookRateLimited:
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"code": "RATE_LIMITED", "retryable": true, "results": out.Results})
	case service.HookUnauthorized:
		// An RFC 7617 sender (Cloud Monitoring among them) sends credentials only after a challenge.
		if out.Challenge {
			w.Header().Set("WWW-Authenticate", `Basic realm="batondeck-hooks", charset="UTF-8"`)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHENTICATED"})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "VALIDATION", "message": out.Detail, "results": out.Results})
	}
}

// CapBody refuses a request body past maxBytes, counted by the bytes that arrive rather than the
// length the request declares, with 413 before anything parses it; a body within the cap is handed
// on whole (ported from hdtp-gateway internal/public/bounds.go, Apache-2.0). refused, when set, hears
// each 413 with the length the request declared (-1 unknown).
func CapBody(next http.Handler, maxBytes int64, refused func(r *http.Request, declared int64)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > maxBytes {
			if refused != nil {
				refused(r, r.ContentLength)
			}
			tooLarge(w)
			return
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
		if err != nil {
			http.Error(w, "the request body could not be read", http.StatusBadRequest)
			return
		}
		if int64(len(b)) > maxBytes {
			if refused != nil {
				refused(r, r.ContentLength)
			}
			tooLarge(w)
			return
		}
		r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(b)), int64(len(b))
		next.ServeHTTP(w, r)
	})
}

func tooLarge(w http.ResponseWriter) {
	writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "too_large"})
}
