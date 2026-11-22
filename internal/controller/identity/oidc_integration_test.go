package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agomez/pulsar/internal/testutil"
	"github.com/agomez/pulsar/pkg/id"
)

// fakeIDP is a minimal OIDC provider: discovery + a token endpoint that
// returns an id_token carrying whatever claims the test configured.
type fakeIDP struct {
	srv    *httptest.Server
	mu     sync.Mutex
	claims map[string]any
	// tokenEndpoint overrides the token_endpoint advertised in discovery.
	tokenEndpoint string
	hits          atomic.Int32
}

func newFakeIDP(t *testing.T) *fakeIDP {
	f := &fakeIDP{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		tokenEndpoint := f.srv.URL + "/token"
		f.mu.Lock()
		if f.tokenEndpoint != "" {
			tokenEndpoint = f.tokenEndpoint
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint":         tokenEndpoint,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "good-code" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		payload, _ := json.Marshal(f.claims)
		f.mu.Unlock()
		enc := base64.RawURLEncoding.EncodeToString
		idToken := enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + ".sig"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idToken,
		})
	})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIDP) setClaims(c map[string]any) {
	f.mu.Lock()
	f.claims = c
	f.mu.Unlock()
}

// addIDP inserts an IDP row directly (bypassing CreateIDP validation, so tests
// can also model rows written before validation existed).
func (e *env) addIDP(orgID, slug, issuer, defaultRole string, autoProvision bool) string {
	idpID := id.New()
	e.exec(`INSERT INTO identity_providers
	          (id, org_id, name, slug, issuer_url, client_id, client_secret, auto_provision, default_role)
	        VALUES ($1, $2, $3, $3, $4, 'client', 'secret', $5, $6)`,
		idpID, orgID, slug, issuer, autoProvision, defaultRole)
	return idpID
}

// ssoLogin drives authorize → (IDP) → callback and returns the callback response.
func (e *env) ssoLogin(slug, cliRedirect string) resp {
	e.t.Helper()
	path := "/v1/auth/oidc/" + slug + "/authorize"
	if cliRedirect != "" {
		path += "?cli_redirect=" + url.QueryEscape(cliRedirect)
	}
	auth := e.do(http.MethodGet, path, "", nil)
	if auth.status != http.StatusFound {
		return auth
	}
	loc, err := url.Parse(auth.header.Get("Location"))
	if err != nil {
		e.t.Fatal(err)
	}
	state := loc.Query().Get("state")
	return e.do(http.MethodGet, "/v1/auth/oidc/"+slug+"/callback?code=good-code&state="+url.QueryEscape(state), "", nil)
}

