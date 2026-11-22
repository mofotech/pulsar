package identity

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/testutil"
	"github.com/agomez/pulsar/pkg/id"
)

// ── Login (POST /v1/auth/tokens) ────────────────────────────────────────────

func TestLoginIssuesSignedTokenWithClaims(t *testing.T) {
	e := newEnv(t)
	pid := e.createProject(testutil.DefaultOrgID, "p1")
	uid := e.createUser("alice@example.test", userOpts{projectID: pid, orgRole: "org_admin"})

	c := claims(t, e.mustLogin("alice@example.test"))

	for k, want := range map[string]any{
		"sub": uid, "email": "alice@example.test", "project_id": pid,
		"role": "member", "org_id": testutil.DefaultOrgID, "org_role": "org_admin",
	} {
		if c[k] != want {
			t.Errorf("claim %s = %v, want %v", k, c[k], want)
		}
	}
	if jti, _ := c["jti"].(string); jti == "" {
		t.Error("missing jti (needed for revocation)")
	}
	exp, _ := c["exp"].(float64)
	if d := time.Until(time.Unix(int64(exp), 0)); d < 59*time.Minute || d > 61*time.Minute {
		t.Errorf("exp is %v from now, want ~1h (configured expiry)", d)
	}
}

func TestLoginRejectsBadCredentialsUniformly(t *testing.T) {
	e := newEnv(t)
	e.createUser("bob@example.test", userOpts{})

	wrongPw := e.login("bob@example.test", "nope")
	unknown := e.login("nobody@example.test", "nope")
	expectStatus(t, wrongPw, http.StatusUnauthorized)
	expectStatus(t, unknown, http.StatusUnauthorized)
	// Same body for both, so the endpoint doesn't reveal which emails exist.
	strip := func(b []byte) string { return string(b[:strings.Index(string(b), `"meta"`)]) }
	if strip(wrongPw.body) != strip(unknown.body) {
		t.Errorf("responses differ:\n%s\n%s", wrongPw.body, unknown.body)
	}
}

func TestLoginRejectsLegacyDevNoHash(t *testing.T) {
	e := newEnv(t)
	e.createUser("legacy@example.test", userOpts{passwordHash: "dev-nohash"})
	expectStatus(t, e.login("legacy@example.test", "anything"), http.StatusUnauthorized)
	expectStatus(t, e.login("legacy@example.test", ""), http.StatusUnauthorized)
}

func TestLoginRejectsSSOOnlyUserWithEmptyPassword(t *testing.T) {
	e := newEnv(t)
	// Auto-provisioned SSO users are stored with an empty password_hash.
	e.exec(`INSERT INTO users (id, org_id, email, password_hash) VALUES (gen_random_uuid(), $1, 'sso@example.test', '')`,
		testutil.DefaultOrgID)
	expectStatus(t, e.login("sso@example.test", ""), http.StatusUnauthorized)
}

