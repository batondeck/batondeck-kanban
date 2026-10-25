package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/dtoschema"
)

// Auth is the session, bearer and CSRF surface the BFF mounts behind (*auth.Service), and the
// re-check /api/stream makes of its caller.
type Auth interface {
	Authenticate(next http.Handler) http.Handler
	CSRF(next http.Handler) http.Handler
	LoginHandler() http.Handler
	LogoutHandler() http.Handler
	Reauthenticate(r *http.Request) (domain.Caller, error)
}

var _ Auth = (*auth.Service)(nil)

// Options is what the REST BFF is built from.
type Options struct {
	Services service.Services
	Auth     Auth
	// Users is the local accounts /api/users lists and adds to; nil answers those routes INTERNAL.
	Users *auth.Accounts
	// Tokens is the TokenService behind /api/me/cli-tokens; nil answers those routes INTERNAL.
	Tokens *auth.TokenService
	// Bus feeds /api/stream; nil answers the stream 503.
	Bus *bus.Bus
	// Presence is the device session directory /api/stream's presence frames read; nil sends none.
	Presence Presence
	Logger   *log.Logger
	// OAuth is whether serve runs the OAuth authorization server (public_url set); /api/public-config says so.
	OAuth bool

	// The stream's timings; zero is the default (StreamCoalesce, StreamPing, ReplayCap).
	Coalesce  time.Duration
	Ping      time.Duration
	ReplayCap int
}

// route is one row of the BFF's table: what routes.json lists and Mount registers.
type route struct {
	Method, Path string
	public       bool // outside Authenticate (CSRF still applies to an unsafe method)
	h            http.Handler
}

// maxBody caps a JSON request body.
const maxBody = 1 << 20

// Mount registers every BFF route on mux: /api/public-config and POST /api/login outside the
// session check, every other path behind Authenticate and then CSRF.
func Mount(mux *http.ServeMux, o Options) {
	for _, r := range table(&o) {
		h := o.Auth.CSRF(r.h)
		if !r.public {
			h = o.Auth.Authenticate(h)
		}
		mux.Handle(r.Method+" "+r.Path, h)
	}
}

// table is every route, in routes.json's order.
func table(o *Options) []route {
	var rs []route
	for _, area := range []func(*Options) []route{sessionRoutes, userRoutes, projectRoutes, boardRoutes, taskRoutes, lifecycleRoutes, contextRoutes, mediaRoutes, feedRoutes,
		meRoutes, agentRoutes, auditRoutes, notificationRoutes, commentRoutes, followUpRoutes, sprintRoutes, runRoutes, oncallRoutes, chronicleRoutes, analyticsRoutes} {
		rs = append(rs, area(o)...)
	}
	return rs
}

func (o *Options) logger() *log.Logger {
	if o.Logger == nil {
		return log.New(io.Discard, "", 0)
	}
	return o.Logger
}

// in is where a route's arguments come from: the named path values (bound to the arguments of
// the same name, or of as's names where as is set), the query keys a GET or a DELETE takes (a
// DELETE has no body, so a field it needs, delete_integration's version, rides the query), and
// whether the body may carry fields (noBody: every argument is in the URL, conductor's refusedUnknownBodyKeys
// with an empty allow-list).
type in struct {
	path   []string
	as     []string
	query  []string
	noBody bool
}

// bind writes the path values into args; they win over the body.
func (s in) bind(r *http.Request, args map[string]any) {
	for i, n := range s.path {
		name := n
		if i < len(s.as) {
			name = s.as[i]
		}
		args[name] = r.PathValue(n)
	}
}

