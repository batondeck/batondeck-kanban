package mcp_test

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// The R2.1 rig for the artifact, comment and follow-up tools on the real stack, every selected
// engine: ada owns project p (its admin), will and max are p's workers, mona its master; grace owns
// q and is a member of nothing of ada's.

const (
	notesAda   = "bk_notes_ada"
	notesWill  = "bk_notes_will"
	notesMax   = "bk_notes_max"
	notesMona  = "bk_notes_mona"
	notesGrace = "bk_notes_grace"
)

type notesRig struct {
	t                    *testing.T
	ada, will, max, mona *caller
	grace                *caller
	p, b, q, qb          string
	engine               string
}

func notesStacks(t *testing.T, fn func(t *testing.T, r *notesRig)) {
	res := authfake.Resolver{Callers: map[string]domain.Caller{
		notesAda:   {Identity: "u-ada", Agent: "agent-ada", TokenScope: "u-ada"},
		notesWill:  {Identity: "u-will", Agent: "agent-will", TokenScope: "u-will"},
		notesMax:   {Identity: "u-max", TokenScope: "u-max"},
		notesMona:  {Identity: "u-mona", TokenScope: "u-mona"},
		notesGrace: {Identity: "u-grace", TokenScope: "u-grace"},
	}}
	for _, e := range servicetest.Engines(t) {
		t.Run(e, func(t *testing.T) {
			st := servicetest.OpenStore(t, e)
			c := service.New(st, service.Options{Logger: log.New(io.Discard, "", 0), ToolCallFlush: -1})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			svc := servicetest.ServicesOf(c)
			h := bk.BootHarness(t, svc, res)
			tools.Register(h.Server(), svc)
			conn := func(tok string) *caller { return &caller{t: t, cs: h.Connect(t, tok), called: map[string]int{}} }
			r := &notesRig{t: t, engine: e, ada: conn(notesAda), will: conn(notesWill), max: conn(notesMax), mona: conn(notesMona), grace: conn(notesGrace)}
			r.p = str(r.ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
			r.b = str(obj(r.ada.call("create_board", map[string]any{"projectId": r.p, "name": "Main"})["board"])["id"])
			for id, role := range map[string]string{"u-will": "worker", "u-max": "worker", "u-mona": "master"} {
				r.ada.call("add_member", map[string]any{"projectId": r.p, "identityId": id, "role": role})
			}
			r.q = str(r.grace.call("create_project", map[string]any{"name": "Grace's"})["projectId"])
			r.qb = str(obj(r.grace.call("create_board", map[string]any{"projectId": r.q, "name": "Main"})["board"])["id"])
			fn(t, r)
		})
	}
}

// task is a new task on p, assigned to assignee when it is not "".
func (r *notesRig) task(title, assignee string) map[string]any {
	r.t.Helper()
	t := obj(r.ada.call("create_task", map[string]any{"projectId": r.p, "boardId": r.b, "title": title})["task"])
	if assignee == "" {
		return t
	}
	return obj(r.ada.call("update_task", map[string]any{"projectId": r.p, "taskId": t["id"], "version": t["version"], "patch": map[string]any{"assignee": assignee}})["task"])
}

// tick keeps two writes in distinct milliseconds: the lists order by (ts, id) and ids are random.
func tick() { time.Sleep(2 * time.Millisecond) }

func (r *notesRig) get(id string) map[string]any {
	r.t.Helper()
	return obj(r.ada.call("get_task", map[string]any{"projectId": r.p, "taskId": id})["task"])
}

// moveTo walks task id along the fixed table to status (TODO, IN_PROGRESS, REVIEW, DONE).
func (r *notesRig) moveTo(id, status string) map[string]any {
	r.t.Helper()
	path := []string{"IN_PROGRESS", "REVIEW", "DONE"}
	cur := r.get(id)
	for _, s := range path {
		cur = obj(r.ada.call("move_task", map[string]any{"projectId": r.p, "taskId": id, "version": cur["version"], "toStatus": s})["task"])
		if s == status {
			break
		}
	}
	if cur["status"] != status {
		r.t.Fatalf("moveTo %s: %v", status, cur["status"])
	}
	return cur
}

// wantCode is a call that fails with code, its error checked against contract/errors.json, and
// logged so the -v run lists each declared error per tool and engine.
func (r *notesRig) wantCode(c *caller, tool string, args map[string]any, code, why string) map[string]any {
	r.t.Helper()
	e := bk.ToolError(r.t, c.cs, tool, args)
	if e["code"] != code {
		r.t.Fatalf("%s (%s) %v: %v, want %s", tool, why, args, e, code)
	}
	r.t.Logf("%s %s: %s (%s)", r.engine, tool, code, why)
	return e
}
