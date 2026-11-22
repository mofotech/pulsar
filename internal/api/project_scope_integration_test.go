package api

import (
	"context"
	"net/http"
	"net/http/httptest"
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
