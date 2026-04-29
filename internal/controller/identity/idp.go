package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
	"golang.org/x/oauth2"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/pkg/id"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// oidcState is embedded in the OAuth2 state parameter (JSON → base64url).
// It carries a CSRF nonce and an optional CLI loopback redirect URL.
type oidcState struct {
	Nonce       string `json:"n"`
	CLIRedirect string `json:"cli,omitempty"`
}

type IdentityProvider struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	ProviderType  string    `json:"provider_type"`
	IssuerURL     string    `json:"issuer_url"`
	ClientID      string    `json:"client_id"`
	Scopes        string    `json:"scopes"`
	ClaimEmail    string    `json:"claim_email"`
	ClaimName     string    `json:"claim_name"`
	AutoProvision bool      `json:"auto_provision"`
	DefaultRole   string    `json:"default_role"`
	DomainHint    string    `json:"domain_hint"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// ClientSecret is intentionally omitted from JSON responses.
}

// ─── IDP CRUD ─────────────────────────────────────────────────────────────────

// LookupIDPs is a public (unauthenticated) endpoint used by the login page.
// Given an email address it returns the enabled IDPs for that user's org,
// with client_secret omitted. Only name, id, and provider_type are needed by
// the browser to render SSO buttons.
func (h *Handler) LookupIDPs(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	email := r.URL.Query().Get("email")
	if email == "" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "email query param required", reqID)
		return
	}

	ctx := r.Context()

	// Extract the domain part of the email for domain_hint matching.
	domain := ""
	if at := strings.LastIndex(email, "@"); at >= 0 {
		domain = strings.ToLower(email[at+1:])
	}

	// Strategy 1: the user already has an account — return all IDPs for their org.
	var orgID string
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT org_id FROM users WHERE email = $1`, email,
	).Scan(&orgID); err == nil {
		// User found — return all enabled IDPs for their org.
		rows, err := h.db.Pool().Query(ctx,
			`SELECT id, org_id, name, slug, provider_type, issuer_url, client_id,
			        scopes, claim_email, claim_name, auto_provision, default_role,
			        COALESCE(domain_hint,''), enabled, created_at, updated_at
			   FROM identity_providers
			  WHERE org_id = $1 AND enabled = true
			  ORDER BY name`, orgID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
			return
		}
		defer rows.Close()
		idps := []IdentityProvider{}
		for rows.Next() {
			var p IdentityProvider
			if err := rows.Scan(
				&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.ProviderType, &p.IssuerURL, &p.ClientID,
				&p.Scopes, &p.ClaimEmail, &p.ClaimName, &p.AutoProvision, &p.DefaultRole,
				&p.DomainHint, &p.Enabled, &p.CreatedAt, &p.UpdatedAt,
			); err != nil {
				api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
				return
			}
			idps = append(idps, p)
		}
		api.WriteJSON(w, http.StatusOK, idps, reqID)
		return
	}

	// Strategy 2: user not found — match email domain against domain_hint.
	// This covers new users who will be auto-provisioned after SSO.
	if domain == "" {
		api.WriteJSON(w, http.StatusOK, []IdentityProvider{}, reqID)
		return
	}
	rows, err := h.db.Pool().Query(ctx,
		`SELECT id, org_id, name, slug, provider_type, issuer_url, client_id,
		        scopes, claim_email, claim_name, auto_provision, default_role,
		        COALESCE(domain_hint,''), enabled, created_at, updated_at
		   FROM identity_providers
		  WHERE enabled = true
		    AND lower(domain_hint) = $1
		  ORDER BY name`, domain)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	defer rows.Close()
	idps := []IdentityProvider{}
	for rows.Next() {
		var p IdentityProvider
		if err := rows.Scan(
			&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.ProviderType, &p.IssuerURL, &p.ClientID,
			&p.Scopes, &p.ClaimEmail, &p.ClaimName, &p.AutoProvision, &p.DefaultRole,
			&p.DomainHint, &p.Enabled, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
			return
		}
		idps = append(idps, p)
	}
	api.WriteJSON(w, http.StatusOK, idps, reqID)
}

