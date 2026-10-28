package mcp

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// harness is the server over HTTP with a real go-sdk client, for every tool test (conductor's
// testing contract: a real MCP client harness). It takes the services and the resolver, so a test
// passes servicefake and authfake here, and R1.18 the real service over either engine.
type harness struct {
	srv *Server
	url string
	log *syncBuffer
}

// bootHarness starts the server on an httptest listener; opt adjusts the Options (a lowered
// SessionIdle, a MaxSessions) before New. The listener closes at the test's end.
func bootHarness(t testing.TB, svc service.Services, res auth.Resolver, opt ...func(*Options)) *harness {
	t.Helper()
	logs := &syncBuffer{}
	o := Options{Version: "test", Resolver: res, Services: svc, Logger: log.New(logs, "", 0)}
	for _, f := range opt {
		f(&o)
	}
	s := New(o)
	mux := http.NewServeMux()
	mux.Handle("/mcp", s.Handler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &harness{srv: s, url: ts.URL + "/mcp", log: logs}
}

// connect initialises a session as bearer and closes it at the test's end.
func (h *harness) connect(t testing.TB, bearer string) *mcp.ClientSession {
	t.Helper()
	cs, err := h.dial(bearer)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// dial initialises a session as bearer, leaving the error to the caller.
func (h *harness) dial(bearer string) (*mcp.ClientSession, error) { return h.dialAs(bearer, "harness") }

// dialAs is dial for a client whose initialize clientInfo.name is client.
func (h *harness) dialAs(bearer, client string) (*mcp.ClientSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := mcp.NewClient(&mcp.Implementation{Name: client, Version: "0.0.1"}, nil)
	return c.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   h.url,
		HTTPClient: &http.Client{Transport: bearerTransport{bearer: bearer}},
		MaxRetries: -1,
	}, nil)
}

// bearerTransport presents bearer on every request.
type bearerTransport struct{ bearer string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.bearer)
	return http.DefaultTransport.RoundTrip(r)
}

// syncBuffer is the server's log, safe to read while the server writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
