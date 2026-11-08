//go:build e2e

package e2e

import (
	"fmt"
	"net/url"
	"sync/atomic"
	"testing"
)

// project is a project the workspace admin made (so its project admin), with the master, both
// workers and the viewer as members, and one board with the default columns.
type project struct {
	id, board string
	// cols maps a status to the board's column for it.
	cols map[string]string
}

var seq atomic.Int64

// uniq is a name no other call in the run gives.
func uniq(prefix string) string { return fmt.Sprintf("%s-%d", prefix, seq.Add(1)) }

// project makes a project as the admin, adds the master (master), the worker and worker2 (worker)
// and the viewer (viewer), and a board with the default columns.
func (e *env) project(t *testing.T, name string) *project {
	t.Helper()
	p := &project{cols: map[string]string{}}
	p.id = S(e.admin.Call(t, "create_project", M{"name": uniq(name)}), "projectId")
	for role, as := range map[string]string{"master": "master", "worker": "worker", "worker2": "worker", "viewer": "viewer"} {
		e.admin.Call(t, "add_member", M{"projectId": p.id, "identityId": e.srv.ids[role], "role": as})
	}
	board := O(e.admin.Call(t, "create_board", M{"projectId": p.id, "name": "Main"}), "board")
	p.board = S(board, "id")
	for _, c := range A(board, "columns") {
		if _, ok := p.cols[S(c, "status")]; !ok {
			p.cols[S(c, "status")] = S(c, "id")
		}
	}
	return p
}

// task creates a task as the master on p's board; extra adds or overrides arguments.
func (e *env) task(t *testing.T, p *project, title string, extra M) M {
	t.Helper()
	args := M{"projectId": p.id, "boardId": p.board, "title": title, "description": "e2e: " + title}
	for k, v := range extra {
		args[k] = v
	}
	return O(e.master.Call(t, "create_task", args), "task")
}

// get reads a task as the admin.
func (e *env) get(t *testing.T, p *project, id string) M {
	t.Helper()
	return O(e.admin.Call(t, "get_task", M{"projectId": p.id, "taskId": id}), "task")
}

// version is a task's current version.
func (e *env) version(t *testing.T, p *project, id string) float64 {
	t.Helper()
	return N(e.get(t, p, id), "version")
}

// claim claims id as c and answers the lease id.
func claim(t *testing.T, c *Client, p *project, id string) string {
	t.Helper()
	lease := S(c.Call(t, "claim_task", M{"projectId": p.id, "taskId": id}), "leaseId")
	want(t, lease != "", "claim_task %s answered no lease", id)
	return lease
}

// restTask reads a task through REST as the admin: the read the UI makes.
func (e *env) restTask(t *testing.T, p *project, id string) (int, M) {
	t.Helper()
	return e.srv.rest(t, e.srv.tokens["admin"], "GET", "/api/projects/"+p.id+"/tasks/"+url.PathEscape(id), nil)
}

// feed reads the board's activity feed resource and answers its event types, newest first as the
// server orders them.
func (e *env) feedTypes(t *testing.T, c *Client, p *project) []string {
	t.Helper()
	out, rerr := c.Read(t, "batondeck://"+p.id+"/board/"+p.board+"/feed")
	if rerr != nil {
		t.Fatalf("reading the board feed: %v", rerr)
	}
	var types []string
	for _, ev := range A(out, "events") {
		types = append(types, S(ev, "type"))
	}
	return types
}

// description is tool's description in c's tools/list, as served: what a client reads before it
// calls the tool, so a scenario can hold it to the behaviour it observes.
func description(t *testing.T, c *Client, tool string) string {
	t.Helper()
	names := c.List(t, "tools/list", "tools", "name")
	descs := c.List(t, "tools/list", "tools", "description")
	for i, n := range names {
		if n == tool && i < len(descs) {
			return descs[i]
		}
	}
	t.Fatalf("tools/list as %s does not list %s", c.name, tool)
	return ""
}
