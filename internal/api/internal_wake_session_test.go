package api

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// sessionsStub answers RegisterWakeSession and records the request; the other methods are not used.
type sessionsStub struct {
	service.Sessions
	got []service.RegisterWakeSessionReq
}

func (s *sessionsStub) RegisterWakeSession(_ context.Context, _ domain.Caller, req service.RegisterWakeSessionReq) (service.RegisterWakeSessionResp, error) {
	s.got = append(s.got, req)
	return service.RegisterWakeSessionResp{ID: "W-1", ExpiresAt: "2026-01-01T00:00:00.000Z"}, nil
}

// The handler's own guard: mounted where a TCP connection reaches it, it still answers 404 and
// registers nothing; on a connection marked as a Unix socket's it registers (the positive control).
func TestWakeSessionHandlerRefusesTCPItself(t *testing.T) {
	stub := &sessionsStub{}
	h := WakeSessionHandler(authfake.Resolver{Callers: map[string]domain.Caller{"t": {Identity: "U-1", TokenScope: "U-1"}}}, stub, log.New(io.Discard, "", 0))
	tcp := httptest.NewServer(h)
	defer tcp.Close()
	marked := httptest.NewUnstartedServer(h)
	marked.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		return core.ConnContext(ctx, &net.UnixConn{})
	}
	marked.Start()
	defer marked.Close()
	post := func(url string) int {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"socket":"/tmp/i.sock","token":"x"}`))
		req.Header.Set("Authorization", "Bearer t")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(tcp.URL); code != http.StatusNotFound || len(stub.got) != 0 {
		t.Fatalf("over TCP: %d with %d registrations, want 404 and none", code, len(stub.got))
	}
	if code := post(marked.URL); code != http.StatusCreated || len(stub.got) != 1 || stub.got[0].SocketToken != "x" || stub.got[0].SocketPath != "/tmp/i.sock" {
		t.Fatalf("over a socket: %d, %+v", code, stub.got)
	}
}
