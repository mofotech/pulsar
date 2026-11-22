package audit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/store/postgres"
	"github.com/agomez/pulsar/internal/testutil"
	"github.com/agomez/pulsar/pkg/id"
)

const secret = "audit-test-secret-0123456789abcdef0123"

func token(t *testing.T, c jwt.MapClaims) string {
	t.Helper()
	c["exp"] = time.Now().Add(time.Hour).Unix()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func call(t *testing.T, srv *httptest.Server, method, path, tok, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, srv.URL+path, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func events(t *testing.T, db *postgres.DB) []Event {
	t.Helper()
	rows, err := db.Pool().Query(context.Background(), `
		SELECT id, COALESCE(org_id::text,''), COALESCE(project_id::text,''), COALESCE(user_id::text,''),
		       COALESCE(actor_email,''), action, resource_type, COALESCE(resource_id,''), COALESCE(outcome,''),
		       COALESCE(method,''), COALESCE(route,''), COALESCE(path,''), COALESCE(status,0),
		       COALESCE(request_id,''), COALESCE(source_ip,'')
		  FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ProjectID, &e.UserID, &e.ActorEmail, &e.Action, &e.ResourceType,
			&e.ResourceID, &e.Outcome, &e.Method, &e.Route, &e.Path, &e.Status, &e.RequestID, &e.SourceIP); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestMiddlewareRecordsMutations(t *testing.T) {
	db := testutil.Postgres(t)
	rec := NewRecorder(db, zap.NewNop())

	ok := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}
	}
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(rec.Middleware)
	r.Route("/v1", func(r chi.Router) {
		r.Use(api.AuthMiddleware(secret, nil, nil))
		r.Post("/compute/instances", ok(202, `{"data":{"id":"inst-new","status":"pending"}}`))
		r.Get("/compute/instances", ok(200, `{"data":[]}`))
		r.Post("/compute/instances/{id}/resize", ok(200, `{"data":{"id":"inst-1"}}`))
		r.Delete("/compute/instances/{id}", ok(204, ""))
		r.Post("/orgs/{org_id}/idps", ok(201, `{"data":{"id":"idp-9"}}`))
		r.Post("/users", ok(403, `{"error":{"code":"FORBIDDEN","message":"no"}}`))
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	user, org, project := id.New(), id.New(), id.New()
	tok := token(t, jwt.MapClaims{"sub": user, "email": "ada@example.test", "org_id": org, "project_id": project, "role": "member"})

	call(t, srv, "POST", "/v1/compute/instances", tok, `{"name":"vm","user_data":"password: hunter2"}`)
	call(t, srv, "GET", "/v1/compute/instances", tok, "")
	call(t, srv, "POST", "/v1/compute/instances/inst-1/resize", tok, `{"flavor_id":"f"}`)
	call(t, srv, "DELETE", "/v1/compute/instances/inst-1", tok, "")
	call(t, srv, "POST", "/v1/orgs/"+org+"/idps", tok, `{"client_secret":"s3cr3t"}`)
	call(t, srv, "POST", "/v1/users", tok, `{"password":"hunter2"}`)
	call(t, srv, "POST", "/v1/compute/instances", "", `{}`) // unauthenticated
	call(t, srv, "POST", "/v1/nope", tok, `{}`)             // unrouted: 404, not recorded
	rec.Close()

	got := events(t, db)
	want := []struct{ typ, id, action, outcome string }{
		{"compute.instances", "inst-new", "create", Success},
		{"compute.instances", "inst-1", "resize", Success},
		{"compute.instances", "inst-1", "delete", Success},
		{"orgs.idps", "idp-9", "create", Success},
		{"users", "", "create", Denied},
		// Rejected by auth mounted on /v1 before routing finished: described
		// from the matched prefix ("/v1/*" → "api"); the real router mounts
		// auth per area, giving e.g. "compute".
		{"api", "", "create", Denied},
	}
	if len(got) != len(want) {
		t.Fatalf("recorded %d events, want %d (GET and unrouted requests are skipped): %+v", len(got), len(want), got)
	}
	for i, w := range want {
		e := got[i]
		if e.ResourceType != w.typ || e.ResourceID != w.id || e.Action != w.action || e.Outcome != w.outcome {
			t.Errorf("event %d = %s/%s %s %s, want %s/%s %s %s", i, e.ResourceType, e.ResourceID, e.Action, e.Outcome,
				w.typ, w.id, w.action, w.outcome)
		}
	}
	first := got[0]
	if first.UserID != user || first.OrgID != org || first.ProjectID != project || first.ActorEmail != "ada@example.test" ||
		first.Method != "POST" || first.Route != "/v1/compute/instances" || first.Status != 202 ||
		first.RequestID == "" || first.SourceIP != "127.0.0.1" {
		t.Errorf("actor/request detail missing: %+v", first)
	}
	if last := got[len(got)-1]; last.UserID != "" || last.Status != 401 {
		t.Errorf("unauthenticated attempt: %+v", last)
	}

	// Bodies are never stored: no column of any row contains the secrets sent.
	var leaks int
	_ = db.Pool().QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_events WHERE row_to_json(audit_events)::text ~ '(hunter2|s3cr3t)'`).Scan(&leaks)
	if leaks != 0 {
		t.Fatalf("%d audit rows contain request-body secrets", leaks)
	}
}

func TestRecorderFlushesAndSurvivesClose(t *testing.T) {
	db := testutil.Postgres(t)
	rec := NewRecorder(db, zap.NewNop())
	for i := 0; i < 500; i++ {
		rec.Record(Event{Action: "create", ResourceType: "t", Outcome: Success, UserID: "not-a-uuid"})
	}
	rec.Close()
	rec.Record(Event{Action: "late", ResourceType: "t", Outcome: Success}) // after shutdown: written inline
	rec.Close()                                                            // idempotent

	var n, late int
	_ = db.Pool().QueryRow(context.Background(), `SELECT COUNT(*), COUNT(*) FILTER (WHERE action='late') FROM audit_events`).Scan(&n, &late)
	if n != 501 || late != 1 {
		t.Fatalf("persisted %d events (%d late), want 501 (1)", n, late)
	}
}

func TestRetentionDeletesOldEvents(t *testing.T) {
	db := testutil.Postgres(t)
	rec := NewRecorder(db, zap.NewNop())
	rec.Record(Event{Action: "old", ResourceType: "t", Outcome: Success, OccurredAt: time.Now().Add(-400 * 24 * time.Hour)})
	rec.Record(Event{Action: "new", ResourceType: "t", Outcome: Success})
	rec.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec.StartRetention(ctx, 365*24*time.Hour)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var actions []string
		rows, _ := db.Pool().Query(context.Background(), `SELECT action FROM audit_events`)
		for rows.Next() {
			var a string
			_ = rows.Scan(&a)
			actions = append(actions, a)
		}
		rows.Close()
		if len(actions) == 1 && actions[0] == "new" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after retention: %v, want [new]", actions)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAuditQueryAuthorization(t *testing.T) {
	db := testutil.Postgres(t)
	orgA, orgB := id.New(), id.New()
	rec := NewRecorder(db, zap.NewNop())
	for i := 0; i < 5; i++ {
		rec.Record(Event{OrgID: orgA, Action: "create", ResourceType: "compute.instances", Outcome: Success})
	}
	rec.Record(Event{OrgID: orgA, Action: "login", ResourceType: "auth", Outcome: Denied})
	rec.Record(Event{OrgID: orgB, Action: "delete", ResourceType: "network.routers", Outcome: Success})
	rec.Close()

	r := chi.NewRouter()
	r.With(api.AuthMiddleware(secret, nil, nil)).Get("/v1/audit", NewHandler(db, zap.NewNop()).List)
	srv := httptest.NewServer(r)
	defer srv.Close()

	list := func(tok, query string) (int, []Event) {
		status, body := call(t, srv, "GET", "/v1/audit"+query, tok, "")
		var env struct {
			Data []Event `json:"data"`
		}
		_ = json.Unmarshal([]byte(body), &env)
		return status, env.Data
	}
	member := token(t, jwt.MapClaims{"sub": id.New(), "role": "member", "org_role": "member", "org_id": orgA})
	orgAdminA := token(t, jwt.MapClaims{"sub": id.New(), "role": "member", "org_role": "org_admin", "org_id": orgA})
	platform := token(t, jwt.MapClaims{"sub": id.New(), "role": "admin", "org_role": "platform_admin", "org_id": orgA})

	if status, _ := list(member, ""); status != http.StatusForbidden {
		t.Errorf("member: %d, want 403", status)
	}
	if status, _ := list("", ""); status != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", status)
	}
	// Org admins are pinned to their org, even if they ask for another.
	_, evs := list(orgAdminA, "?org_id="+orgB)
	if len(evs) != 6 {
		t.Fatalf("org admin A saw %d events, want their org's 6", len(evs))
	}
	for _, e := range evs {
		if e.OrgID != orgA {
			t.Fatalf("org admin saw another org's event: %+v", e)
		}
	}
	if _, evs := list(platform, ""); len(evs) != 7 {
		t.Errorf("platform admin saw %d, want 7", len(evs))
	}
	if _, evs := list(platform, "?org_id="+orgB); len(evs) != 1 || evs[0].ResourceType != "network.routers" {
		t.Errorf("org filter: %+v", evs)
	}
	if _, evs := list(platform, "?outcome=denied&action=login"); len(evs) != 1 {
		t.Errorf("outcome+action filter: %+v", evs)
	}

	// Newest first, paged with before_id.
	_, page1 := list(platform, "?limit=4")
	if len(page1) != 4 || page1[0].ID < page1[3].ID {
		t.Fatalf("page1 not newest-first: %+v", page1)
	}
	_, page2 := list(platform, "?limit=4&before_id="+strconv.FormatInt(page1[3].ID, 10))
	if len(page2) != 3 || page2[0].ID >= page1[3].ID {
		t.Fatalf("page2: %+v", page2)
	}

	for _, bad := range []string{"?since=yesterday", "?limit=0", "?before_id=x", "?project_id=not-a-uuid"} {
		if status, _ := list(platform, bad); status != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, status)
		}
	}
}