// tokenFromRedirect extracts the JWT the callback hands to the UI / CLI.
func tokenFromRedirect(t *testing.T, r resp) (target *url.URL, token string) {
	t.Helper()
	expectStatus(t, r, http.StatusFound)
	u, err := url.Parse(r.header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	tok := u.Query().Get("token")
	if tok == "" {
		t.Fatalf("no token in redirect %s", u)
	}
	return u, tok
}

func TestOIDCAuthorizeRedirectsToIDPWithState(t *testing.T) {
	e := newEnv(t)
	idp := newFakeIDP(t)
	e.addIDP(testutil.DefaultOrgID, "acme", idp.srv.URL, "member", true)

	r := e.do(http.MethodGet, "/v1/auth/oidc/acme/authorize", "", nil)
	expectStatus(t, r, http.StatusFound)
	loc, _ := url.Parse(r.header.Get("Location"))
	if !strings.HasPrefix(loc.String(), idp.srv.URL+"/authorize") {
		t.Fatalf("redirected to %s, want the IDP's authorization_endpoint", loc)
	}
	q := loc.Query()
	if q.Get("client_id") != "client" || q.Get("state") == "" {
		t.Fatalf("missing client_id/state: %s", loc)
	}
	if want := e.srv.URL + "/v1/auth/oidc/acme/callback"; q.Get("redirect_uri") != want {
		t.Errorf("redirect_uri = %s, want %s", q.Get("redirect_uri"), want)
	}
}

func TestOIDCAuthorizeUnknownOrDisabledIDP(t *testing.T) {
	e := newEnv(t)
	idp := newFakeIDP(t)
	idpID := e.addIDP(testutil.DefaultOrgID, "off", idp.srv.URL, "member", true)
	e.exec(`UPDATE identity_providers SET enabled = false WHERE id = $1`, idpID)

	expectStatus(t, e.do(http.MethodGet, "/v1/auth/oidc/nope/authorize", "", nil), http.StatusNotFound)
	expectStatus(t, e.do(http.MethodGet, "/v1/auth/oidc/off/authorize", "", nil), http.StatusNotFound)
}

func TestOIDCCallbackStateIsRequiredAndSingleUse(t *testing.T) {
	e := newEnv(t)
	idp := newFakeIDP(t)
	e.addIDP(testutil.DefaultOrgID, "acme", idp.srv.URL, "member", true)
	e.createProject(testutil.DefaultOrgID, "default")
	idp.setClaims(map[string]any{"sub": "s1", "email": "s1@example.test"})

	expectStatus(t, e.do(http.MethodGet, "/v1/auth/oidc/acme/callback?code=good-code", "", nil), http.StatusBadRequest)

	// A well-formed state that was never issued by authorize.
	forged := base64.RawURLEncoding.EncodeToString([]byte(`{"n":"never-issued"}`))
	expectStatus(t, e.do(http.MethodGet, "/v1/auth/oidc/acme/callback?code=good-code&state="+forged, "", nil),
		http.StatusBadRequest)

	auth := e.do(http.MethodGet, "/v1/auth/oidc/acme/authorize", "", nil)
	loc, _ := url.Parse(auth.header.Get("Location"))
	cb := "/v1/auth/oidc/acme/callback?code=good-code&state=" + url.QueryEscape(loc.Query().Get("state"))
	expectStatus(t, e.do(http.MethodGet, cb, "", nil), http.StatusFound)
	expectStatus(t, e.do(http.MethodGet, cb, "", nil), http.StatusBadRequest) // replay
}

func TestOIDCAutoProvisionsNewUser(t *testing.T) {
	e := newEnv(t)
	org := e.createOrg("acme")
	proj := e.createProject(org, "default")
	idp := newFakeIDP(t)
	idpID := e.addIDP(org, "acme", idp.srv.URL, "member", true)
	idp.setClaims(map[string]any{"sub": "ext-123", "email": "newbie@acme.test"})

	target, tok := tokenFromRedirect(t, e.ssoLogin("acme", ""))
	if target.Path != "/auth/callback" {
		t.Errorf("redirected to %s, want the web UI callback", target.Path)
	}
	c := claims(t, tok)
	if c["org_id"] != org || c["project_id"] != proj || c["role"] != "member" || c["org_role"] != "member" || c["idp_id"] != idpID {
		t.Fatalf("unexpected claims: %v", c)
	}

	// A second login reuses the same account via the federated link.
	_, tok2 := tokenFromRedirect(t, e.ssoLogin("acme", ""))
	if claims(t, tok2)["sub"] != c["sub"] {
		t.Fatal("second SSO login created a different user")
	}
	var users, links int
	_ = e.db.Pool().QueryRow(context.Background(), `SELECT COUNT(*) FROM users WHERE email = 'newbie@acme.test'`).Scan(&users)
	_ = e.db.Pool().QueryRow(context.Background(), `SELECT COUNT(*) FROM federated_identities WHERE idp_id = $1`, idpID).Scan(&links)
	if users != 1 || links != 1 {
		t.Fatalf("users=%d links=%d, want 1/1", users, links)
	}
}

func TestOIDCNoAutoProvisionRejectsUnknownIdentity(t *testing.T) {
	e := newEnv(t)
	idp := newFakeIDP(t)
	e.addIDP(testutil.DefaultOrgID, "strict", idp.srv.URL, "member", false)
	idp.setClaims(map[string]any{"sub": "x", "email": "x@example.test"})
	expectStatus(t, e.ssoLogin("strict", ""), http.StatusForbidden)
}

// Regression: cli_redirect used to accept any URL, so a crafted login link
// delivered the victim's freshly minted JWT to an attacker-controlled host.
func TestOIDCCLIRedirectMustBeLoopback(t *testing.T) {
	e := newEnv(t)
	org := e.createOrg("acme")
	e.createProject(org, "default")
	idp := newFakeIDP(t)
	e.addIDP(org, "acme", idp.srv.URL, "member", true)
	idp.setClaims(map[string]any{"sub": "cli-user", "email": "cli@acme.test"})

	for _, bad := range []string{
		"https://attacker.example/steal",
		"http://attacker.example:8080/cb",
		"http://127.0.0.1.attacker.example:8080/cb",
		"http://user@127.0.0.1:8080/cb",
		"http://127.0.0.1/cb", // no port
		"javascript:alert(1)",
	} {
		r := e.do(http.MethodGet, "/v1/auth/oidc/acme/authorize?cli_redirect="+url.QueryEscape(bad), "", nil)
		if r.status != http.StatusBadRequest {
			t.Errorf("cli_redirect %q: status %d, want 400", bad, r.status)
		}
	}

	target, _ := tokenFromRedirect(t, e.ssoLogin("acme", "http://127.0.0.1:43123/callback"))
	if target.Host != "127.0.0.1:43123" || target.Path != "/callback" {
		t.Fatalf("CLI flow redirected to %s, want the loopback listener", target)
	}
}

// Regression: default_role was unvalidated, so an org admin could create an
// IDP that auto-provisions platform admins.
func TestOIDCDefaultRoleCannotGrantPlatformPrivileges(t *testing.T) {
	e := newEnv(t)
	org := e.createOrg("acme")
	e.createProject(org, "default")
	e.createUser("orgadmin@acme.test", userOpts{orgID: org, orgRole: "org_admin"})
	tok := e.mustLogin("orgadmin@acme.test")
	idp := newFakeIDP(t)

	for _, role := range []string{"platform_admin", "admin", "superuser"} {
		r := e.do(http.MethodPost, "/v1/orgs/"+org+"/idps", tok, map[string]any{
			"name": "evil-" + role, "issuer_url": idp.srv.URL, "client_id": "c", "client_secret": "s", "default_role": role,
		})
		expectStatus(t, r, http.StatusBadRequest)
	}
	ok := e.do(http.MethodPost, "/v1/orgs/"+org+"/idps", tok, map[string]any{
		"name": "Good IdP", "issuer_url": idp.srv.URL, "client_id": "c", "client_secret": "it's-a-secret", "default_role": "org_admin",
	})
	expectStatus(t, ok, http.StatusCreated)
	var created IdentityProvider
	ok.json(t, &created)
	expectStatus(t, e.do(http.MethodPatch, "/v1/orgs/"+org+"/idps/"+created.ID, tok,
		map[string]any{"default_role": "platform_admin"}), http.StatusBadRequest)

	// UpdateIDP: partial update with a quote in the secret (now a bind parameter).
	upd := e.do(http.MethodPatch, "/v1/orgs/"+org+"/idps/"+created.ID, tok,
		map[string]any{"client_secret": "o'neil", "enabled": false})
	expectStatus(t, upd, http.StatusOK)
	var secret, name string
	var enabled bool
	_ = e.db.Pool().QueryRow(context.Background(),
		`SELECT client_secret, name, enabled FROM identity_providers WHERE id = $1`, created.ID).Scan(&secret, &name, &enabled)
	if secret != "o'neil" || name != "Good IdP" || enabled {
		t.Fatalf("after patch: secret=%q name=%q enabled=%v", secret, name, enabled)
	}

	// Defence in depth: a row written before validation existed is clamped.
	e.addIDP(org, "legacy", idp.srv.URL, "platform_admin", true)
	idp.setClaims(map[string]any{"sub": "legacy-1", "email": "legacy1@acme.test"})
	_, sso := tokenFromRedirect(t, e.ssoLogin("legacy", ""))
	c := claims(t, sso)
	if c["role"] != "member" || c["org_role"] != "member" {
		t.Fatalf("provisioned with role=%v org_role=%v, want member/member", c["role"], c["org_role"])
	}
}

// Regression: first SSO login matched existing accounts by email in ANY org
// (ON CONFLICT (email) DO UPDATE SET org_id = ...), letting an IDP that an
// org admin controls take over e.g. a platform admin and move them into the
// attacker's org.
func TestOIDCDoesNotHijackExistingAccounts(t *testing.T) {
	e := newEnv(t)
	attackerOrg := e.createOrg("attacker")
	e.createProject(attackerOrg, "default")
	idp := newFakeIDP(t)
	idpID := e.addIDP(attackerOrg, "attacker", idp.srv.URL, "member", true)

	victimProj := e.createProject(testutil.DefaultOrgID, "ops")
	cases := map[string]userOpts{
		"platform admin in other org": {projectID: victimProj, role: "admin", orgRole: "platform_admin"},
		"member in other org":         {projectID: victimProj},
		"admin in same org":           {orgID: attackerOrg, role: "admin"},
	}
	i := 0
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			i++
			email := "victim" + string(rune('0'+i)) + "@example.test"
			uid := e.createUser(email, opts)
			idp.setClaims(map[string]any{"sub": "attacker-" + email, "email": email})

			expectStatus(t, e.ssoLogin("attacker", ""), http.StatusForbidden)

			var org string
			var links int
			_ = e.db.Pool().QueryRow(context.Background(), `SELECT org_id::text FROM users WHERE id = $1`, uid).Scan(&org)
			_ = e.db.Pool().QueryRow(context.Background(),
				`SELECT COUNT(*) FROM federated_identities WHERE user_id = $1 AND idp_id = $2`, uid, idpID).Scan(&links)
			wantOrg := opts.orgID
			if wantOrg == "" {
				wantOrg = testutil.DefaultOrgID
			}
			if org != wantOrg || links != 0 {
				t.Fatalf("victim org=%s (want %s), federated links=%d (want 0)", org, wantOrg, links)
			}
			expectStatus(t, e.login(email, "pw-"+email), http.StatusCreated) // still their account
		})
	}
}

