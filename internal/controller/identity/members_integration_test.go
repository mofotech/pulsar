package identity

import (
	"context"
	"net/http"
	"testing"
)

func (e *env) memberRole(projectID, userID string) string {
	e.t.Helper()
	var role string
	_ = e.db.Pool().QueryRow(context.Background(),
		`SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID).Scan(&role)
	return role
}

func (e *env) addMember(projectID, userID, role string) {
	e.t.Helper()
	e.exec(`INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)
	        ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role`, projectID, userID, role)
}

func TestProjectCreatorBecomesAdmin(t *testing.T) {
	e := newEnv(t)
	org := e.createOrg("acme")
	uid := e.createUser("founder@acme.test", userOpts{orgID: org, orgRole: "org_admin"})
	r := e.do(http.MethodPost, "/v1/projects", e.mustLogin("founder@acme.test"), map[string]string{"name": "web"})
	expectStatus(t, r, http.StatusCreated)
	var p Project
	r.json(t, &p)
	if got := e.memberRole(p.ID, uid); got != "admin" {
		t.Fatalf("creator role = %q, want admin", got)
	}
}

func TestProjectMembershipManagement(t *testing.T) {
	e := newEnv(t)
	org, other := e.createOrg("acme"), e.createOrg("other")
	proj := e.createProject(org, "web")
	home := e.createProject(org, "home")

	projAdmin := e.createUser("padmin@acme.test", userOpts{orgID: org, projectID: home})
	e.addMember(proj, projAdmin, "admin")
	member := e.createUser("member@acme.test", userOpts{orgID: org, projectID: home})
	e.addMember(proj, member, "member")
	reader := e.createUser("reader@acme.test", userOpts{orgID: org, projectID: home})
	e.addMember(proj, reader, "reader")
	newbie := e.createUser("newbie@acme.test", userOpts{orgID: org, projectID: home})
	outsider := e.createUser("outsider@other.test", userOpts{orgID: other})
	// Home projects set so login doesn't auto-join them to the first org project.
	e.createUser("orgadmin@acme.test", userOpts{orgID: org, orgRole: "org_admin", projectID: home})
	otherHome := e.createProject(other, "other-home")
	e.createUser("orgadmin@other.test", userOpts{orgID: other, orgRole: "org_admin", projectID: otherHome})

	tok := map[string]string{}
	for _, u := range []string{"padmin@acme.test", "member@acme.test", "reader@acme.test", "newbie@acme.test",
		"orgadmin@acme.test", "orgadmin@other.test", "outsider@other.test"} {
		tok[u] = e.mustLogin(u)
	}
	members := "/v1/projects/" + proj + "/members"

	t.Run("who can list members", func(t *testing.T) {
		for u, want := range map[string]int{
			"reader@acme.test": 200, "member@acme.test": 200, "padmin@acme.test": 200, "orgadmin@acme.test": 200,
			"newbie@acme.test": 404, "orgadmin@other.test": 404, "outsider@other.test": 404,
		} {
			if got := e.do(http.MethodGet, members, tok[u], nil).status; got != want {
				t.Errorf("%s: %d, want %d", u, got, want)
			}
		}
		var list []ProjectMember
		e.do(http.MethodGet, members, tok["reader@acme.test"], nil).json(t, &list)
		if len(list) != 3 {
			t.Fatalf("members: %+v", list)
		}
	})

	t.Run("only admins manage membership", func(t *testing.T) {
		for _, u := range []string{"member@acme.test", "reader@acme.test"} {
			r := e.do(http.MethodPut, members+"/"+newbie, tok[u], map[string]string{"role": "member"})
			expectStatus(t, r, http.StatusForbidden)
		}
		expectStatus(t, e.do(http.MethodPut, members+"/"+newbie, tok["orgadmin@other.test"],
			map[string]string{"role": "member"}), http.StatusNotFound)
		if e.memberRole(proj, newbie) != "" {
			t.Fatal("unauthorized caller added a member")
		}
	})

	t.Run("project admin adds, changes and removes members", func(t *testing.T) {
		r := e.do(http.MethodPut, members+"/"+newbie, tok["padmin@acme.test"], map[string]string{"role": "reader"})
		expectStatus(t, r, http.StatusOK)
		var m ProjectMember
		r.json(t, &m)
		if m.Role != "reader" || m.Email != "newbie@acme.test" || e.memberRole(proj, newbie) != "reader" {
			t.Fatalf("after add: %+v / %q", m, e.memberRole(proj, newbie))
		}
		expectStatus(t, e.do(http.MethodPut, members+"/"+newbie, tok["padmin@acme.test"],
			map[string]string{"role": "member"}), http.StatusOK)
		if e.memberRole(proj, newbie) != "member" {
			t.Fatal("role not changed")
		}
		expectStatus(t, e.do(http.MethodDelete, members+"/"+newbie, tok["padmin@acme.test"], nil), http.StatusNoContent)
		if e.memberRole(proj, newbie) != "" {
			t.Fatal("member not removed")
		}
	})

	t.Run("org admin manages any project in their org", func(t *testing.T) {
		expectStatus(t, e.do(http.MethodPut, members+"/"+newbie, tok["orgadmin@acme.test"],
			map[string]string{"role": "admin"}), http.StatusOK)
		if e.memberRole(proj, newbie) != "admin" {
			t.Fatal("org admin could not promote")
		}
	})

	t.Run("validation", func(t *testing.T) {
		expectStatus(t, e.do(http.MethodPut, members+"/"+member, tok["padmin@acme.test"],
			map[string]string{"role": "owner"}), http.StatusBadRequest)
		expectStatus(t, e.do(http.MethodPut, members+"/"+outsider, tok["padmin@acme.test"],
			map[string]string{"role": "member"}), http.StatusBadRequest) // other org
		expectStatus(t, e.do(http.MethodDelete, "/v1/projects/"+home+"/members/"+member, tok["orgadmin@acme.test"], nil),
			http.StatusConflict) // home project
	})

	t.Run("the last admin can't be removed or demoted", func(t *testing.T) {
		solo := e.createProject(org, "solo")
		e.addMember(solo, projAdmin, "admin")
		path := "/v1/projects/" + solo + "/members/" + projAdmin
		expectStatus(t, e.do(http.MethodPut, path, tok["orgadmin@acme.test"], map[string]string{"role": "member"}),
			http.StatusConflict)
		expectStatus(t, e.do(http.MethodDelete, path, tok["orgadmin@acme.test"], nil), http.StatusConflict)
		// With a second admin, the first can step down.
		e.addMember(solo, member, "admin")
		expectStatus(t, e.do(http.MethodPut, path, tok["padmin@acme.test"], map[string]string{"role": "reader"}),
			http.StatusOK)
	})

	t.Run("legacy user-scoped endpoints follow the same rules", func(t *testing.T) {
		legacy := "/v1/users/" + newbie + "/projects/" + proj
		expectStatus(t, e.do(http.MethodPut, legacy, tok["member@acme.test"], nil), http.StatusForbidden)
		expectStatus(t, e.do(http.MethodDelete, legacy, tok["orgadmin@acme.test"], nil), http.StatusNoContent)
		expectStatus(t, e.do(http.MethodPut, legacy, tok["orgadmin@acme.test"], nil), http.StatusOK) // no body → member
		if e.memberRole(proj, newbie) != "member" {
			t.Fatalf("legacy grant role = %q, want member", e.memberRole(proj, newbie))
		}
	})
}
