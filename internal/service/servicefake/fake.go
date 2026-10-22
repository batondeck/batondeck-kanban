// Package servicefake is a fake of every internal/service interface, for the transports' mapping
// tests only (ADR-0003 B3): it records each call, returns a per-method response or error, and
// returns any domain.Code on a trigger input, so a tool test can provoke every declared error. It
// implements no rule. R1.18 re-runs every transport test on the real service, which is what keeps
// this fake honest.
package servicefake

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// triggerPrefix starts every trigger value.
const triggerPrefix = "servicefake:"

// Trigger is the input value that makes any fake method fail with code: put it in any string field
// of the request (projectId by convention; nested fields count), or, for a request without one
// (list_projects, list_agent_sessions), in the caller's Identity. The error is
// domain.NewError(code, …), so the transport sees the shape the real service returns.
func Trigger(code domain.Code) string { return triggerPrefix + string(code) }

// Call is one recorded call. Method is "<Interface>.<Method>", as in service.Services.
type Call struct {
	Method string
	Caller domain.Caller
	Req    any
}

type result struct {
	resp any
	err  error
}

// Fake holds the configured results and the recorded calls. It is safe for concurrent use.
type Fake struct {
	mu      sync.Mutex
	results map[string]result
	calls   []Call
}

// New returns a Fake with no results configured: a method then returns its zero response and nil.
func New() *Fake { return &Fake{results: map[string]result{}} }

// Services returns the fake as the bundle a transport is built from.
func (f *Fake) Services() service.Services {
	return service.Services{
		Projects:  projectsFake{f},
		Boards:    boardsFake{f},
		Tasks:     tasksFake{f},
		Lifecycle: lifecycleFake{f},
		Context:   contextFake{f},
		Media:     mediaFake{f},
		Feed:      feedFake{f},
		Sessions:  sessionsFake{f},
		ToolCalls: toolCallsFake{f},
	}
}

// Set configures what method ("Tasks.Get") returns when no trigger fires: resp, which must be the
// method's response type, and err. It panics on an unknown method or a response of the wrong type,
// since either is a bug in the test that calls it.
func (f *Fake) Set(method string, resp any, err error) {
	want, ok := methods()[method]
	if !ok {
		panic(fmt.Sprintf("servicefake: no method %q", method))
	}
	if reflect.TypeOf(resp) != want {
		panic(fmt.Sprintf("servicefake: %s returns %s, not %T", method, want, resp))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[method] = result{resp: resp, err: err}
}

// Calls returns the recorded calls, oldest first.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Reset forgets the recorded calls and the configured results.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
	f.results = map[string]result{}
}

func call[Resp any](f *Fake, method string, caller domain.Caller, req any) (Resp, error) {
	var zero Resp
	f.mu.Lock()
	f.calls = append(f.calls, Call{Method: method, Caller: caller, Req: req})
	r, ok := f.results[method]
	f.mu.Unlock()
	if code, ok := triggered(caller, req); ok {
		return zero, domain.NewError(code, "servicefake: "+method+" triggered "+string(code), nil)
	}
	if !ok {
		return zero, nil
	}
	resp, _ := r.resp.(Resp)
	return resp, r.err
}

// triggered reports the code a trigger value in req's strings or the caller's Identity names.
func triggered(caller domain.Caller, req any) (domain.Code, bool) {
	if code, ok := codeOf(caller.Identity); ok {
		return code, true
	}
	return scan(reflect.ValueOf(req))
}

func codeOf(s string) (domain.Code, bool) {
	name, ok := strings.CutPrefix(s, triggerPrefix)
	if !ok {
		return "", false
	}
	for _, c := range domain.AllCodes() {
		if string(c) == name {
			return c, true
		}
	}
	return "", false
}

func scan(v reflect.Value) (domain.Code, bool) {
	switch v.Kind() {
	case reflect.String:
		return codeOf(v.String())
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return scan(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if code, ok := scan(v.Field(i)); ok {
				return code, true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if code, ok := scan(v.Index(i)); ok {
				return code, true
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if code, ok := scan(iter.Value()); ok {
				return code, true
			}
		}
	}
	return "", false
}

// methods maps "<Interface>.<Method>" to its response type, read from service.Services.
func methods() map[string]reflect.Type {
	out := map[string]reflect.Type{}
	bundle := reflect.TypeFor[service.Services]()
	for i := range bundle.NumField() {
		field := bundle.Field(i)
		for j := range field.Type.NumMethod() {
			m := field.Type.Method(j)
			out[field.Name+"."+m.Name] = m.Type.Out(0)
		}
	}
	return out
}
