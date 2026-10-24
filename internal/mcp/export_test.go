package mcp

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// The harness, exported to this directory's mcp_test files: the tool tests import
// internal/mcp/tools, which imports this package, so they cannot be package mcp.

// Harness is the R0.14 harness: the server over HTTP and a real go-sdk client.
type Harness = harness

// BootHarness is bootHarness.
func BootHarness(t testing.TB, svc service.Services, res auth.Resolver, opt ...func(*Options)) *Harness {
	return bootHarness(t, svc, res, opt...)
}

// Connect initialises a session as bearer, closed at the test's end.
func (h *harness) Connect(t testing.TB, bearer string) *mcp.ClientSession {
	return h.connect(t, bearer)
}

// ConnectNotified is Connect for a session that sends bearer to n on each tools/list_changed.
func (h *harness) ConnectNotified(t *testing.T, bearer string, n chan<- string) *mcp.ClientSession {
	return dialNotified(t, h, bearer, n)
}

// Server is the harness's server, for registering tools.
func (h *harness) Server() *Server { return h.srv }

// Log is what the server has logged.
func (h *harness) Log() string { return h.log.String() }

// ToolError is toolError: the structured error of a call that must fail, checked against
// contract/errors.json's shape.
var ToolError = toolError

// URL is the harness's /mcp endpoint, for a raw HTTP request.
func (h *harness) URL() string { return h.url }