// call is one route that is one service call: its arguments are the JSON body (or, on a GET, the
// query string, typed by the request's schema), with the path parameters over them; they are
// validated against the same schema the MCP tool registers (dtoschema) and refused when any string
// holds a NUL character (domain.CheckText, as the MCP tool does), decoded into Req, and the response
// is the tool's output as JSON.
func call[Req, Resp any](o *Options, fn func(context.Context, domain.Caller, Req) (Resp, error), spec in) http.Handler {
	schema := dtoschema.For(reflect.TypeFor[Req]())
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(fmt.Sprintf("api: resolve %s: %v", reflect.TypeFor[Req](), err))
	}
	logger := o.logger()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := auth.CallerFrom(r.Context())
		if !ok {
			auth.WriteError(w, http.StatusUnauthorized, domain.CodeUnauthenticated, "unauthenticated")
			return
		}
		args, err := arguments(w, r, schema, spec)
		if err == nil {
			spec.bind(r, args)
			if err = resolved.Validate(args); err != nil {
				err = domain.Validation(err.Error(), nil)
			} else {
				err = domain.CheckText(args)
			}
		}
		var req Req
		if err == nil {
			err = decode(args, &req)
		}
		if err != nil {
			writeServiceError(w, err, logger)
			return
		}
		resp, err := fn(r.Context(), caller, req)
		if err != nil {
			writeServiceError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

// arguments is the request's arguments before the path parameters: on a GET the route's query
// keys typed by schema (any other key is ignored, as conductor's coerceQuery does), the body's JSON
// object otherwise, and on a DELETE the route's query keys over that body.
func arguments(w http.ResponseWriter, r *http.Request, schema *jsonschema.Schema, spec in) (map[string]any, error) {
	if r.Method == http.MethodGet {
		return queryArguments(r, schema, spec)
	}
	args, err := bodyArguments(w, r, spec)
	if err != nil || r.Method != http.MethodDelete || len(spec.query) == 0 {
		return args, err
	}
	q, err := queryArguments(r, schema, spec)
	if err != nil {
		return nil, err
	}
	for k, v := range q {
		args[k] = v
	}
	return args, nil
}

// queryArguments is the route's query keys, typed by schema.
func queryArguments(r *http.Request, schema *jsonschema.Schema, spec in) (map[string]any, error) {
	args := map[string]any{}
	q := r.URL.Query()
	for _, k := range spec.query {
		p, ok := schema.Properties[k]
		if !ok {
			panic("api: query key " + k + " is not in the request schema")
		}
		vs := q[k]
		if len(vs) == 0 {
			continue
		}
		v, err := queryValue(p, vs[0])
		if err != nil {
			return nil, domain.Validation(fmt.Sprintf("query %s: %v", k, err), map[string]any{"field": k})
		}
		args[k] = v
	}
	return args, nil
}

// bodyArguments is the body's JSON object; an empty body is no arguments.
func bodyArguments(w http.ResponseWriter, r *http.Request, spec in) (map[string]any, error) {
	args := map[string]any{}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return nil, domain.Validation("the body is unreadable or over 1 MiB", nil)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return args, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil || args == nil {
		return nil, domain.Validation("the body is not a JSON object", nil)
	}
	if dec.More() {
		return nil, domain.Validation("the body holds more than one JSON value", nil)
	}
	if spec.noBody && len(args) > 0 {
		keys := make([]string, 0, len(args))
		for k := range args {
			keys = append(keys, k)
		}
		return nil, domain.Validation("this route takes no body fields", map[string]any{"fields": keys})
	}
	return numbersToFloat(args).(map[string]any), nil
}

// numbersToFloat turns json.Number into float64, the type the schema validator checks.
func numbersToFloat(v any) any {
	switch n := v.(type) {
	case map[string]any:
		for k, c := range n {
			n[k] = numbersToFloat(c)
		}
	case []any:
		for i, c := range n {
			n[i] = numbersToFloat(c)
		}
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return n.String()
		}
		return f
	}
	return v
}

// queryValue types a query string value by its property's schema: an integer or number is parsed,
// a boolean is true or false, anything else is the string.
func queryValue(p *jsonschema.Schema, s string) (any, error) {
	types := p.Types
	if p.Type != "" {
		types = []string{p.Type}
	}
	for _, t := range types {
		switch t {
		case "integer", "number":
			return strconv.ParseFloat(s, 64)
		case "boolean":
			return strconv.ParseBool(s)
		}
	}
	return s, nil
}

func decode(args map[string]any, req any) error {
	b, err := json.Marshal(args)
	if err == nil {
		err = json.Unmarshal(b, req)
	}
	if err != nil {
		var e *domain.BatonDeckError
		if errors.As(err, &e) {
			return e
		}
		return domain.Validation(err.Error(), nil)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// The table's path prefixes.
const (
	pp  = "/api/projects/{projectId}"
	ppb = pp + "/boards/{boardId}"
	ppt = pp + "/tasks/{taskId}"
)

var (
	byProject = in{path: []string{"projectId"}}
	byBoard   = in{path: []string{"projectId", "boardId"}}
	byTask    = in{path: []string{"projectId", "taskId"}}
)
