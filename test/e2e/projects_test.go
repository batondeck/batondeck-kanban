//go:build e2e

package e2e

import "testing"

func init() {
	register(scenario{area: "projects", name: "create-read-list", tools: []string{"create_project", "get_project", "list_projects"}, run: projectCreateReadList})
	register(scenario{area: "projects", name: "members", tools: []string{"add_member", "remove_member"}, run: projectMembers})
	register(scenario{area: "projects", name: "settings", tools: []string{"update_settings"}, run: projectSettings})
	register(scenario{area: "projects", name: "delete", tools: []string{"delete_project"}, run: projectDelete})
}

// A project is made by the admin, who becomes its admin; its members read it with their roles, the
// outsider cannot, and the listing's all:true is refused as documented.
func projectCreateReadList(t *testing.T, e *env) {
	e.admin.Refuse(t, "create_project", M{"name": ""}, "VALIDATION")
	name := uniq("Launch")
	pid := S(e.admin.Call(t, "create_project", M{"name": name, "idempotencyKey": uniq("cp")}), "projectId")
	want(t, pid != "", "create_project answered no projectId")

	// The separate read: REST, as the UI reads it.
	code, body := e.srv.rest(t, e.srv.tokens["admin"], "GET", "/api/projects/"+pid, nil)
	want(t, code == 200 && S(body, "project.name") == name, "GET /api/projects/%s: %d %s", pid, code, js(body))
	want(t, S(body, "project.members."+e.srv.ids["admin"]) == "admin", "the creator is not the project's admin: %s", js(body))

	e.admin.Call(t, "add_member", M{"projectId": pid, "identityId": e.srv.ids["master"], "role": "master"})
	got := e.master.Call(t, "get_project", M{"projectId": pid})
	want(t, S(got, "project.id") == pid && S(got, "project.name") == name, "get_project as the master: %s", js(got))
	want(t, S(got, "project.members."+e.srv.ids["master"]) == "master", "get_project members: %s", js(O(got, "project.members")))
	want(t, S(got, "memberDisplay."+e.srv.ids["master"]) != "", "get_project memberDisplay names no label for the master: %s", js(O(got, "memberDisplay")))
	e.outsider.Refuse(t, "get_project", M{"projectId": pid}, "FORBIDDEN")
	e.master.Refuse(t, "get_project", M{"projectId": "P-doesnotexist"}, "FORBIDDEN")

	roleIn := func(c *Client) string {
		for _, p := range A(c.Call(t, "list_projects", M{}), "projects") {
			if S(p, "projectId") == pid {
				return S(p, "role")
			}
		}
		return ""
	}
	want(t, roleIn(e.admin) == "admin", "list_projects as the admin: no admin row for %s", pid)
	want(t, roleIn(e.master) == "master", "list_projects as the master: no master row for %s", pid)
	want(t, roleIn(e.outsider) == "", "list_projects as the outsider lists %s", pid)
	e.admin.Refuse(t, "list_projects", M{"all": true}, "VALIDATION")
	e.master.Refuse(t, "list_projects", M{"all": true}, "FORBIDDEN")
}

