package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"golang.org/x/oauth2"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/audit"
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
	if err := validateIssuerURL(req.IssuerURL); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
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
	if !allowedDefaultRoles[req.DefaultRole] {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", errInvalidDefaultRole.Error(), reqID)
		return
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
	if isUniqueViolation(err) {
		api.WriteError(w, http.StatusConflict, "CONFLICT",
			"an identity provider with this name already exists; choose a different name", reqID)
		return
	}
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

	if req.DefaultRole != "" && !allowedDefaultRoles[req.DefaultRole] {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", errInvalidDefaultRole.Error(), reqID)
		return
	}
	if req.IssuerURL != "" {
		if err := validateIssuerURL(req.IssuerURL); err != nil {
			api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
			return
		}
	}

	ctx := r.Context()
	var p IdentityProvider

	// Only non-empty / non-nil fields are changed. Every value is a bind
	// parameter; slug tracks the name whenever the name changes.
	slug := ""
	if req.Name != "" {
		slug = slugify(req.Name)
	}
	const query = `
		UPDATE identity_providers SET
		  name           = COALESCE(NULLIF($1,''), name),
		  slug           = COALESCE(NULLIF($2,''), slug),
		  issuer_url     = COALESCE(NULLIF($3,''), issuer_url),
		  client_id      = COALESCE(NULLIF($4,''), client_id),
		  client_secret  = COALESCE(NULLIF($5,''), client_secret),
		  scopes         = COALESCE(NULLIF($6,''), scopes),
		  claim_email    = COALESCE(NULLIF($7,''), claim_email),
		  claim_name     = COALESCE(NULLIF($8,''), claim_name),
		  auto_provision = COALESCE($9::boolean, auto_provision),
		  default_role   = COALESCE(NULLIF($10,''), default_role),
		  domain_hint    = COALESCE(NULLIF($11,''), domain_hint),
		  enabled        = COALESCE($12::boolean, enabled),
		  updated_at     = now()
		WHERE id = $13 AND org_id = $14
		RETURNING id, org_id, name, slug, provider_type, issuer_url, client_id,
		          scopes, claim_email, claim_name, auto_provision, default_role,
		          COALESCE(domain_hint,''), enabled, created_at, updated_at`

	err := h.db.Pool().QueryRow(ctx, query,
		req.Name, slug, req.IssuerURL, req.ClientID, req.ClientSecret,
		req.Scopes, req.ClaimEmail, req.ClaimName, req.AutoProvision,
		req.DefaultRole, req.DomainHint, req.Enabled,
		idpID, orgID,
	).Scan(
		&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.ProviderType, &p.IssuerURL, &p.ClientID,
		&p.Scopes, &p.ClaimEmail, &p.ClaimName, &p.AutoProvision, &p.DefaultRole,
		&p.DomainHint, &p.Enabled, &p.CreatedAt, &p.UpdatedAt,
	)
	if isUniqueViolation(err) {
		api.WriteError(w, http.StatusConflict, "CONFLICT",
			"an identity provider with this name already exists; choose a different name", reqID)
		return
	}
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

	// The callback hands the freshly minted JWT to cli_redirect, so it must
	// be the CLI's loopback listener — never an arbitrary URL.
	cliRedirect := r.URL.Query().Get("cli_redirect")
	if cliRedirect != "" {
		if err := validateCLIRedirect(cliRedirect); err != nil {
			http.Error(w, "invalid cli_redirect: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	cfg, err := h.oidcConfig(ctx, idpSlug, clientID, clientSecret, issuerURL, scopes)
	if err != nil {
		// Details stay in the log: this endpoint is unauthenticated, and echoing
		// fetch errors would map the controller's network for a caller.
		h.log.Warn("OIDC discovery failed", zap.String("idp", idpSlug), zap.Error(err))
		http.Error(w, errIDPUnavailable, http.StatusBadGateway)
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
		CLIRedirect: cliRedirect,
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
	if cliRedirect != "" {
		if err := validateCLIRedirect(cliRedirect); err != nil {
			http.Error(w, "invalid state parameter", http.StatusBadRequest)
			return
		}
	}

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
		h.log.Warn("OIDC discovery failed", zap.String("idp", idpSlug), zap.Error(err))
		http.Error(w, errIDPUnavailable, http.StatusBadGateway)
		return
	}
	// token_endpoint comes from the (IdP-controlled) discovery document, so the
	// exchange must use the same restricted client.
	oauthToken, err := cfg.Exchange(context.WithValue(ctx, oauth2.HTTPClient, h.issuerHTTP), code)
	if err != nil {
		h.log.Warn("OIDC token exchange failed", zap.String("idp", idpSlug), zap.Error(err))
		http.Error(w, "token exchange with the identity provider failed", http.StatusBadGateway)
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

	userID, projectID, userRole, userOrgRole, status, msg := h.resolveFederatedUser(
		ctx, idpID, orgID, externalSub, externalEmail, autoProvision, defaultRole)
	if status != 0 {
		outcome := audit.Failure
		if status == http.StatusForbidden {
			outcome = audit.Denied
		}
		h.recordAuth(r, "sso_login", outcome, "", orgID, externalEmail)
		http.Error(w, msg, status)
		return
	}
	_ = externalName // used for display purposes in future

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

	h.recordAuth(r, "sso_login", audit.Success, userID, orgID, email)

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

// resolveFederatedUser maps an IDP identity to a local user, linking or
// auto-provisioning as allowed. On failure it returns a non-zero HTTP status
// and message. Security rules:
//
//   - An existing federated link always wins.
//   - An unlinked identity whose email matches an existing account is linked
//     only if that account is in the IDP's own org and is not a platform-level
//     admin. Otherwise any org admin could configure an IDP that asserts
//     someone else's email and log in as them.
//   - The user's org is never changed, and auto-provisioned users get
//     role=member and an org_role limited to member/org_admin.
func (h *Handler) resolveFederatedUser(
	ctx context.Context, idpID, idpOrgID, externalSub, externalEmail string,
	autoProvision bool, defaultRole string,
) (userID, projectID, role, orgRole string, status int, msg string) {
	err := h.db.Pool().QueryRow(ctx,
		`SELECT u.id, COALESCE(u.project_id::text, ''), u.role, u.org_role
		   FROM federated_identities fi
		   JOIN users u ON u.id = fi.user_id
		  WHERE fi.idp_id = $1 AND fi.external_sub = $2`, idpID, externalSub,
	).Scan(&userID, &projectID, &role, &orgRole)
	if err == nil {
		if _, err := h.db.Pool().Exec(ctx,
			`UPDATE federated_identities SET last_login_at = now()
			  WHERE idp_id = $1 AND external_sub = $2`, idpID, externalSub,
		); err != nil {
			h.log.Error("failed to update last_login_at", zap.Error(err))
		}
		return userID, projectID, role, orgRole, 0, ""
	}

	if !autoProvision {
		return "", "", "", "", http.StatusForbidden, "no account found for this identity; contact your org admin"
	}
	if externalEmail == "" {
		return "", "", "", "", http.StatusBadGateway, "id_token missing email claim; cannot auto-provision"
	}

	// An account with this email already exists but is not linked to this IDP.
	var existingOrgID string
	err = h.db.Pool().QueryRow(ctx,
		`SELECT id, org_id::text, COALESCE(project_id::text, ''), role, org_role
		   FROM users WHERE email = $1`, externalEmail,
	).Scan(&userID, &existingOrgID, &projectID, &role, &orgRole)
	if err == nil {
		if existingOrgID != idpOrgID || role == "admin" || orgRole == "platform_admin" {
			h.log.Warn("refusing to link SSO identity to existing account",
				zap.String("idp_id", idpID), zap.String("user_id", userID),
				zap.Bool("cross_org", existingOrgID != idpOrgID))
			return "", "", "", "", http.StatusForbidden,
				"an account with this email already exists; ask an administrator to link it"
		}
		if _, err := h.db.Pool().Exec(ctx,
			`INSERT INTO federated_identities (id, user_id, idp_id, external_sub, external_email, last_login_at)
			 VALUES ($1, $2, $3, $4, $5, now())`,
			id.New(), userID, idpID, externalSub, externalEmail,
		); err != nil {
			return "", "", "", "", http.StatusInternalServerError, "failed to link identity"
		}
		return userID, projectID, role, orgRole, 0, ""
	}

	// New user: home project is the org's first project.
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT id FROM projects WHERE org_id = $1 ORDER BY created_at LIMIT 1`, idpOrgID,
	).Scan(&projectID); err != nil {
		return "", "", "", "", http.StatusInternalServerError, "org has no projects; cannot auto-provision user"
	}
	role = "member"
	orgRole = defaultRole
	if !allowedDefaultRoles[orgRole] {
		// Rows written before default_role was validated may hold anything.
		h.log.Warn("ignoring invalid IDP default_role", zap.String("idp_id", idpID), zap.String("default_role", defaultRole))
		orgRole = "member"
	}
	userID = id.New()
	if _, err := h.db.Pool().Exec(ctx,
		`INSERT INTO users (id, org_id, email, password_hash, role, org_role, project_id)
		 VALUES ($1, $2, $3, '', $4, $5, $6)`,
		userID, idpOrgID, externalEmail, role, orgRole, projectID,
	); err != nil {
		return "", "", "", "", http.StatusInternalServerError, "failed to provision user"
	}
	if _, err := h.db.Pool().Exec(ctx,
		`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		projectID, userID,
	); err != nil {
		h.log.Error("failed to seed project_members for federated user", zap.Error(err))
	}
	if _, err := h.db.Pool().Exec(ctx,
		`INSERT INTO federated_identities (id, user_id, idp_id, external_sub, external_email, last_login_at)
		 VALUES ($1, $2, $3, $4, $5, now())`,
		id.New(), userID, idpID, externalSub, externalEmail,
	); err != nil {
		h.log.Error("failed to create federated identity link", zap.Error(err))
	}
	return userID, projectID, role, orgRole, 0, ""
}

// isUniqueViolation reports a Postgres unique_violation (23505), e.g. a slug
// that another org's IDP already uses.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// allowedDefaultRoles are the org_role values an IDP may grant to the users it
// auto-provisions. platform_admin and the legacy role 'admin' are never
// grantable through an IDP an org admin controls.
var allowedDefaultRoles = map[string]bool{"member": true, "org_admin": true}

var errInvalidDefaultRole = errors.New("default_role must be 'member' or 'org_admin'")

// errIDPUnavailable is the only detail unauthenticated callers get when the
// controller can't reach or parse an IdP; the cause is logged.
const errIDPUnavailable = "identity provider is unreachable or misconfigured; contact your administrator"

// validateCLIRedirect accepts only http URLs on a loopback host with an
// explicit port — the shape of pulsarctl's local callback listener.
func validateCLIRedirect(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("not a valid URL")
	}
	if u.Scheme != "http" {
		return errors.New("must use http")
	}
	if u.User != nil {
		return errors.New("must not contain credentials")
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return errors.New("must point to a loopback address (127.0.0.1, localhost or ::1)")
	}
	if u.Port() == "" {
		return errors.New("must include a port")
	}
	return nil
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
	disc, err := fetchOIDCDiscovery(ctx, h.issuerHTTP, issuerURL)
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
func fetchOIDCDiscovery(ctx context.Context, client *http.Client, issuerURL string) (*oidcDiscovery, error) {
	discoveryURL := strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery returned HTTP %d", resp.StatusCode)
	}
	var disc oidcDiscovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&disc); err != nil {
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
