package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/audit"
	"github.com/agomez/pulsar/internal/config"
	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/internal/store/postgres"
	"github.com/agomez/pulsar/internal/testutil"
	"github.com/agomez/pulsar/pkg/id"
)

const testJWTSecret = "integration-test-secret-0123456789abcdef"

// env is a live identity API backed by a fresh Postgres database and etcd.
type env struct {
	t     *testing.T
	db    *postgres.DB
	store *etcd.Client
	cfg   *config.Config
	srv   *httptest.Server
	audit *audit.Recorder
}

// newEnv allows IdP traffic to loopback, where the fake IdPs run.
func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, func(c *config.Config) {
		c.Controller.OIDCAllowedNetworks = []string{"127.0.0.1/32"}
	})
}

// newEnvWith builds an env with production defaults plus mutate.
func newEnvWith(t *testing.T, mutate func(*config.Config)) *env {
	t.Helper()
	db := testutil.Postgres(t)
	store := testutil.Etcd(t)
	e := &env{t: t, db: db, store: store}

	e.cfg = &config.Config{}
	e.cfg.Controller.JWT.Secret = testJWTSecret
	e.cfg.Controller.JWT.Expiry = time.Hour
	if mutate != nil {
		mutate(e.cfg)
	}

	h := NewHandler(e.cfg, db, store, zap.NewNop())
	e.audit = audit.NewRecorder(db, zap.NewNop())
	t.Cleanup(e.audit.Close)
	h.SetAuditRecorder(e.audit)
	auth := api.AuthMiddleware(testJWTSecret, store, db)

	// Mirrors the identity routes in internal/controller/router.go.
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		r.Post("/tokens", h.CreateToken)
		r.Delete("/tokens", h.DeleteToken)
		r.Get("/idps", h.LookupIDPs)
		r.Group(func(r chi.Router) {
			r.Use(auth)
			r.Get("/tokens/personal", h.ListPATs)
			r.Post("/tokens/personal", h.CreatePAT)
			r.Delete("/tokens/personal/{id}", h.DeletePAT)
		})
	})
	r.Route("/v1/orgs/{org_id}/idps", func(r chi.Router) {
		r.Use(auth)
		r.Post("/", h.CreateIDP)
		r.Patch("/{idp_id}", h.UpdateIDP)
	})
	r.Get("/v1/auth/oidc/{idp_slug}/authorize", h.OIDCAuthorize)
	r.Get("/v1/auth/oidc/{idp_slug}/callback", h.OIDCCallback)
	r.Route("/v1/users", func(r chi.Router) {
		r.Use(auth)
		r.Post("/", h.CreateUser)
	})

	e.srv = httptest.NewServer(r)
	t.Cleanup(e.srv.Close)
	// The controller's public URL is what OIDC redirect_uris are built from.
	e.cfg.Controller.PublicURL = e.srv.URL
	return e
}

// ── Fixtures ────────────────────────────────────────────────────────────────

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Pool().Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("exec %q: %v", sql, err)
	}
}

func (e *env) createOrg(name string) string {
	orgID := id.New()
	e.exec(`INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $2)`, orgID, name)
	return orgID
}

func (e *env) createProject(orgID, name string) string {
	pid := id.New()
	e.exec(`INSERT INTO projects (id, name, org_id) VALUES ($1, $2, $3)`, pid, name, orgID)
	return pid
}

type userOpts struct {
	orgID, projectID, role, orgRole, passwordHash string
}

// createUser inserts a user with password "pw-<email>" unless passwordHash is set.
func (e *env) createUser(email string, o userOpts) string {
	e.t.Helper()
	if o.orgID == "" {
		o.orgID = testutil.DefaultOrgID
	}
	if o.role == "" {
		o.role = "member"
	}
	if o.orgRole == "" {
		o.orgRole = "member"
	}
	if o.passwordHash == "" {
		// MinCost keeps tests fast; CompareHashAndPassword reads the cost from the hash.
		h, err := bcrypt.GenerateFromPassword([]byte("pw-"+email), bcrypt.MinCost)
		if err != nil {
			e.t.Fatal(err)
		}
		o.passwordHash = string(h)
	}
	var projectID any
	if o.projectID != "" {
		projectID = o.projectID
	}
	uid := id.New()
	e.exec(`INSERT INTO users (id, org_id, email, password_hash, role, org_role, project_id)
	        VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uid, o.orgID, email, o.passwordHash, o.role, o.orgRole, projectID)
	if o.projectID != "" {
		e.exec(`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2)`, o.projectID, uid)
	}
	return uid
}

// ── HTTP helpers ────────────────────────────────────────────────────────────

type resp struct {
	status int
	header http.Header
	body   []byte
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	// Successful responses use the api.WriteJSON envelope: {"data": ...}.
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(r.body, &env); err == nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, v); err != nil {
			t.Fatalf("decode data: %v\n%s", err, r.body)
		}
		return
	}
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode body: %v\n%s", err, r.body)
	}
}

// noRedirect returns 3xx responses to the caller instead of following them.
var noRedirect = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Timeout:       10 * time.Second,
}

func (e *env) do(method, path, token string, body any) resp {
	e.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	url := path
	if strings.HasPrefix(path, "/") {
		url = e.srv.URL + path
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, rdr)
	if err != nil {
		e.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := noRedirect.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	return resp{status: res.StatusCode, header: res.Header, body: buf.Bytes()}
}

func (e *env) login(email, password string) resp {
	return e.do(http.MethodPost, "/v1/auth/tokens", "", map[string]string{"email": email, "password": password})
}

// mustLogin logs in with the createUser default password and returns the JWT.
func (e *env) mustLogin(email string) string {
	e.t.Helper()
	r := e.login(email, "pw-"+email)
	if r.status != http.StatusCreated {
		e.t.Fatalf("login %s: status %d: %s", email, r.status, r.body)
	}
	var tr tokenResponse
	r.json(e.t, &tr)
	return tr.Token
}

// claims parses a JWT issued by the handler and verifies its signature.
func claims(t *testing.T, token string) jwt.MapClaims {
	t.Helper()
	tok, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return []byte(testJWTSecret), nil })
	if err != nil || !tok.Valid {
		t.Fatalf("token invalid: %v", err)
	}
	return tok.Claims.(jwt.MapClaims)
}

func expectStatus(t *testing.T, r resp, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status = %d, want %d; body: %s", r.status, want, r.body)
	}
}

// auditTrail flushes queued audit events and returns
// "action outcome email user_id" lines in order.
func (e *env) auditTrail() []string {
	e.t.Helper()
	e.audit.Close() // flush; later events are written inline
	rows, err := e.db.Pool().Query(context.Background(), `
		SELECT action, outcome, COALESCE(actor_email,''), COALESCE(user_id::text,'')
		  FROM audit_events WHERE resource_type = 'auth' ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, o, em, u string
		if err := rows.Scan(&a, &o, &em, &u); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, strings.TrimSpace(a+" "+o+" "+em+" "+u))
	}
	return out
}