func TestOIDCLinksExistingMemberInSameOrg(t *testing.T) {
	e := newEnv(t)
	org := e.createOrg("acme")
	proj := e.createProject(org, "default")
	uid := e.createUser("existing@acme.test", userOpts{orgID: org, projectID: proj})
	idp := newFakeIDP(t)
	e.addIDP(org, "acme", idp.srv.URL, "member", true)
	idp.setClaims(map[string]any{"sub": "ext-existing", "email": "existing@acme.test"})

	_, tok := tokenFromRedirect(t, e.ssoLogin("acme", ""))
	if c := claims(t, tok); c["sub"] != uid || c["project_id"] != proj {
		t.Fatalf("claims %v; want existing user %s with home project %s", c, uid, proj)
	}
}

func TestOIDCRejectsIDTokenWithoutSub(t *testing.T) {
	e := newEnv(t)
	idp := newFakeIDP(t)
	e.addIDP(testutil.DefaultOrgID, "acme", idp.srv.URL, "member", true)
	idp.setClaims(map[string]any{"email": "nosub@example.test"})
	expectStatus(t, e.ssoLogin("acme", ""), http.StatusBadGateway)
}

// Regression: slugs were unique only per org, but OIDC routes resolve IDPs by
// slug alone, so a second org naming its IDP "Google" made SSO ambiguous.
func TestIDPSlugsAreGloballyUnique(t *testing.T) {
	e := newEnv(t)
	idp := newFakeIDP(t)
	orgA, orgB := e.createOrg("org-a"), e.createOrg("org-b")
	e.createUser("admin@a.test", userOpts{orgID: orgA, orgRole: "org_admin"})
	e.createUser("admin@b.test", userOpts{orgID: orgB, orgRole: "org_admin"})
	body := map[string]any{"name": "Google", "issuer_url": idp.srv.URL, "client_id": "c", "client_secret": "s"}

	expectStatus(t, e.do(http.MethodPost, "/v1/orgs/"+orgA+"/idps", e.mustLogin("admin@a.test"), body), http.StatusCreated)
	expectStatus(t, e.do(http.MethodPost, "/v1/orgs/"+orgB+"/idps", e.mustLogin("admin@b.test"), body), http.StatusConflict)
}