// ListIDPs returns all IDPs for the caller's org.
// Accessible by org_admin and platform_admin.
func (h *Handler) ListIDPs(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requireOrgAdmin(w, r, reqID) {
		return
	}
	ctx := r.Context()
	orgID := chi.URLParam(r, "org_id")
	if !h.callerCanAccessOrg(w, r, orgID, reqID) {
		return
	}

	rows, err := h.db.Pool().Query(ctx,
		`SELECT id, org_id, name, slug, provider_type, issuer_url, client_id,
		        scopes, claim_email, claim_name, auto_provision, default_role,
		        COALESCE(domain_hint,''), enabled, created_at, updated_at
		   FROM identity_providers WHERE org_id = $1 ORDER BY created_at`, orgID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	defer rows.Close()

	idps := []IdentityProvider{}
	for rows.Next() {
		var p IdentityProvider
		if err := rows.Scan(
			&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.ProviderType, &p.IssuerURL, &p.ClientID,
			&p.Scopes, &p.ClaimEmail, &p.ClaimName, &p.AutoProvision, &p.DefaultRole,
			&p.DomainHint, &p.Enabled, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
			return
		}
		idps = append(idps, p)
	}
	api.WriteJSON(w, http.StatusOK, idps, reqID)
}

// CreateIDP registers a new OIDC provider for an org.
func (h *Handler) CreateIDP(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requireOrgAdmin(w, r, reqID) {
		return
	}
	orgID := chi.URLParam(r, "org_id")
	if !h.callerCanAccessOrg(w, r, orgID, reqID) {
		return
	}

	var req struct {
		Name          string `json:"name"`
		IssuerURL     string `json:"issuer_url"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		Scopes        string `json:"scopes"`
		ClaimEmail    string `json:"claim_email"`
		ClaimName     string `json:"claim_name"`
		AutoProvision *bool  `json:"auto_provision"`
		DefaultRole   string `json:"default_role"`
		DomainHint    string `json:"domain_hint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}
	if req.Name == "" || req.IssuerURL == "" || req.ClientID == "" || req.ClientSecret == "" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
			"name, issuer_url, client_id and client_secret are required", reqID)
		return
	}
	if req.Scopes == "" {
		req.Scopes = "openid email profile"
	}
	if req.ClaimEmail == "" {
		req.ClaimEmail = "email"
	}
	if req.ClaimName == "" {
		req.ClaimName = "name"
	}
	if req.DefaultRole == "" {
		req.DefaultRole = "member"
	}
	autoProvision := true
	if req.AutoProvision != nil {
		autoProvision = *req.AutoProvision
	}

	ctx := r.Context()
	var p IdentityProvider
	err := h.db.Pool().QueryRow(ctx,
		`INSERT INTO identity_providers
		   (id, org_id, name, slug, issuer_url, client_id, client_secret,
		    scopes, claim_email, claim_name, auto_provision, default_role, domain_hint)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 RETURNING id, org_id, name, slug, provider_type, issuer_url, client_id,
		           scopes, claim_email, claim_name, auto_provision, default_role,
		           COALESCE(domain_hint,''), enabled, created_at, updated_at`,
		id.New(), orgID, req.Name, slugify(req.Name), req.IssuerURL, req.ClientID, req.ClientSecret,
		req.Scopes, req.ClaimEmail, req.ClaimName, autoProvision, req.DefaultRole, req.DomainHint,
	).Scan(
		&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.ProviderType, &p.IssuerURL, &p.ClientID,
		&p.Scopes, &p.ClaimEmail, &p.ClaimName, &p.AutoProvision, &p.DefaultRole,
		&p.DomainHint, &p.Enabled, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	api.WriteJSON(w, http.StatusCreated, p, reqID)
}

// UpdateIDP patches an existing IDP configuration.
func (h *Handler) UpdateIDP(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requireOrgAdmin(w, r, reqID) {
		return
	}
	orgID := chi.URLParam(r, "org_id")
	if !h.callerCanAccessOrg(w, r, orgID, reqID) {
		return
	}
	idpID := chi.URLParam(r, "idp_id")

	var req struct {
		Name          string `json:"name"`
		IssuerURL     string `json:"issuer_url"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		Scopes        string `json:"scopes"`
		ClaimEmail    string `json:"claim_email"`
		ClaimName     string `json:"claim_name"`
		AutoProvision *bool  `json:"auto_provision"`
		DefaultRole   string `json:"default_role"`
		DomainHint    string `json:"domain_hint"`
		Enabled       *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}

	ctx := r.Context()
	var p IdentityProvider

	// Build update with COALESCE so only non-empty fields are changed.
	// client_secret is only updated when explicitly provided.
	secretExpr := "client_secret"
	if req.ClientSecret != "" {
		secretExpr = fmt.Sprintf("'%s'", strings.ReplaceAll(req.ClientSecret, "'", "''"))
	}
	autoProvision := "auto_provision"
	if req.AutoProvision != nil {
		if *req.AutoProvision {
			autoProvision = "true"
		} else {
			autoProvision = "false"
		}
	}
	enabled := "enabled"
	if req.Enabled != nil {
		if *req.Enabled {
			enabled = "true"
		} else {
			enabled = "false"
		}
	}

	// slug tracks the name — update it whenever name changes.
	slugExpr := "slug"
	if req.Name != "" {
		slugExpr = fmt.Sprintf("'%s'", strings.ReplaceAll(slugify(req.Name), "'", "''"))
	}

	query := fmt.Sprintf(`
		UPDATE identity_providers SET
		  name           = COALESCE(NULLIF($1,''), name),
		  slug           = %s,
		  issuer_url     = COALESCE(NULLIF($2,''), issuer_url),
		  client_id      = COALESCE(NULLIF($3,''), client_id),
		  client_secret  = %s,
		  scopes         = COALESCE(NULLIF($4,''), scopes),
		  claim_email    = COALESCE(NULLIF($5,''), claim_email),
		  claim_name     = COALESCE(NULLIF($6,''), claim_name),
		  auto_provision = %s,
		  default_role   = COALESCE(NULLIF($7,''), default_role),
		  domain_hint    = COALESCE(NULLIF($8,''), domain_hint),
		  enabled        = %s,
		  updated_at     = now()
		WHERE id = $9 AND org_id = $10
		RETURNING id, org_id, name, slug, provider_type, issuer_url, client_id,
		          scopes, claim_email, claim_name, auto_provision, default_role,
		          COALESCE(domain_hint,''), enabled, created_at, updated_at`,
		slugExpr, secretExpr, autoProvision, enabled)

	err := h.db.Pool().QueryRow(ctx, query,
		req.Name, req.IssuerURL, req.ClientID,
		req.Scopes, req.ClaimEmail, req.ClaimName, req.DefaultRole, req.DomainHint,
		idpID, orgID,
	).Scan(
		&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.ProviderType, &p.IssuerURL, &p.ClientID,
		&p.Scopes, &p.ClaimEmail, &p.ClaimName, &p.AutoProvision, &p.DefaultRole,
		&p.DomainHint, &p.Enabled, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "identity provider not found", reqID)
		return
	}
	api.WriteJSON(w, http.StatusOK, p, reqID)
}

// DeleteIDP removes an IDP and all associated federated identity links.
func (h *Handler) DeleteIDP(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requireOrgAdmin(w, r, reqID) {
		return
	}
	orgID := chi.URLParam(r, "org_id")
	if !h.callerCanAccessOrg(w, r, orgID, reqID) {
		return
	}
	idpID := chi.URLParam(r, "idp_id")
	ctx := r.Context()

	if _, err := h.db.Pool().Exec(ctx,
		`DELETE FROM identity_providers WHERE id = $1 AND org_id = $2`, idpID, orgID,
	); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── OIDC flow ────────────────────────────────────────────────────────────────

// oidcStateKeyPrefix is the etcd namespace for short-lived OIDC state tokens.
// Key: /pulsar/auth/oidc-state/<nonce>  →  base64url(JSON oidcState), TTL 5 min.
const oidcStateKeyPrefix = "/pulsar/auth/oidc-state/"

// OIDCAuthorize constructs an OAuth2 authorization URL and redirects the browser.
// GET /v1/auth/oidc/{idp_slug}/authorize
//
// Optional query param:
//
//	cli_redirect=http://localhost:PORT/callback
//	  When present (CLI SSO flow) the token is delivered to that loopback
//	  address instead of the web UI after a successful callback.
func (h *Handler) OIDCAuthorize(w http.ResponseWriter, r *http.Request) {
	idpSlug := chi.URLParam(r, "idp_slug")
	ctx := r.Context()

	// Look up by slug to get the UUID needed for oidcConfig's redirect_uri.
	var idpID, clientID, clientSecret, issuerURL, scopes string
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT id, client_id, client_secret, issuer_url, scopes
		   FROM identity_providers WHERE slug = $1 AND enabled = true`, idpSlug,
	).Scan(&idpID, &clientID, &clientSecret, &issuerURL, &scopes); err != nil {
		http.Error(w, "identity provider not found or disabled", http.StatusNotFound)
		return
	}

	cfg, err := h.oidcConfig(ctx, idpSlug, clientID, clientSecret, issuerURL, scopes)
	if err != nil {
		http.Error(w, "OIDC discovery failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// CSRF state: random 16 bytes → nonce. Store the full oidcState payload in
	// etcd (TTL 5 min) so it survives across the browser redirect without relying
	// on a cookie (which gets dropped by the Vite dev proxy).
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(b)

	statePayload := oidcState{
		Nonce:       nonce,
		CLIRedirect: r.URL.Query().Get("cli_redirect"),
	}
	stateJSON, err := json.Marshal(statePayload)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	stateEncoded := base64.RawURLEncoding.EncodeToString(stateJSON)

	// Persist state in etcd with a 5-minute TTL.
	if err := h.store.PutWithTTL(ctx, oidcStateKeyPrefix+nonce, stateEncoded, 300); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, cfg.AuthCodeURL(stateEncoded, oauth2.AccessTypeOnline), http.StatusFound)
}

// OIDCCallback handles the redirect back from the IDP, exchanges the code for
// tokens, looks up or creates a local user, and issues a Pulsar JWT.
// GET /v1/auth/oidc/{idp_slug}/callback
func (h *Handler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	idpSlug := chi.URLParam(r, "idp_slug")
	ctx := r.Context()

	// Verify CSRF state via etcd lookup.
	// The state param echoed by the IDP is the base64url-encoded oidcState JSON.
	// We decode it to extract the nonce, then verify it exists in etcd.
	stateParam := r.URL.Query().Get("state")
	if stateParam == "" {
		http.Error(w, "missing state parameter", http.StatusBadRequest)
		return
	}
	stateBytes, err := base64.RawURLEncoding.DecodeString(stateParam)
	if err != nil {
		http.Error(w, "invalid state parameter", http.StatusBadRequest)
		return
	}
	var stateParsed oidcState
	if err := json.Unmarshal(stateBytes, &stateParsed); err != nil {
		http.Error(w, "invalid state parameter", http.StatusBadRequest)
		return
	}

	// Look up and immediately delete the nonce from etcd (single-use).
	stored, err := h.store.Get(ctx, oidcStateKeyPrefix+stateParsed.Nonce)
	if err != nil || stored != stateParam {
		http.Error(w, "invalid state parameter", http.StatusBadRequest)
		return
	}
	_ = h.store.Delete(ctx, oidcStateKeyPrefix+stateParsed.Nonce)

	cliRedirect := stateParsed.CLIRedirect

	code := r.URL.Query().Get("code")
	if code == "" {
		errMsg := r.URL.Query().Get("error_description")
		if errMsg == "" {
			errMsg = r.URL.Query().Get("error")
		}
		http.Error(w, "authorization failed: "+errMsg, http.StatusBadRequest)
		return
	}

	// Load IDP config — look up by slug, also retrieve the UUID needed for
	// federated_identities foreign-key references.
	var idpID, clientID, clientSecret, issuerURL, scopes, orgID, claimEmail, claimName, defaultRole string
	var autoProvision bool
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT id, client_id, client_secret, issuer_url, scopes,
		        org_id, claim_email, claim_name, auto_provision, default_role
		   FROM identity_providers WHERE slug = $1 AND enabled = true`, idpSlug,
	).Scan(&idpID, &clientID, &clientSecret, &issuerURL, &scopes,
		&orgID, &claimEmail, &claimName, &autoProvision, &defaultRole); err != nil {
		http.Error(w, "identity provider not found or disabled", http.StatusNotFound)
		return
	}

	cfg, err := h.oidcConfig(ctx, idpSlug, clientID, clientSecret, issuerURL, scopes)
	if err != nil {
		http.Error(w, "OIDC discovery failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	oauthToken, err := cfg.Exchange(ctx, code)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Extract claims from the id_token.
	rawIDToken, _ := oauthToken.Extra("id_token").(string)
	claims, err := parseIDTokenClaims(rawIDToken)
	if err != nil {
		http.Error(w, "failed to parse id_token: "+err.Error(), http.StatusBadGateway)
		return
	}

	externalSub, _ := claims["sub"].(string)
	externalEmail, _ := claims[claimEmail].(string)
	externalName, _ := claims[claimName].(string)
	if externalSub == "" {
		http.Error(w, "id_token missing sub claim", http.StatusBadGateway)
		return
	}

	// Look up existing federated identity link.
	var userID, projectID, userRole, userOrgRole string
	err = h.db.Pool().QueryRow(ctx,
		`SELECT u.id, u.project_id, u.role, u.org_role
		   FROM federated_identities fi
		   JOIN users u ON u.id = fi.user_id
		  WHERE fi.idp_id = $1 AND fi.external_sub = $2`, idpID, externalSub,
	).Scan(&userID, &projectID, &userRole, &userOrgRole)

	if err != nil {
		// No existing link — auto-provision if allowed.
		if !autoProvision {
			http.Error(w, "no account found for this identity; contact your org admin", http.StatusForbidden)
			return
		}
		if externalEmail == "" {
			http.Error(w, "id_token missing email claim; cannot auto-provision", http.StatusBadGateway)
			return
		}

		// Find or create a default project in this org for the new user.
		var defaultProjectID string
		if err := h.db.Pool().QueryRow(ctx,
			`SELECT id FROM projects WHERE org_id = $1 ORDER BY created_at LIMIT 1`, orgID,
		).Scan(&defaultProjectID); err != nil {
			http.Error(w, "org has no projects; cannot auto-provision user", http.StatusInternalServerError)
			return
		}

		// Create the user.
		userID = id.New()
		userRole = defaultRole
		userOrgRole = defaultRole
		if err := h.db.Pool().QueryRow(ctx,
			`INSERT INTO users (id, org_id, email, password_hash, role, org_role, project_id)
			 VALUES ($1, $2, $3, '', $4, $5, $6)
			 ON CONFLICT (email) DO UPDATE SET org_id = EXCLUDED.org_id
			 RETURNING id, project_id, role, org_role`,
			userID, orgID, externalEmail, userRole, userOrgRole, defaultProjectID,
		).Scan(&userID, &projectID, &userRole, &userOrgRole); err != nil {
			http.Error(w, "failed to provision user: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Seed project membership.
		if _, err := h.db.Pool().Exec(ctx,
			`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			projectID, userID,
		); err != nil {
			h.log.Error("failed to seed project_members for federated user", zap.Error(err))
		}

		// Create the federated identity link.
		if _, err := h.db.Pool().Exec(ctx,
			`INSERT INTO federated_identities (id, user_id, idp_id, external_sub, external_email, last_login_at)
			 VALUES ($1, $2, $3, $4, $5, now())`,
			id.New(), userID, idpID, externalSub, externalEmail,
		); err != nil {
			h.log.Error("failed to create federated identity link", zap.Error(err))
		}

		_ = externalName // used for display purposes in future
	} else {
		// Existing user — update last_login_at.
		if _, err := h.db.Pool().Exec(ctx,
			`UPDATE federated_identities SET last_login_at = now()
			  WHERE idp_id = $1 AND external_sub = $2`, idpID, externalSub,
		); err != nil {
			h.log.Error("failed to update last_login_at", zap.Error(err))
		}
	}

	// Issue a Pulsar JWT.
	email := externalEmail
	if email == "" {
		_ = h.db.Pool().QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email)
	}

	jti := id.New()
	expiry := time.Now().Add(h.cfg.Controller.JWT.Expiry)
	pulsarClaims := jwt.MapClaims{
		"jti":        jti,
		"sub":        userID,
		"email":      email,
		"project_id": projectID,
		"role":       userRole,
		"org_id":     orgID,
		"org_role":   userOrgRole,
		"idp_id":     idpID,
		"exp":        expiry.Unix(),
		"iat":        time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, pulsarClaims)
	signed, err := token.SignedString([]byte(h.cfg.Controller.JWT.Secret))
	if err != nil {
		http.Error(w, "failed to issue token", http.StatusInternalServerError)
		return
	}

	// Redirect to the UI with the token as a query parameter.
	// The UI reads it from the URL, stores it, then strips the param.
	// For the CLI SSO flow, redirect to the loopback server instead.
	if cliRedirect != "" {
		http.Redirect(w, r,
			fmt.Sprintf("%s?token=%s&expires_at=%s",
				cliRedirect, signed, expiry.UTC().Format(time.RFC3339)),
			http.StatusFound)
		return
	}
	http.Redirect(w, r,
		fmt.Sprintf("/auth/callback?token=%s&expires_at=%s",
			signed, expiry.UTC().Format(time.RFC3339)),
		http.StatusFound)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// oidcBaseURL returns the externally-reachable base URL for the controller.