// The project admin adds, re-roles and removes members; each change is read back through
// get_project and through what the member can then do. A worker cannot manage members, an unknown
// role is refused, and the last admin can be neither demoted nor removed.
func projectMembers(t *testing.T, e *env) {
	pid := S(e.admin.Call(t, "create_project", M{"name": uniq("Members")}), "projectId")
	members := func() M { return O(e.admin.Call(t, "get_project", M{"projectId": pid}), "project.members") }

	e.admin.Call(t, "add_member", M{"projectId": pid, "identityId": e.srv.ids["worker"], "role": "worker"})
	// A login is accepted as the identity, as documented.
	added := e.admin.Call(t, "add_member", M{"projectId": pid, "identityId": e.srv.logins["worker2"], "role": "viewer"})
	want(t, S(added, "members."+e.srv.ids["worker2"]) == "viewer", "add_member by login answered %s", js(added))
	want(t, S(members(), e.srv.ids["worker"]) == "worker", "get_project after add_member: %s", js(members()))
	// The new member reads the project; the worker manages nobody.
	e.worker.Call(t, "get_project", M{"projectId": pid})
	e.worker.Refuse(t, "add_member", M{"projectId": pid, "identityId": e.srv.ids["viewer"], "role": "viewer"}, "FORBIDDEN")
	e.admin.Refuse(t, "add_member", M{"projectId": pid, "identityId": e.srv.ids["viewer"], "role": "owner"}, "VALIDATION")

	// A role change is an add_member on an existing member.
	e.admin.Call(t, "add_member", M{"projectId": pid, "identityId": e.srv.ids["worker"], "role": "master"})
	want(t, S(members(), e.srv.ids["worker"]) == "master", "the re-role did not land: %s", js(members()))

	e.admin.Refuse(t, "add_member", M{"projectId": pid, "identityId": e.srv.ids["admin"], "role": "worker"}, "VALIDATION")
	e.worker.Refuse(t, "remove_member", M{"projectId": pid, "identityId": e.srv.ids["worker2"]}, "FORBIDDEN")
	e.admin.Refuse(t, "remove_member", M{"projectId": pid, "identityId": e.srv.ids["admin"]}, "VALIDATION")

	got := e.admin.Call(t, "remove_member", M{"projectId": pid, "identityId": e.srv.ids["worker"]})
	want(t, B(got, "ok"), "remove_member answered %s", js(got))
	_, gone := members()[e.srv.ids["worker"]]
	want(t, !gone, "get_project still lists the removed member: %s", js(members()))
	e.worker.Refuse(t, "get_project", M{"projectId": pid}, "FORBIDDEN")
}

// The project admin changes the settings and reads them back; the change takes effect (with review
// off, a completed task lands DONE). A master may not change them, and quota fields are refused.
func projectSettings(t *testing.T, e *env) {
	p := e.project(t, "Settings")
	before := O(e.admin.Call(t, "get_project", M{"projectId": p.id}), "project.settings")
	want(t, B(before, "reviewEnabled"), "a new project does not review: %s", js(before))

	got := e.admin.Call(t, "update_settings", M{"projectId": p.id, "reviewEnabled": false, "maxRunsPerTask": 4, "color": "#123456"})
	want(t, O(got, "settings")["reviewEnabled"] == false && N(got, "settings.maxRunsPerTask") == 4, "update_settings answered %s", js(got))
	after := O(e.master.Call(t, "get_project", M{"projectId": p.id}), "project.settings")
	want(t, after["reviewEnabled"] == false && N(after, "maxRunsPerTask") == 4 && S(after, "color") == "#123456", "get_project settings after update: %s", js(after))

	id := S(e.task(t, p, "settled", nil), "id")
	lease := claim(t, e.worker, p, id)
	done := e.worker.Call(t, "complete_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "deliverable": "done"})
	want(t, S(done, "task.status") == "DONE", "with review off complete_task landed %s", S(done, "task.status"))

	e.master.Refuse(t, "update_settings", M{"projectId": p.id, "reviewEnabled": true}, "FORBIDDEN")
	e.admin.Refuse(t, "update_settings", M{"projectId": p.id, "quotaEnabled": true}, "VALIDATION")
	e.admin.Refuse(t, "update_settings", M{"projectId": p.id, "maxRunsPerTask": -1}, "VALIDATION")
}

// The project admin deletes a project with its boards and tasks: it leaves every member's listing
// and its task no longer reads. A master may not delete it.
func projectDelete(t *testing.T, e *env) {
	p := e.project(t, "Doomed")
	id := S(e.task(t, p, "doomed task", nil), "id")
	e.master.Refuse(t, "delete_project", M{"projectId": p.id}, "FORBIDDEN")
	e.viewer.Refuse(t, "delete_project", M{"projectId": p.id}, "FORBIDDEN")

	got := e.admin.Call(t, "delete_project", M{"projectId": p.id})
	want(t, B(got, "ok"), "delete_project answered %s", js(got))
	for _, pr := range A(e.master.Call(t, "list_projects", M{}), "projects") {
		want(t, S(pr, "projectId") != p.id, "list_projects still lists the deleted project")
	}
	code, _ := e.srv.rest(t, e.srv.tokens["admin"], "GET", "/api/projects/"+p.id+"/tasks/"+id, nil)
	want(t, code == 403 || code == 404, "the deleted project's task still reads over REST: %d", code)
	e.admin.Refuse(t, "get_project", M{"projectId": p.id}, "FORBIDDEN")
}