// Regression (SSRF): the controller fetched any issuer_url an org admin
// configured — from the unauthenticated /authorize endpoint — and echoed the
// fetch error, so it could be used to probe internal services. Non-public
// destinations now need the operator's controller.oidc_allowed_networks.
func TestOIDCIssuerOnInternalNetworkIsBlockedByDefault(t *testing.T) {
	e := newEnvWith(t, nil) // production default: no allowed internal networks
	idp := newFakeIDP(t)    // listens on 127.0.0.1
	e.addIDP(testutil.DefaultOrgID, "internal", idp.srv.URL, "member", true)

	r := e.do(http.MethodGet, "/v1/auth/oidc/internal/authorize", "", nil)
	expectStatus(t, r, http.StatusBadGateway)
	body := string(r.body)
	if !strings.Contains(body, "unreachable or misconfigured") || strings.Contains(body, "127.0.0.1") ||
		strings.Contains(body, "blocked") || strings.Contains(body, "dial") {
		t.Fatalf("response leaks fetch details: %q", body)
	}
	if n := idp.hits.Load(); n != 0 {
		t.Fatalf("controller sent %d request(s) to an internal address", n)
	}
}

// token_endpoint comes from the IdP's discovery document, so an allowed IdP
// must not be able to point the controller's token POST somewhere internal.
func TestOIDCTokenEndpointIsAlsoRestricted(t *testing.T) {
	e := newEnv(t) // 127.0.0.1 allowed for discovery
	org := e.createOrg("acme")
	e.createProject(org, "default")
	idp := newFakeIDP(t)
	idp.tokenEndpoint = "http://169.254.169.254/latest/meta-data/iam"
	e.addIDP(org, "acme", idp.srv.URL, "member", true)
	idp.setClaims(map[string]any{"sub": "s", "email": "s@acme.test"})

	r := e.ssoLogin("acme", "")
	expectStatus(t, r, http.StatusBadGateway)
	if strings.Contains(string(r.body), "169.254") {
		t.Fatalf("response leaks fetch details: %q", r.body)
	}
}

