package servicefake

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// codes is every code the fake must return on its trigger, listed rather than derived so the table
// is the evidence; TestTriggerReturnsEveryCode also holds it to domain.AllCodes.
var codes = []domain.Code{
	domain.CodeValidation, domain.CodeUnauthenticated, domain.CodeForbidden, domain.CodeNotFound,
	domain.CodeStale, domain.CodeConflictLocked, domain.CodeLeaseExpired, domain.CodeInvalidTransition,
	domain.CodeWIPExceeded, domain.CodeCycleDetected, domain.CodeQuotaExceeded, domain.CodeRateLimited,
	domain.CodeInternal,
}

// noStringRequests are the requests with no string field, triggered through the caller instead.
var noStringRequests = []string{"ListAgentSessionsReq", "ListProjectsReq"}

type method struct {
	name string // "<Interface>.<Method>"
	fn   reflect.Value
	req  reflect.Type
}

func allMethods(t *testing.T, f *Fake) []method {
	t.Helper()
	svc := reflect.ValueOf(f.Services())
	var out []method
	for i := range svc.NumField() {
		iface := svc.Type().Field(i)
		impl := svc.Field(i)
		if impl.IsNil() {
			t.Fatalf("Services().%s is nil", iface.Name)
		}
		for j := range iface.Type.NumMethod() {
			m := iface.Type.Method(j)
			out = append(out, method{name: iface.Name + "." + m.Name, fn: impl.MethodByName(m.Name), req: m.Type.In(2)})
		}
	}
	return out
}

func invoke(m method, caller domain.Caller, req reflect.Value) (any, error) {
	out := m.fn.Call([]reflect.Value{reflect.ValueOf(context.Background()), reflect.ValueOf(caller), req})
	err, _ := out[1].Interface().(error)
	return out[0].Interface(), err
}

// firstString returns a request value with its first string field (depth first) set to s, or false.
func firstString(typ reflect.Type, s string) (reflect.Value, bool) {
	v := reflect.New(typ).Elem()
	var set func(reflect.Value) bool
	set = func(v reflect.Value) bool {
		switch v.Kind() {
		case reflect.String:
			v.SetString(s)
			return true
		case reflect.Struct:
			for i := range v.NumField() {
				if set(v.Field(i)) {
					return true
				}
			}
		}
		return false
	}
	return v, set(v)
}

func wantCode(t *testing.T, name, via string, err error, code domain.Code) {
	t.Helper()
	e, ok := domain.AsBatonDeckError(err)
	if !ok {
		t.Errorf("%s via %s: got %v, want a *domain.BatonDeckError %s", name, via, err, code)
		return
	}
	if e.Code != code || e.Retryable != domain.IsRetryable(code) {
		t.Errorf("%s via %s: got %s (retryable %v), want %s (retryable %v)", name, via, e.Code, e.Retryable, code, domain.IsRetryable(code))
	}
}

// TestTriggerReturnsEveryCode: every method of every interface returns each of the 13 codes on
// its trigger, in a request string field and in the caller's Identity.
func TestTriggerReturnsEveryCode(t *testing.T) {
	if !slices.Equal(codes, domain.AllCodes()) {
		t.Fatalf("the table lists %v, domain.AllCodes is %v", codes, domain.AllCodes())
	}
	f := New()
	ms := allMethods(t, f)
	if len(ms) != 74 {
		t.Errorf("Services has %d methods; the nine interfaces of ADR-0003 B3 hold 50, Artifacts, Comments and FollowUps (R2.1) 7, Chronicle (R2.6) 8 and Oncall (R2.8) 9", len(ms))
	}
	var stringless []string
	for _, m := range ms {
		if _, ok := firstString(m.req, ""); !ok {
			stringless = append(stringless, m.req.Name())
		}
	}
	sort.Strings(stringless)
	if !slices.Equal(stringless, noStringRequests) {
		t.Errorf("requests with no string field: %v, want %v (a new one is reachable only through the caller)", stringless, noStringRequests)
	}
	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			viaReq := 0
			for _, m := range ms {
				if req, ok := firstString(m.req, Trigger(code)); ok {
					_, err := invoke(m, domain.Caller{Identity: "u@example.com"}, req)
					wantCode(t, m.name, "the request", err, code)
					viaReq++
				}
				_, err := invoke(m, domain.Caller{Identity: Trigger(code)}, reflect.New(m.req).Elem())
				wantCode(t, m.name, "the caller", err, code)
			}
			t.Logf("%s: %d methods via the request, %d via the caller", code, viaReq, len(ms))
		})
	}
}

