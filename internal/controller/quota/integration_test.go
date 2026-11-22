package quota

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/store/postgres"
	"github.com/agomez/pulsar/internal/testutil"
	"github.com/agomez/pulsar/pkg/id"
)

func seedProject(t *testing.T, db *postgres.DB, orgID string) string {
	t.Helper()
	pid := id.New()
	if _, err := db.Pool().Exec(context.Background(),
		`INSERT INTO projects (id, name, org_id) VALUES ($1, $2, $3)`, pid, "p-"+pid, orgID); err != nil {
		t.Fatal(err)
	}
	return pid
}

func TestPGStoreOverridesDefaults(t *testing.T) {
	db := testutil.Postgres(t)
	ctx := context.Background()
	pid := seedProject(t, db, testutil.DefaultOrgID)
	s := NewPGStore(db, BuiltinDefaults)

	l, err := s.Limits(ctx, pid)
	if err != nil || l[VCPUs] != BuiltinDefaults[VCPUs] {
		t.Fatalf("defaults: %v %v", l, err)
	}
	forty, unlimited := 40, Unlimited
	if err := s.Set(ctx, pid, map[Resource]*int{VCPUs: &forty, Instances: &unlimited}); err != nil {
		t.Fatal(err)
	}
	l, _ = s.Limits(ctx, pid)
	if l[VCPUs] != 40 || l[Instances] != Unlimited || l[Volumes] != BuiltinDefaults[Volumes] {
		t.Fatalf("after set: %v", l)
	}
	// nil reverts to the default.
	if err := s.Set(ctx, pid, map[Resource]*int{VCPUs: nil}); err != nil {
		t.Fatal(err)
	}
	if l, _ = s.Limits(ctx, pid); l[VCPUs] != BuiltinDefaults[VCPUs] {
		t.Fatalf("after reset: %v", l)
	}
	bad := -5
	if err := s.Set(ctx, pid, map[Resource]*int{"gpus": &forty}); err == nil {
		t.Error("unknown resource accepted")
	}
	if err := s.Set(ctx, pid, map[Resource]*int{VCPUs: &bad}); err == nil {
		t.Error("limit < -1 accepted")
	}
}

// The etcd lock must serialise check+commit across concurrent requests (and
// controller replicas): with a limit of 5, exactly 5 of 20 racing creates win.
func TestReserveIsRaceFreeUnderConcurrency(t *testing.T) {
	db := testutil.Postgres(t)
	locker := testutil.Etcd(t)
	ctx := context.Background()
	pid := seedProject(t, db, testutil.DefaultOrgID)
	store := NewPGStore(db, Limits{Instances: 5})

	var mu sync.Mutex
	created := 0
	e := NewEnforcer(store, locker, zap.NewNop())
	e.RegisterUsage(func(context.Context, string) (Usage, error) {
		mu.Lock()
		n := created
		mu.Unlock()
		time.Sleep(5 * time.Millisecond) // widen the read→commit window
		return Usage{Instances: n}, nil
	})

	var ok, exceeded atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := e.Reserve(ctx, pid, Usage{Instances: 1}, func() error {
				mu.Lock()
				created++
				mu.Unlock()
				return nil
			})
			switch {
			case err == nil:
				ok.Add(1)
			case IsExceeded(err):
				exceeded.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 5 || exceeded.Load() != 15 || created != 5 {
		t.Fatalf("ok=%d exceeded=%d created=%d; want 5/15/5", ok.Load(), exceeded.Load(), created)
	}
}

const testSecret = "quota-test-secret-0123456789abcdef0123"

func bearer(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestQuotaAPIAuthorization(t *testing.T) {
	db := testutil.Postgres(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	orgA, orgB := id.New(), id.New()
	exec(`INSERT INTO organizations (id, name, slug) VALUES ($1,'a','a'), ($2,'b','b')`, orgA, orgB)
	projA := seedProject(t, db, orgA)
	member, outsider := id.New(), id.New()
	exec(`INSERT INTO users (id, org_id, email, password_hash) VALUES ($1,$3,'m@a','x'), ($2,$3,'o@a','x')`, member, outsider, orgA)
	exec(`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2)`, projA, member)

	store := NewPGStore(db, BuiltinDefaults)
	e := NewEnforcer(store, nil, zap.NewNop())
	e.RegisterUsage(func(context.Context, string) (Usage, error) { return Usage{Instances: 3, VCPUs: 6}, nil })
	h := NewHandler(db, store, e, BuiltinDefaults, zap.NewNop())
	r := chi.NewRouter()
	r.Use(api.AuthMiddleware(testSecret, nil, db))
	r.Get("/v1/projects/{id}/quotas", h.Get)
	r.Put("/v1/projects/{id}/quotas", h.Put)
	srv := httptest.NewServer(r)
	defer srv.Close()

	call := func(method, tok, body string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, method, srv.URL+"/v1/projects/"+projA+"/quotas", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	memberTok := bearer(t, jwt.MapClaims{"sub": member, "role": "member", "org_role": "member", "org_id": orgA})
	outsiderTok := bearer(t, jwt.MapClaims{"sub": outsider, "role": "member", "org_role": "member", "org_id": orgA})
	orgAdminA := bearer(t, jwt.MapClaims{"sub": id.New(), "role": "member", "org_role": "org_admin", "org_id": orgA})
	orgAdminB := bearer(t, jwt.MapClaims{"sub": id.New(), "role": "member", "org_role": "org_admin", "org_id": orgB})
	platform := bearer(t, jwt.MapClaims{"sub": id.New(), "role": "admin", "org_role": "platform_admin", "org_id": testutil.DefaultOrgID})

	for name, tc := range map[string]struct {
		tok  string
		want int
	}{
		"project member":        {memberTok, 200},
		"same-org non-member":   {outsiderTok, 404},
		"own org admin":         {orgAdminA, 200},
		"other org's org admin": {orgAdminB, 404},
		"platform admin":        {platform, 200},
	} {
		if got, body := call(http.MethodGet, tc.tok, ""); got != tc.want {
			t.Errorf("GET as %s: %d, want %d (%s)", name, got, tc.want, body)
		}
	}

	// The report carries limits and live usage.
	_, body := call(http.MethodGet, memberTok, "")
	var env struct {
		Data quotaReport `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	byRes := map[Resource]QuotaEntry{}
	for _, q := range env.Data.Quotas {
		byRes[q.Resource] = q
	}
	if q := byRes[VCPUs]; q.Limit != 20 || q.Used != 6 || !q.Default || len(env.Data.Quotas) != len(All) {
		t.Fatalf("report: %+v", env.Data)
	}

	// Only platform admins can change limits; org admins can't raise their own.
	for _, tok := range []string{memberTok, orgAdminA} {
		if got, _ := call(http.MethodPut, tok, `{"vcpus": 1000}`); got != http.StatusForbidden {
			t.Errorf("PUT by non-platform-admin: %d, want 403", got)
		}
	}
	got, body := call(http.MethodPut, platform, `{"vcpus": 64, "instances": -1}`)
	if got != 200 || !strings.Contains(body, `"resource":"vcpus","limit":64,"used":6,"default":false`) {
		t.Fatalf("PUT by platform admin: %d %s", got, body)
	}
	if got, _ := call(http.MethodPut, platform, `{"gpus": 1}`); got != http.StatusBadRequest {
		t.Errorf("unknown resource: %d, want 400", got)
	}
}