func (h *Handler) oidcBaseURL() string {
	if h.cfg.Controller.OIDCBaseURL != "" {
		return strings.TrimRight(h.cfg.Controller.OIDCBaseURL, "/")
	}
	return strings.TrimRight(h.cfg.Controller.PublicURL, "/")
}

// oidcConfig builds an oauth2.Config for the given IDP by fetching the OIDC
// discovery document from {issuerURL}/.well-known/openid-configuration.
// idpSlug is used in the redirect URI so the registered URI is human-readable.
func (h *Handler) oidcConfig(ctx context.Context, idpSlug, clientID, clientSecret, issuerURL, scopes string) (*oauth2.Config, error) {
	disc, err := fetchOIDCDiscovery(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery for %s: %w", issuerURL, err)
	}

	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  fmt.Sprintf("%s/v1/auth/oidc/%s/callback", h.oidcBaseURL(), idpSlug),
		Scopes:       strings.Fields(scopes),
		Endpoint: oauth2.Endpoint{
			AuthURL:  disc.AuthorizationEndpoint,
			TokenURL: disc.TokenEndpoint,
		},
	}, nil
}

// oidcDiscovery holds the relevant subset of an OIDC discovery document.
type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

// fetchOIDCDiscovery retrieves and parses the OIDC discovery document.
func fetchOIDCDiscovery(ctx context.Context, issuerURL string) (*oidcDiscovery, error) {
	discoveryURL := strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery returned HTTP %d", resp.StatusCode)
	}
	var disc oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		return nil, err
	}
	if disc.AuthorizationEndpoint == "" || disc.TokenEndpoint == "" {
		return nil, fmt.Errorf("discovery document missing required endpoints")
	}
	return &disc, nil
}

