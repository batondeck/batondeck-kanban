// Package api is the HTTP routes outside /mcp and /wake. R0.15 adds the first,
// POST /internal/wake-session.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"path/filepath"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// maxWakeSessionBody bounds the request body, counted in bytes read.
const maxWakeSessionBody = 16 << 10

// wakeSessionReq is the body: the session inbox socket's absolute path and, when it has one, the
// token it expects (Claude Code's CLAUDE_CODE_MESSAGING_SOCKET and CLAUDE_CODE_MESSAGING_TOKEN).
type wakeSessionReq struct {
	Socket string `json:"socket"`
	Token  string `json:"token"`
}

// WakeSessionHandler is POST /internal/wake-session {socket, token}: it registers a Claude Code
// session inbox for the bearer's identity (service.Sessions.RegisterWakeSession) and answers 201
// {id, expiresAt}. It is served only on a Unix socket: a request that arrived over TCP is 404, the
// answer for a route that does not exist. Refusals: no or a bad bearer 401; a body that is not that
// JSON object, over 16 KiB, or a socket that is not an absolute path 400; the service's errors by
// their code.
func WakeSessionHandler(r auth.Resolver, sessions service.Sessions, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !core.ViaUnixSocket(req.Context()) {
			http.NotFound(w, req)
			return
		}
		caller, ok := auth.Bearer(r, logger, w, req)
		if !ok {
			return
		}
		var body wakeSessionReq
		dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxWakeSessionBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			auth.WriteError(w, http.StatusBadRequest, domain.CodeValidation, "the body must be one JSON object {socket, token}")
			return
		}
		if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			auth.WriteError(w, http.StatusBadRequest, domain.CodeValidation, "the body must be one JSON object {socket, token}")
			return
		}
		if !filepath.IsAbs(body.Socket) {
			auth.WriteError(w, http.StatusBadRequest, domain.CodeValidation, "socket must be an absolute path")
			return
		}
		resp, err := sessions.RegisterWakeSession(req.Context(), caller, service.RegisterWakeSessionReq{SocketPath: body.Socket, SocketToken: body.Token})
		if err != nil {
			writeServiceError(w, err, logger)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	})
}

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
	}
	auth.WriteError(w, status, e.Code, e.Message)
}
