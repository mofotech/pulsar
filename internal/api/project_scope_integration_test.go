package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agomez/pulsar/internal/testutil"
	"github.com/agomez/pulsar/pkg/id"
)

// ProjectScopeMiddleware is the tenant-isolation check for every resource
// route: X-Project-Id must name a project the caller belongs to, in the
// caller's own org.
func TestProjectScopeMiddleware(t *testing.T) {
	db := testutil.Postgres(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	orgA, orgB := id.New(), id.New()
	exec(`INSERT INTO organizations (id, name, slug) VALUES ($1,'a','a'), ($2,'b','b')`, orgA, orgB)
	projA, projA2, projB := id.New(), id.New(), id.New()
	exec(`INSERT INTO projects (id, name, org_id) VALUES ($1,'a1',$4), ($2,'a2',$4), ($3,'b1',$5)`,
		projA, projA2, projB, orgA, orgB)
	user := id.New()
	exec(`INSERT INTO users (id, org_id, email, password_hash, project_id) VALUES ($1,$2,'u@a.test','',$3)`, user, orgA, projA)
	exec(`INSERT INTO project_members (project_id, user_id) VALUES ($1,$2)`, projA, user)
	// Even a (corrupt) membership row in another org's project must not grant access.
	exec(`INSERT INTO project_members (project_id, user_id) VALUES ($1,$2)`, projB, user)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := ProjectScopeMiddleware(db)(next)

	call := func(role, orgID, project string) int {
		c := context.WithValue(ctx, ctxKeyUserID, user)
		c = context.WithValue(c, ctxKeyRole, role)
		c = context.WithValue(c, ctxKeyOrgID, orgID)
		c = context.WithValue(c, ctxKeyProjectID, project)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(c))
		return rec.Code
	}

	cases := []struct {
		name          string
		role, org, pj string
		want          int
	}{
		{"member of own-org project", "member", orgA, projA, http.StatusOK},
		{"own org but not a member", "member", orgA, projA2, http.StatusForbidden},
		{"other org's project (even with membership row)", "member", orgA, projB, http.StatusForbidden},
		{"unknown project", "member", orgA, id.New(), http.StatusForbidden},
		{"platform admin bypasses", "admin", orgA, projB, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := call(tc.role, tc.org, tc.pj); got != tc.want {
				t.Fatalf("status %d, want %d", got, tc.want)
			}
		})
	}
}

// Project roles: readers are read-only on every project-scoped route; the
// caller's role is made available to handlers.
func TestProjectScopeRoles(t *testing.T) {
	db := testutil.Postgres(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	org, proj := id.New(), id.New()
	exec(`INSERT INTO organizations (id, name, slug) VALUES ($1,'o','o')`, org)
	exec(`INSERT INTO projects (id, name, org_id) VALUES ($1,'p',$2)`, proj, org)
	users := map[string]string{}
	for _, role := range []string{"reader", "member", "admin"} {
		u := id.New()
		users[role] = u
		exec(`INSERT INTO users (id, org_id, email, password_hash) VALUES ($1,$2,$3,'')`, u, org, role+"@o.test")
		exec(`INSERT INTO project_members (project_id, user_id, role) VALUES ($1,$2,$3)`, proj, u, role)
	}
	if _, err := db.Pool().Exec(ctx, `INSERT INTO project_members (project_id, user_id, role) VALUES ($1,$2,'owner')`,
		proj, id.New()); err == nil {
		t.Fatal("database accepted an unknown project role")
	}

	var seenRole string
	h := ProjectScopeMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRole = ProjectRoleFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	call := func(user, platformRole, method string) (int, string) {
		c := context.WithValue(ctx, ctxKeyUserID, user)
		c = context.WithValue(c, ctxKeyRole, platformRole)
		c = context.WithValue(c, ctxKeyOrgID, org)
		c = context.WithValue(c, ctxKeyProjectID, proj)
		seenRole = ""
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/", nil).WithContext(c))
		return rec.Code, rec.Body.String()
	}

	for _, m := range []string{http.MethodGet, http.MethodHead} {
		if got, _ := call(users["reader"], "member", m); got != http.StatusOK || seenRole != "reader" {
			t.Errorf("reader %s: %d role=%q, want 200 reader", m, got, seenRole)
		}
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		got, body := call(users["reader"], "member", m)
		if got != http.StatusForbidden || !strings.Contains(body, "PROJECT_READ_ONLY") {
			t.Errorf("reader %s: %d %s, want 403 PROJECT_READ_ONLY", m, got, body)
		}
		for _, role := range []string{"member", "admin"} {
			if got, _ := call(users[role], "member", m); got != http.StatusOK || seenRole != role {
				t.Errorf("%s %s: %d role=%q, want 200", role, m, got, seenRole)
			}
		}
	}
	if got, _ := call(id.New(), "admin", http.MethodDelete); got != http.StatusOK || seenRole != "admin" {
		t.Errorf("platform admin: %d role=%q, want 200 admin", got, seenRole)
	}
}

// One platform-admin rule everywhere (IsPlatformAdmin), and no unchecked
// mutations when there is no active project.
func TestProjectScopePlatformAdminAndNoProject(t *testing.T) {
	db := testutil.Postgres(t)
	ctx := context.Background()
	h := ProjectScopeMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	call := func(role, orgRole, project, method string) (int, string) {
		c := context.WithValue(ctx, ctxKeyUserID, id.New())
		c = context.WithValue(c, ctxKeyRole, role)
		c = context.WithValue(c, ctxKeyOrgRole, orgRole)
		c = context.WithValue(c, ctxKeyOrgID, id.New())
		c = context.WithValue(c, ctxKeyProjectID, project)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/", nil).WithContext(c))
		return rec.Code, rec.Body.String()
	}

	// org_role platform_admin with role member: same access as role admin.
	if got, body := call("member", "platform_admin", id.New(), http.MethodDelete); got != http.StatusOK {
		t.Errorf("org_role platform_admin: %d %s, want 200", got, body)
	}
	// No active project: reads pass (and see nothing), writes are refused.
	if got, _ := call("member", "org_admin", "", http.MethodGet); got != http.StatusOK {
		t.Errorf("no project GET: %d, want 200", got)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if got, body := call("member", "org_admin", "", m); got != http.StatusForbidden || !strings.Contains(body, "PROJECT_REQUIRED") {
			t.Errorf("no project %s: %d %s, want 403 PROJECT_REQUIRED", m, got, body)
		}
	}
	if got, _ := call("admin", "member", "", http.MethodPost); got != http.StatusOK {
		t.Errorf("platform admin without a project: %d, want 200", got)
	}
}
