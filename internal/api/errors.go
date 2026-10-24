// Package api is the HTTP routes outside /mcp: the REST BFF under /api (router.go, R1.11) and the
// signed PUT and GET /blobs/{id} (R1.8, blobs.go).
package api

import (
	"log"
	"net/http"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// writeServiceError answers a service error with ui-backend's status for its code (gateway
// shared/mcp-pool.ts STATUS) and the body {code, message, details}.
func writeServiceError(w http.ResponseWriter, err error, logger *log.Logger) {
	e, ok := domain.AsBatonDeckError(err)
	if !ok {
		logger.Printf("api: an error that is not a BatonDeckError: %+v", err)
		auth.WriteError(w, http.StatusInternalServerError, domain.CodeInternal, "Internal error")
		return
	}
	writeJSON(w, statusOf(e.Code), struct {
		Code    domain.Code    `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
	}{e.Code, e.Message, e.Details})
}

// statusOf is ui-backend's HTTP status for a code; an unknown one is 500.
func statusOf(c domain.Code) int {
	switch c {
	case domain.CodeValidation:
		return http.StatusBadRequest
	case domain.CodeUnauthenticated:
		return http.StatusUnauthorized
	case domain.CodeForbidden:
		return http.StatusForbidden
	case domain.CodeNotFound:
		return http.StatusNotFound
	case domain.CodeStale, domain.CodeConflictLocked, domain.CodeLeaseExpired, domain.CodeInvalidTransition,
		domain.CodeWIPExceeded, domain.CodeCycleDetected:
		return http.StatusConflict
	case domain.CodeQuotaExceeded, domain.CodeRateLimited:
		return http.StatusTooManyRequests
	}
	return http.StatusInternalServerError
}