// TestNoTriggerReturnsTheConfiguredResult is the positive control: without a trigger a method
// returns what Set configured (or its zero value and nil), a near-miss value triggers nothing, and
// every call is recorded with its caller and request.
func TestNoTriggerReturnsTheConfiguredResult(t *testing.T) {
	f := New()
	svc := f.Services()
	ctx := context.Background()
	caller := domain.Caller{Identity: "u@example.com", Agent: "a1", Role: "worker"}

	want := service.GetTaskResp{Task: task("T-1")}
	f.Set("Tasks.Get", want, nil)
	got, err := svc.Tasks.Get(ctx, caller, service.GetTaskReq{ProjectID: "P-1", TaskID: "T-1"})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Tasks.Get = %+v, %v; want the configured response", got, err)
	}

	boom := domain.NewError(domain.CodeStale, "configured", nil)
	f.Set("Lifecycle.Claim", service.ClaimTaskResp{}, boom)
	if _, err := svc.Lifecycle.Claim(ctx, caller, service.ClaimTaskReq{ProjectID: "P-1"}); !errors.Is(err, boom) {
		t.Fatalf("Lifecycle.Claim error = %v, want the configured one", err)
	}

	for _, near := range []string{"servicefake:", "servicefake:NOPE", "servicefake:stale", "P-1"} {
		if _, err := svc.Boards.List(ctx, caller, service.ListBoardsReq{ProjectID: near}); err != nil {
			t.Errorf("Boards.List with projectId %q = %v, want no error", near, err)
		}
	}
	if resp, err := svc.Context.ListSubtasks(ctx, caller, service.ListSubtasksReq{ProjectID: "P-1"}); err != nil || !reflect.DeepEqual(resp, service.ListSubtasksResp{Tasks: []service.Task{}}) {
		t.Errorf("an unconfigured method = %+v, %v; want its zero response with tasks [] and nil", resp, err)
	}

	calls := f.Calls()
	if len(calls) != 7 {
		t.Fatalf("recorded %d calls, want 7: %+v", len(calls), calls)
	}
	if c := calls[0]; c.Method != "Tasks.Get" || c.Caller != caller || c.Req != (service.GetTaskReq{ProjectID: "P-1", TaskID: "T-1"}) {
		t.Errorf("first call recorded as %+v", c)
	}
	f.Reset()
	if len(f.Calls()) != 0 {
		t.Error("Reset kept the calls")
	}
}

// TestSetRefusesAMistake: an unknown method name or a response of another method's type panics.
func TestSetRefusesAMistake(t *testing.T) {
	for name, set := range map[string]func(*Fake){
		"unknown method": func(f *Fake) { f.Set("Tasks.Fetch", service.GetTaskResp{}, nil) },
		"wrong type":     func(f *Fake) { f.Set("Tasks.Get", service.ListTasksResp{}, nil) },
		"nil response":   func(f *Fake) { f.Set("Tasks.Get", nil, errors.New("x")) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Set did not panic", name)
				}
			}()
			set(New())
		}()
	}
	New().Set("Tasks.Get", service.GetTaskResp{Task: task("T-1")}, nil) // the right type is accepted
}

// TestTriggerWinsOverAConfiguredResult: a trigger fires even when the method has a configured
// success, and a trigger in a map key (a custom field's name) counts like one in a value.
func TestTriggerWinsOverAConfiguredResult(t *testing.T) {
	f := New()
	svc := f.Services()
	ctx := context.Background()
	caller := domain.Caller{Identity: "u@example.com"}
	f.Set("Tasks.Get", service.GetTaskResp{Task: task("T-1")}, nil)
	_, err := svc.Tasks.Get(ctx, caller, service.GetTaskReq{ProjectID: Trigger(domain.CodeNotFound), TaskID: "T-1"})
	wantCode(t, "Tasks.Get", "the request over a configured success", err, domain.CodeNotFound)
	_, err = svc.Tasks.Get(ctx, domain.Caller{Identity: Trigger(domain.CodeForbidden)}, service.GetTaskReq{ProjectID: "P-1", TaskID: "T-1"})
	wantCode(t, "Tasks.Get", "the caller over a configured success", err, domain.CodeForbidden)

	f.Set("Tasks.Create", service.CreateTaskResp{Task: task("T-2")}, nil)
	req := service.CreateTaskReq{ProjectID: "P-1", BoardID: "B-1", Title: "t",
		CustomFields: map[string]service.FieldValue{Trigger(domain.CodeValidation): "v"}}
	_, err = svc.Tasks.Create(ctx, caller, req)
	wantCode(t, "Tasks.Create", "a customFields key", err, domain.CodeValidation)
}

// TestSetRefusesANilRequiredSlice: a configured response whose required slice or map is nil (null
// on the wire, which output validation refuses) panics, at any depth; a zero optional field is off
// the wire and not inspected; an unconfigured method's zero response carries [] and {}.
func TestSetRefusesANilRequiredSlice(t *testing.T) {
	for name, resp := range map[string]any{
		"top level":  service.ListTasksResp{},
		"nested":     service.GetTaskResp{Task: service.Task{ID: "T-1"}},
		"in an item": service.ListTasksResp{Tasks: []service.Task{{ID: "T-1"}}},
		"a map":      service.ListTasksResp{Tasks: []service.Task{noFields}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Set accepted a nil required slice or map", name)
				}
			}()
			method := "Tasks.List"
			if _, ok := resp.(service.GetTaskResp); ok {
				method = "Tasks.Get"
			}
			New().Set(method, resp, nil)
		}()
	}
	New().Set("Tasks.Get", service.GetTaskResp{Task: task("T-1")}, nil) // filled: accepted
	next := task("T-1")
	New().Set("Lifecycle.Next", service.NextTaskResp{Task: &next}, nil) // inbox omitted: accepted
	resp, err := New().Services().Tasks.List(context.Background(), domain.Caller{}, service.ListTasksReq{ProjectID: "P-1"})
	if err != nil || resp.Tasks == nil {
		t.Errorf("an unconfigured Tasks.List = %+v, %v; want tasks [] and nil", resp, err)
	}
	board, err := New().Services().Boards.Get(context.Background(), domain.Caller{}, service.GetBoardReq{ProjectID: "P-1", BoardID: "B-1"})
	if err != nil || board.TaskCounts == nil {
		t.Errorf("an unconfigured Boards.Get = %+v, %v; want taskCounts {} and nil", board, err)
	}
}

// noFields is a filled Task but for its nil customFields.
var noFields = func() service.Task { t := task("T-1"); t.CustomFields = nil; return t }()

// task is a Task with every required slice and map filled.
func task(id string) service.Task {
	return service.Task{ID: id, BlockedBy: []string{}, Blocks: []string{}, Labels: []string{}, RequiredCapabilities: []string{},
		CustomFields: map[string]service.FieldValue{}}
}
