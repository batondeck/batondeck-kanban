package mcp_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

func rawPost(url, bearer, id, msg string) (int, string, error) {
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(msg))
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", id)
	req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), nil
}

// A self-disconnect while other requests on the same session keep finishing gets its answer whole,
// and the session ends (P-450): before, whichever request finished first after the mark closed the
// transport, and the answer arrived as an empty 200 (45 of 60 on 2ff5de6). This guards the whole
// path, including sessionMiddleware reading the mark from the request's headers.
func TestSelfDisconnectUnderOtherRequests(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		lost, notClosed := 0, 0
		const rounds = 10
		for i := 0; i < rounds; i++ {
			cs := h.Connect(t, stackGrace)
			id := cs.ID()
			stop := make(chan struct{})
			var wg sync.WaitGroup
			for w := 0; w < 6; w++ {
				wg.Go(func() {
					for {
						select {
						case <-stop:
							return
						default:
						}
						_, _, _ = rawPost(h.URL(), stackGrace, id, `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`)
					}
				})
			}
			code, body, err := rawPost(h.URL(), stackGrace, id, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"disconnect_agent","arguments":{"identityId":"u-grace","agentId":"harness"}}}`)
			close(stop)
			wg.Wait()
			if err != nil || code != 200 || !strings.Contains(body, `"disconnected":true`) {
				lost++
				if lost == 1 {
					t.Logf("first lost answer: %d %q %v", code, body, err)
				}
			}
			// After everything settles, the session is gone.
			closed := false
			for k := 0; k < 100; k++ {
				if c, _, _ := rawPost(h.URL(), stackGrace, id, `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`); c == 404 {
					closed = true
					break
				}
			}
			if !closed {
				notClosed++
			}
		}
		t.Logf("%s: %d rounds: %d self-disconnect answers lost, %d sessions not closed", s.Engine, rounds, lost, notClosed)
		if lost > 0 || notClosed > 0 {
			t.Fail()
		}
	})
}
