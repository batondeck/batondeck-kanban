// Package api is the HTTP routes outside /mcp and the socket-only /internal/ ones: the signed PUT
// and GET /blobs/{id} (R1.8, blobs.go).
package api

import (
	"log"
	"net/http"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// writeServiceError answers a service error with the status its code names.
func writeServiceError(w http.ResponseWriter, err error, logger *log.Logger) {
	e, ok := domain.AsBatonDeckError(err)
	if !ok {
		logger.Printf("api: an error that is not a BatonDeckError: %+v", err)
		auth.WriteError(w, http.StatusInternalServerError, domain.CodeInternal, "Internal error")
		return
	}
	status := http.StatusInternalServerError
	switch e.Code {
	case domain.CodeValidation:
		status = http.StatusBadRequest
	case domain.CodeUnauthenticated:
		status = http.StatusUnauthorized
	case domain.CodeForbidden:
		status = http.StatusForbidden
	case domain.CodeNotFound:
		status = http.StatusNotFound
	case domain.CodeInvalidTransition:
		status = http.StatusConflict
	}
	auth.WriteError(w, status, e.Code, e.Message)
}
