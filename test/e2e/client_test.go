//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// protocolVersion is the MCP revision the client asks for at initialize.
const protocolVersion = "2025-06-18"

// Client is a small MCP client over Streamable HTTP: initialize, then each request a POST carrying
// the bearer, the Mcp-Session-Id the server answered at initialize and the negotiated
// MCP-Protocol-Version. A reply is either one application/json message or a text/event-stream whose
// message events carry it; the client reads both.
type Client struct {
	srv     *Server
	url     string
	token   string
	name    string // clientInfo.name
	session string
	proto   string
	http    *http.Client
	next    atomic.Int64
	// Instructions and ServerVersion are what initialize answered.
	Instructions  string
	ServerVersion string
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("json-rpc %d: %s %s", e.Code, e.Message, e.Data)
}

// HTTPError is a reply that is not 2xx.
type HTTPError struct {
	Status      int
	ContentType string
	Body        string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d (%s): %s", e.Status, e.ContentType, e.Body)
}

// Reply is one decoded JSON-RPC response and how it arrived.
type Reply struct {
	Result      json.RawMessage
	Err         *RPCError
	ContentType string // the base media type the reply came in
	Status      int
}

// connect opens a session as token with clientInfo.name name; it is closed (DELETE) at the test's
// end.
func (s *Server) connect(t *testing.T, token, name string) *Client {
	t.Helper()
	c, err := s.dial(token, name)
	if err != nil {
		t.Fatalf("initialize as %s: %v", name, err)
	}
	t.Cleanup(c.Close)
	return c
}

// dial is connect leaving the error, and the session's end, to the caller.
func (s *Server) dial(token, name string) (*Client, error) {
	c := &Client{srv: s, url: s.base + "/mcp", token: token, name: name, http: &http.Client{Timeout: 60 * time.Second}}
	rep, err := c.post(context.Background(), "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": name, "version": "0.0.1"},
	}, true)
	if err != nil {
		return nil, err
	}
	if rep.Err != nil {
		return nil, rep.Err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Instructions    string `json:"instructions"`
		ServerInfo      struct {
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(rep.Result, &init); err != nil {
		return nil, err
	}
	if c.session == "" {
		return nil, errors.New("initialize answered no Mcp-Session-Id")
	}
	c.proto, c.Instructions, c.ServerVersion = init.ProtocolVersion, init.Instructions, init.ServerInfo.Version
	if err := c.notify(context.Background(), "notifications/initialized"); err != nil {
		return nil, err
	}
	return c, nil
}

// Close ends the session (DELETE /mcp), as a client that is done does.
func (c *Client) Close() {
	if c.session == "" {
		return
	}
	req, err := http.NewRequest(http.MethodDelete, c.url, nil)
	if err != nil {
		return
	}
	c.headers(req)
	if res, err := c.http.Do(req); err == nil {
		res.Body.Close()
	}
}

func (c *Client) headers(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	if c.proto != "" {
		req.Header.Set("MCP-Protocol-Version", c.proto)
	}
}

func (c *Client) notify(ctx context.Context, method string) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.headers(req)
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res.Body)
		return &HTTPError{Status: res.StatusCode, ContentType: res.Header.Get("Content-Type"), Body: string(raw)}
	}
	return nil
}

// post sends one request and reads its response, from a JSON body or an event stream.
func (c *Client) post(ctx context.Context, method string, params any, initialize bool) (*Reply, error) {
	id := c.next.Add(1)
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.headers(req)
	return c.do(req, id, initialize)
}

func (c *Client) do(req *http.Request, id int64, initialize bool) (*Reply, error) {
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if initialize && res.StatusCode == http.StatusOK {
		c.session = res.Header.Get("Mcp-Session-Id")
	}
	rep := &Reply{ContentType: ct, Status: res.StatusCode}
	switch {
	case ct == "application/json":
		// A JSON reply may be an error status carrying a JSON-RPC error (a protocol refusal).
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}
		if err := decodeResponse(raw, id, rep); err != nil {
			return nil, fmt.Errorf("HTTP %d: %w: %s", res.StatusCode, err, raw)
		}
		return rep, nil
	case ct == "text/event-stream" && res.StatusCode == http.StatusOK:
		found, err := readSSE(res.Body, id, rep)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("the event stream ended without the response to request %d", id)
		}
		return rep, nil
	default:
		raw, _ := io.ReadAll(res.Body)
		return nil, &HTTPError{Status: res.StatusCode, ContentType: res.Header.Get("Content-Type"), Body: strings.TrimSpace(string(raw))}
	}
}

// decodeResponse reads raw as the JSON-RPC response to id.
func decodeResponse(raw []byte, id int64, rep *Reply) error {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if string(m.ID) != fmt.Sprint(id) {
		return fmt.Errorf("a response to id %s, want %d", m.ID, id)
	}
	rep.Result, rep.Err = m.Result, m.Error
	return nil
}