func TestLoginAssignsFirstOrgProjectWhenUserHasNone(t *testing.T) {
	e := newEnv(t)
	org := e.createOrg("acme")
	first := e.createProject(org, "first")
	e.createProject(org, "second")
	uid := e.createUser("new@example.test", userOpts{orgID: org})

	c := claims(t, e.mustLogin("new@example.test"))
	if c["project_id"] != first {
		t.Fatalf("project_id = %v, want first org project %s", c["project_id"], first)
	}
	var home string
	var members int
	ctx := context.Background()
	_ = e.db.Pool().QueryRow(ctx, `SELECT project_id::text FROM users WHERE id = $1`, uid).Scan(&home)
	_ = e.db.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM project_members WHERE user_id = $1 AND project_id = $2`, uid, first).Scan(&members)
	if home != first || members != 1 {
		t.Fatalf("home project = %q, memberships = %d; want %s persisted with membership", home, members, first)
	}
}

// ── JWT revocation (DELETE /v1/auth/tokens) ─────────────────────────────────

func TestLogoutRevokesTokenEverywhere(t *testing.T) {
	e := newEnv(t)
	e.createUser("carol@example.test", userOpts{})
	tok := e.mustLogin("carol@example.test")
	other := e.mustLogin("carol@example.test") // second session

	expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", tok, nil), http.StatusOK)
	expectStatus(t, e.do(http.MethodDelete, "/v1/auth/tokens", tok, nil), http.StatusNoContent)

	r := e.do(http.MethodGet, "/v1/auth/tokens/personal", tok, nil)
	expectStatus(t, r, http.StatusUnauthorized)
	if !strings.Contains(string(r.body), "revoked") {
		t.Errorf("want 'revoked' error, got %s", r.body)
	}
	// Revocation is per-token (jti), not per-user.
	expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", other, nil), http.StatusOK)
}

func TestLogoutWithInvalidTokenIsNoop(t *testing.T) {
	e := newEnv(t)
	expectStatus(t, e.do(http.MethodDelete, "/v1/auth/tokens", "not-a-jwt", nil), http.StatusNoContent)
	expectStatus(t, e.do(http.MethodDelete, "/v1/auth/tokens", "", nil), http.StatusNoContent)
}

// ── Personal access tokens ──────────────────────────────────────────────────

func createPAT(t *testing.T, e *env, jwtTok, name string, expires *time.Time) createPATResponse {
	t.Helper()
	body := map[string]any{"name": name}
	if expires != nil {
		body["expires_at"] = expires.UTC().Format(time.RFC3339)
	}
	r := e.do(http.MethodPost, "/v1/auth/tokens/personal", jwtTok, body)
	expectStatus(t, r, http.StatusCreated)
	var pat createPATResponse
	r.json(t, &pat)
	return pat
}

func TestPATLifecycle(t *testing.T) {
	e := newEnv(t)
	e.createUser("dave@example.test", userOpts{})
	jwtTok := e.mustLogin("dave@example.test")

	pat := createPAT(t, e, jwtTok, "ci", nil)
	if !strings.HasPrefix(pat.Token, "pat_") || len(pat.Token) < 40 {
		t.Fatalf("unexpected token format %q", pat.Token)
	}

	// The raw value is shown once; listings never include it.
	list := e.do(http.MethodGet, "/v1/auth/tokens/personal", jwtTok, nil)
	expectStatus(t, list, http.StatusOK)
	if strings.Contains(string(list.body), pat.Token) || strings.Contains(string(list.body), `"token"`) {
		t.Fatalf("listing leaks token value: %s", list.body)
	}
	// Only the SHA-256 lookup key is stored, never the token itself.
	var sha string
	var bcryptHash *string
	_ = e.db.Pool().QueryRow(context.Background(),
		`SELECT token_sha256, token_hash FROM personal_access_tokens WHERE id = $1`, pat.ID).Scan(&sha, &bcryptHash)
	if sha != api.HashPAT(pat.Token) || bcryptHash != nil {
		t.Fatalf("stored sha256=%q token_hash=%v; want sha256 of token and no bcrypt hash", sha, bcryptHash)
	}

	// The PAT authenticates on its own…
	expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", pat.Token, nil), http.StatusOK)
	// …and stops working once deleted.
	expectStatus(t, e.do(http.MethodDelete, "/v1/auth/tokens/personal/"+pat.ID, jwtTok, nil), http.StatusNoContent)
	expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", pat.Token, nil), http.StatusUnauthorized)
}

func TestPATRejectedWhenExpiredOrForged(t *testing.T) {
	e := newEnv(t)
	e.createUser("erin@example.test", userOpts{})
	jwtTok := e.mustLogin("erin@example.test")

	past := time.Now().Add(-time.Minute)
	expired := createPAT(t, e, jwtTok, "old", &past)
	expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", expired.Token, nil), http.StatusUnauthorized)

	valid := createPAT(t, e, jwtTok, "new", nil)
	forged := valid.Token[:len(valid.Token)-2] + "xx"
	expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", forged, nil), http.StatusUnauthorized)
}

func TestPATCannotBeDeletedByAnotherUser(t *testing.T) {
	e := newEnv(t)
	e.createUser("owner@example.test", userOpts{})
	e.createUser("other@example.test", userOpts{})
	pat := createPAT(t, e, e.mustLogin("owner@example.test"), "mine", nil)

	r := e.do(http.MethodDelete, "/v1/auth/tokens/personal/"+pat.ID, e.mustLogin("other@example.test"), nil)
	expectStatus(t, r, http.StatusNotFound)
	expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", pat.Token, nil), http.StatusOK)
}

func TestPATNameRequired(t *testing.T) {
	e := newEnv(t)
	e.createUser("frank@example.test", userOpts{})
	r := e.do(http.MethodPost, "/v1/auth/tokens/personal", e.mustLogin("frank@example.test"), map[string]string{})
	expectStatus(t, r, http.StatusBadRequest)
}

// ── User creation authorisation (POST /v1/users) ────────────────────────────

func TestCreateUserAuthorization(t *testing.T) {
	e := newEnv(t)
	orgA := e.createOrg("org-a")
	orgB := e.createOrg("org-b")
	projA := e.createProject(orgA, "a1")
	projB := e.createProject(orgB, "b1")
	e.createUser("member@a.test", userOpts{orgID: orgA, projectID: projA})
	e.createUser("admin@a.test", userOpts{orgID: orgA, projectID: projA, orgRole: "org_admin"})
	member := e.mustLogin("member@a.test")
	orgAdmin := e.mustLogin("admin@a.test")

	t.Run("plain member cannot create users", func(t *testing.T) {
		r := e.do(http.MethodPost, "/v1/users", member, map[string]string{"email": "x1@a.test", "password": "pw"})
		expectStatus(t, r, http.StatusForbidden)
	})
	t.Run("org admin creates member in own org", func(t *testing.T) {
		r := e.do(http.MethodPost, "/v1/users", orgAdmin,
			map[string]string{"email": "x2@a.test", "password": "pw", "project_id": projA})
		expectStatus(t, r, http.StatusCreated)
		expectStatus(t, e.login("x2@a.test", "pw"), http.StatusCreated)
	})
	t.Run("org admin cannot create users in another org", func(t *testing.T) {
		r := e.do(http.MethodPost, "/v1/users", orgAdmin,
			map[string]string{"email": "x3@a.test", "password": "pw", "org_id": orgB})
		expectStatus(t, r, http.StatusForbidden)
	})
	t.Run("org admin cannot seed membership in another org's project", func(t *testing.T) {
		r := e.do(http.MethodPost, "/v1/users", orgAdmin,
			map[string]string{"email": "x4@a.test", "password": "pw", "project_id": projB})
		expectStatus(t, r, http.StatusBadRequest)
	})
	t.Run("org admin cannot mint admins", func(t *testing.T) {
		expectStatus(t, e.do(http.MethodPost, "/v1/users", orgAdmin,
			map[string]string{"email": "x5@a.test", "role": "admin"}), http.StatusForbidden)
		expectStatus(t, e.do(http.MethodPost, "/v1/users", orgAdmin,
			map[string]string{"email": "x6@a.test", "org_role": "org_admin"}), http.StatusForbidden)
	})
}

// Regression: the PAT path loaded every unexpired PAT of every user and
// bcrypt-compared each one, so any unauthenticated request carrying a bogus
// "pat_" token cost (number of PATs × bcrypt) CPU. Lookup is now a single
// indexed SHA-256 match, so cost no longer grows with the number of PATs.
func TestPATLookupCostDoesNotGrowWithTokenCount(t *testing.T) {
	e := newEnv(t)
	e.createUser("many@example.test", userOpts{})
	jwtTok := e.mustLogin("many@example.test")
	const n = 25
	for i := 0; i < n; i++ {
		createPAT(t, e, jwtTok, "tok-"+string(rune('a'+i)), nil)
	}

	bogus := "pat_" + strings.Repeat("A", 43)
	start := time.Now()
	const attempts = 3
	for i := 0; i < attempts; i++ {
		expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", bogus, nil), http.StatusUnauthorized)
	}
	// Old behaviour: n bcrypt compares (~50ms+ each at DefaultCost) per request.
	per := time.Since(start) / attempts
	t.Logf("bogus PAT request: %v with %d PATs", per, n)
	if per > 250*time.Millisecond {
		t.Fatalf("bogus PAT request took %v with %d PATs stored; lookup must not scan/bcrypt every token", per, n)
	}
}

// PATs created before migration 010 only have a bcrypt hash. They must keep
// working, and are upgraded to the SHA-256 fast path on first use.
func TestLegacyBcryptPATStillWorksAndIsUpgraded(t *testing.T) {
	e := newEnv(t)
	uid := e.createUser("legacy-pat@example.test", userOpts{})
	raw := "pat_" + strings.Repeat("L", 43)
	h, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	patID := id.New()
	e.exec(`INSERT INTO personal_access_tokens (id, user_id, name, token_hash) VALUES ($1, $2, 'old', $3)`,
		patID, uid, string(h))

	expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", raw, nil), http.StatusOK)

	var sha *string
	var bcryptHash *string
	_ = e.db.Pool().QueryRow(context.Background(),
		`SELECT token_sha256, token_hash FROM personal_access_tokens WHERE id = $1`, patID).Scan(&sha, &bcryptHash)
	if sha == nil || *sha != api.HashPAT(raw) || bcryptHash != nil {
		t.Fatalf("after first use: sha256=%v token_hash=%v; want upgraded to sha256 only", sha, bcryptHash)
	}
	// Works on the fast path afterwards; a wrong token still fails.
	expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", raw, nil), http.StatusOK)
	expectStatus(t, e.do(http.MethodGet, "/v1/auth/tokens/personal", raw[:len(raw)-1]+"X", nil), http.StatusUnauthorized)
}

func TestAuthEventsAreAudited(t *testing.T) {
	e := newEnv(t)
	uid := e.createUser("audited@example.test", userOpts{})

	expectStatus(t, e.login("audited@example.test", "wrong"), http.StatusUnauthorized)
	expectStatus(t, e.login("ghost@example.test", "whatever"), http.StatusUnauthorized)
	tok := e.mustLogin("audited@example.test")
	expectStatus(t, e.do(http.MethodDelete, "/v1/auth/tokens", tok, nil), http.StatusNoContent)

	want := []string{
		"login denied audited@example.test " + uid,
		"login denied ghost@example.test", // unknown user: email recorded, no user id
		"login success audited@example.test " + uid,
		"logout success audited@example.test " + uid,
	}
	got := e.auditTrail()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("audit trail:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var leaks int
	_ = e.db.Pool().QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_events WHERE row_to_json(audit_events)::text LIKE '%pw-audited%'`).Scan(&leaks)
	if leaks != 0 {
		t.Fatal("password appeared in the audit log")
	}
}