// parseIDTokenClaims decodes the JWT payload of an id_token without signature
// verification (the IDP's TLS transport already provides authenticity for the
// token exchange response).
func parseIDTokenClaims(rawIDToken string) (map[string]any, error) {
	parts := strings.Split(rawIDToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid id_token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return claims, nil
}

// slugify converts a display name to a URL-safe slug.
// "Acme SSO" → "acme-sso", "My Provider 2!" → "my-provider-2"
var reNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = reNonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// requireOrgAdmin returns true when the caller is an org_admin or platform_admin.
func requireOrgAdmin(w http.ResponseWriter, r *http.Request, reqID string) bool {
	role := api.OrgRoleFromContext(r.Context())
	if role != "org_admin" && role != "platform_admin" {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN", "org_admin role required", reqID)
		return false
	}
	return true
}

// callerCanAccessOrg ensures the caller either is a platform_admin (can access
// any org) or is an org_admin whose JWT org_id matches the target orgID.
func (h *Handler) callerCanAccessOrg(w http.ResponseWriter, r *http.Request, orgID, reqID string) bool {
	ctx := r.Context()
	if api.OrgRoleFromContext(ctx) == "platform_admin" {
		return true
	}
	if api.OrgIDFromContext(ctx) != orgID {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN",
			"you can only manage identity providers for your own organization", reqID)
		return false
	}
	return true
}