// readSSE reads an event stream until the message answering id; notifications and other server
// messages on the stream are skipped. found is false when the stream ends first.
func readSSE(r io.Reader, id int64, rep *Reply) (found bool, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	var data []string
	event := ""
	dispatch := func() (bool, error) {
		defer func() { data, event = nil, "" }()
		if len(data) == 0 || (event != "" && event != "message") {
			return false, nil
		}
		raw := []byte(strings.Join(data, "\n"))
		var probe struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return false, fmt.Errorf("an event that is not JSON: %q", raw)
		}
		if probe.Method != "" || string(probe.ID) != fmt.Sprint(id) {
			return false, nil
		}
		return true, decodeResponse(raw, id, rep)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ok, err := dispatch(); ok || err != nil {
				return ok, err
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	if err := sc.Err(); err != nil {
		return false, err
	}
	return dispatch()
}

// Request sends a request on the session and answers the reply; transport failures are errors.
func (c *Client) Request(method string, params any) (*Reply, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return c.post(ctx, method, params, false)
}

// ToolErr is a tool's refusal: the BatonDeckError a result with isError carries, or a JSON-RPC
// error the server answered the call with (its data, when it is one, decoded the same way).
type ToolErr struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
	RPC     *RPCError      `json:"-"`
}

// Try calls tool and answers its structured result, or its refusal, or a transport failure.
func (c *Client) Try(tool string, args map[string]any) (M, *ToolErr, error) {
	if args == nil {
		args = map[string]any{}
	}
	rep, err := c.Request("tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, nil, err
	}
	if rep.Err != nil {
		te := &ToolErr{RPC: rep.Err, Message: rep.Err.Message}
		_ = json.Unmarshal(rep.Err.Data, te)
		return nil, te, nil
	}
	var res struct {
		Structured M    `json:"structuredContent"`
		IsError    bool `json:"isError"`
		Content    []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rep.Result, &res); err != nil {
		return nil, nil, err
	}
	if res.IsError {
		te := &ToolErr{}
		if res.Structured != nil {
			b, _ := json.Marshal(res.Structured)
			_ = json.Unmarshal(b, te)
		} else if len(res.Content) > 0 {
			if json.Unmarshal([]byte(res.Content[0].Text), te) != nil {
				te.Message = res.Content[0].Text
			}
		}
		return nil, te, nil
	}
	if res.Structured == nil {
		res.Structured = M{}
	}
	return res.Structured, nil, nil
}

// Call calls tool and fails the test unless it answers without isError; the call counts toward the
// tool's coverage in the running scenario.
func (c *Client) Call(t *testing.T, tool string, args map[string]any) M {
	t.Helper()
	out, te, err := c.Try(tool, args)
	if err != nil {
		t.Fatalf("%s as %s %v: %v", tool, c.name, args, err)
	}
	if te != nil {
		t.Fatalf("%s as %s %v: refused %s: %s %v", tool, c.name, args, te.Code, te.Message, te.Details)
	}
	c.srv.cov.hit(kindTool, tool, false)
	return out
}

// Refuse calls tool and fails the test unless it is refused with code; it answers the refusal.
func (c *Client) Refuse(t *testing.T, tool string, args map[string]any, code string) *ToolErr {
	t.Helper()
	out, te, err := c.Try(tool, args)
	if err != nil {
		t.Fatalf("%s as %s %v: %v", tool, c.name, args, err)
	}
	if te == nil {
		t.Fatalf("%s as %s %v: answered %v, want %s", tool, c.name, args, out, code)
	}
	if te.Code != code {
		t.Fatalf("%s as %s %v: refused %q (%s %v, rpc %v), want %s", tool, c.name, args, te.Code, te.Message, te.Details, te.RPC, code)
	}
	c.srv.cov.hit(kindTool, tool, true)
	return te
}

// Prompt gets a prompt; the result's first message text is returned.
func (c *Client) Prompt(t *testing.T, name string, args map[string]string) (string, *RPCError) {
	t.Helper()
	rep, err := c.Request("prompts/get", map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatalf("prompts/get %s: %v", name, err)
	}
	if rep.Err != nil {
		c.srv.cov.hit(kindPrompt, name, true)
		return "", rep.Err
	}
	var res struct {
		Messages []struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rep.Result, &res); err != nil || len(res.Messages) != 1 || res.Messages[0].Role != "user" {
		t.Fatalf("prompts/get %s answered %s (%v), want one user message", name, rep.Result, err)
	}
	c.srv.cov.hit(kindPrompt, name, false)
	return res.Messages[0].Content.Text, nil
}