func TestIDPIssuerURLValidatedOnSave(t *testing.T) {
	e := newEnv(t)
	org := e.createOrg("acme")
	e.createUser("admin@acme.test", userOpts{orgID: org, orgRole: "org_admin"})
	tok := e.mustLogin("admin@acme.test")

	for _, bad := range []string{"file:///etc/passwd", "gopher://x", "https://u:p@idp.example", "not a url"} {
		r := e.do(http.MethodPost, "/v1/orgs/"+org+"/idps", tok, map[string]any{
			"name": "x", "issuer_url": bad, "client_id": "c", "client_secret": "s",
		})
		if r.status != http.StatusBadRequest {
			t.Errorf("issuer_url %q: status %d, want 400", bad, r.status)
		}
	}
	ok := e.do(http.MethodPost, "/v1/orgs/"+org+"/idps", tok, map[string]any{
		"name": "Good", "issuer_url": "https://idp.example", "client_id": "c", "client_secret": "s",
	})
	expectStatus(t, ok, http.StatusCreated)
	var p IdentityProvider
	ok.json(t, &p)
	expectStatus(t, e.do(http.MethodPatch, "/v1/orgs/"+org+"/idps/"+p.ID, tok,
		map[string]any{"issuer_url": "file:///etc/passwd"}), http.StatusBadRequest)
}
