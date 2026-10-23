package wake

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// maxRegisterBody bounds the request body, counted in bytes read.
const maxRegisterBody = 16 << 10

// registerBody is POST /internal/wake-session's body (R3.3's `session register`): the client
// process and its role, its agent label, and, for Claude Code, CLAUDE_CODE_MESSAGING_SOCKET with
// the sessionId and procStart of its registry entry; token is the socket's own token, if any.
type registerBody struct {
	PID       int64  `json:"pid"`
	Role      string `json:"role"`
	Agent     string `json:"agent"`
	Socket    string `json:"socket"`
	Token     string `json:"token"`
	SessionID string `json:"sessionId"`
	ProcStart string `json:"procStart"`
	Cursor    int64  `json:"cursor"`
}

// RegisterHandler is POST /internal/wake-session: it adds the body's client process to the
// directory for the bearer's identity and answers 201 RegisterResp. Served only on a Unix socket
// Listen made (a request over TCP is 404, as a route that does not exist). Refusals: no or a bad
// bearer 401; a body that is not that JSON object, over 16 KiB, a pid that is not live, a bad role,
// a socket the registry does not name 400; a pid another uid owns, or another identity registered,
// 403.
func RegisterHandler(d *Directory, r auth.Resolver, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		peer, ok := core.PeerOf(req.Context())
		if !ok {
			http.NotFound(w, req)
			return
		}
		caller, ok := auth.Bearer(r, logger, w, req)
		if !ok {
			return
		}
		bearer, _ := auth.BearerToken(req)
		var body registerBody
		dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxRegisterBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil || !errors.Is(dec.Decode(&struct{}{}), io.EOF) {
			auth.WriteError(w, http.StatusBadRequest, domain.CodeValidation, "the body must be one JSON object {pid, role, agent, socket, token, sessionId, procStart, cursor}")
			return
		}
		if body.Cursor < 0 {
			auth.WriteError(w, http.StatusBadRequest, domain.CodeValidation, "cursor cannot be negative")
			return
		}
		resp, err := d.Register(req.Context(), peer, caller, bearer, RegisterReq(body))
		if err != nil {
			writeError(w, err, logger)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// UnregisterHandler is DELETE /internal/wake-session/{pid} (R3.3's `session unregister`, the
// SessionEnd hook): 204 when the bearer's identity had that pid registered, else 404. Socket only.
func UnregisterHandler(d *Directory, r auth.Resolver, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, ok := core.PeerOf(req.Context()); !ok {
			http.NotFound(w, req)
			return
		}
		caller, ok := auth.Bearer(r, logger, w, req)
		if !ok {
			return
		}
		pid, err := strconv.ParseInt(req.PathValue("pid"), 10, 64)
		if err != nil || pid <= 0 {
			auth.WriteError(w, http.StatusBadRequest, domain.CodeValidation, "pid must be a positive process id")
			return
		}
		if err := d.Unregister(req.Context(), caller, pid); err != nil {
			writeError(w, err, logger)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// writeError answers a directory error with the status its code names.
func writeError(w http.ResponseWriter, err error, logger *log.Logger) {
	e, ok := domain.AsBatonDeckError(err)
	if !ok {
		logger.Printf("wake: an error that is not a BatonDeckError: %+v", err)
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