// A handler that panics must still be audited (as a 500), and the panic must
// still reach Recoverer.
func TestMiddlewareRecordsPanics(t *testing.T) {
	db := testutil.Postgres(t)
	rec := NewRecorder(db, zap.NewNop())

	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(rec.Middleware)
	r.Delete("/v1/compute/instances/{id}", func(http.ResponseWriter, *http.Request) {
		panic("half-done delete")
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	if status, _ := call(t, srv, "DELETE", "/v1/compute/instances/inst-1", "", ""); status != 500 {
		t.Fatalf("status %d, want 500 from Recoverer", status)
	}
	rec.Close()

	got := events(t, db)
	if len(got) != 1 {
		t.Fatalf("recorded %d events, want 1: %+v", len(got), got)
	}
	if e := got[0]; e.ResourceType != "compute.instances" || e.ResourceID != "inst-1" ||
		e.Action != "delete" || e.Outcome != Failure || e.Status != 500 {
		t.Errorf("panic event = %+v", e)
	}
}

// Auth routes are recorded by their handlers; any path that doesn't record
// (bad body, internal error, bad SSO state) gets a fallback event instead.
func TestAuthRoutesFallBackWhenHandlerDoesNotRecord(t *testing.T) {
	db := testutil.Postgres(t)
	rec := NewRecorder(db, zap.NewNop())

	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(rec.Middleware)
	r.Post("/v1/auth/tokens", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("recorded") != "" {
			e := RequestEvent(req, "auth", "login", Success)
			e.ActorEmail = "ada@example.test"
			rec.Record(e)
			MarkRecorded(req)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusBadRequest) // malformed body: no explicit record
	})
	r.Delete("/v1/auth/tokens", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // invalid token
	})
	r.Get("/v1/auth/oidc/{idp_slug}/callback", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // bad state
	})
	r.Get("/v1/auth/idps", func(w http.ResponseWriter, _ *http.Request) {}) // a plain read: not audited
	srv := httptest.NewServer(r)
	defer srv.Close()

	call(t, srv, "POST", "/v1/auth/tokens?recorded=1", "", `{}`)
	call(t, srv, "POST", "/v1/auth/tokens", "", `not json`)
	call(t, srv, "DELETE", "/v1/auth/tokens", "", "")
	call(t, srv, "GET", "/v1/auth/oidc/acme/callback?state=x", "", "")
	call(t, srv, "GET", "/v1/auth/idps?email=a@b", "", "")
	rec.Close()

	got := events(t, db)
	want := []struct{ action, outcome, email string }{
		{"login", Success, "ada@example.test"}, // explicit, not duplicated
		{"login", Failure, ""},
		{"logout", Denied, ""},
		{"sso_login", Failure, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("recorded %d events, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if e := got[i]; e.ResourceType != "auth" || e.Action != w.action || e.Outcome != w.outcome || e.ActorEmail != w.email {
			t.Errorf("event %d = %s %s %q, want auth %s %s %q", i, e.Action, e.Outcome, e.ActorEmail, w.action, w.outcome, w.email)
		}
	}
}