// Read reads a resource URI and decodes its one JSON content; a refusal is returned instead. The
// read counts toward the template the URI instantiates.
func (c *Client) Read(t *testing.T, uri string) (M, *RPCError) {
	t.Helper()
	rep, err := c.Request("resources/read", map[string]any{"uri": uri})
	if err != nil {
		t.Fatalf("resources/read %s: %v", uri, err)
	}
	tmpl := c.srv.cov.templateOf(uri)
	if rep.Err != nil {
		if tmpl != "" {
			c.srv.cov.hit(kindTemplate, tmpl, true)
		}
		return nil, rep.Err
	}
	var res struct {
		Contents []struct {
			URI      string `json:"uri"`
			MIMEType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(rep.Result, &res); err != nil || len(res.Contents) != 1 {
		t.Fatalf("resources/read %s answered %s (%v), want one content", uri, rep.Result, err)
	}
	if res.Contents[0].URI != uri || res.Contents[0].MIMEType != "application/json" {
		t.Fatalf("resources/read %s answered content %s %s", uri, res.Contents[0].URI, res.Contents[0].MIMEType)
	}
	var out M
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &out); err != nil {
		t.Fatalf("resources/read %s: the content is not a JSON object: %v", uri, err)
	}
	if tmpl == "" {
		t.Fatalf("resources/read %s: the URI instantiates no listed template", uri)
	}
	c.srv.cov.hit(kindTemplate, tmpl, false)
	return out, nil
}

// List pages through a list method (tools/list, prompts/list, resources/templates/list) and
// answers every item's field key.
func (c *Client) List(t *testing.T, method, items, key string) []string {
	t.Helper()
	var names []string
	cursor := ""
	for {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		rep, err := c.Request(method, params)
		if err != nil || rep.Err != nil {
			t.Fatalf("%s: %v %v", method, err, rep)
		}
		var page map[string]json.RawMessage
		if err := json.Unmarshal(rep.Result, &page); err != nil {
			t.Fatal(err)
		}
		var list []map[string]any
		if err := json.Unmarshal(page[items], &list); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		for _, it := range list {
			names = append(names, fmt.Sprint(it[key]))
		}
		cursor = ""
		if nc, ok := page["nextCursor"]; ok {
			_ = json.Unmarshal(nc, &cursor)
		}
		if cursor == "" {
			return names
		}
	}
}

// coverage is what the scenarios on one server exercised: per kind (tool, prompt, template), the
// scenarios each name answered in, and those it was refused in.
type coverage struct {
	mu        sync.Mutex
	scenario  string
	ok        map[string]map[string]map[string]bool // kind -> name -> scenario
	refused   map[string]map[string]map[string]bool
	templates []string // the uriTemplates the server lists, by name order of templateNames
	names     []string
}

const (
	kindTool     = "tool"
	kindPrompt   = "prompt"
	kindTemplate = "template"
)

func newCoverage() *coverage {
	return &coverage{ok: map[string]map[string]map[string]bool{}, refused: map[string]map[string]map[string]bool{}}
}

func (c *coverage) setScenario(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scenario = name
}

func (c *coverage) hit(kind, name string, refused bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.ok
	if refused {
		m = c.refused
	}
	if m[kind] == nil {
		m[kind] = map[string]map[string]bool{}
	}
	if m[kind][name] == nil {
		m[kind][name] = map[string]bool{}
	}
	m[kind][name][c.scenario] = true
}

// answeredIn reports whether name answered (refused false) or was refused (true) in scenario.
func (c *coverage) in(kind, name, scenario string, refused bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.ok
	if refused {
		m = c.refused
	}
	return m[kind][name][scenario]
}

// setTemplates records the listed resource templates (name and uriTemplate), which Read matches
// a URI against.
func (c *coverage) setTemplates(names, templates []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names, c.templates = names, templates
}

// templateOf is the name of the listed template uri instantiates, "" for none. A template's
// {?cursor} matches a URI with a cursor query parameter, and only such a URI.
func (c *coverage) templateOf(uri string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, tmpl := range c.templates {
		if matchTemplate(tmpl, uri) {
			return c.names[i]
		}
	}
	return ""
}

// matchTemplate matches a level-1 RFC 6570 template, with an optional trailing {?name} query form,
// against uri.
func matchTemplate(tmpl, uri string) bool {
	query := ""
	if i := strings.Index(tmpl, "{?"); i >= 0 {
		query = strings.TrimSuffix(tmpl[i+2:], "}")
		tmpl = tmpl[:i]
	}
	path, rawQuery, _ := strings.Cut(uri, "?")
	if (query == "") != (rawQuery == "") {
		return false
	}
	if query != "" && !strings.HasPrefix(rawQuery, query+"=") {
		return false
	}
	for tmpl != "" {
		open := strings.Index(tmpl, "{")
		if open < 0 {
			return path == tmpl
		}
		if !strings.HasPrefix(path, tmpl[:open]) {
			return false
		}
		path = path[open:]
		end := strings.Index(tmpl, "}")
		tmpl = tmpl[end+1:]
		// A variable is one non-empty segment: up to the next "/" (or the end).
		seg := strings.IndexByte(path, '/')
		if seg < 0 {
			seg = len(path)
		}
		if seg == 0 {
			return false
		}
		path = path[seg:]
	}
	return path == ""
}
