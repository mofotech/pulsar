package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/config"
	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/internal/store/postgres"
	"github.com/agomez/pulsar/pkg/id"
)

// revokedTokenKeyPrefix is the etcd namespace for revoked JWT IDs.
// Keys are: /pulsar/auth/revoked/<jti>  →  expiry unix timestamp (as string).
const revokedTokenKeyPrefix = "/pulsar/auth/revoked/" //nolint:gosec // etcd key prefix, not a credential

type Handler struct {
	cfg   *config.Config
	db    *postgres.DB
	store *etcd.Client
	log   *zap.Logger
}

func NewHandler(cfg *config.Config, db *postgres.DB, store *etcd.Client, log *zap.Logger) *Handler {
	return &Handler{cfg: cfg, db: db, store: store, log: log}
}

type tokenRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreateToken validates credentials and issues a signed JWT.
func (h *Handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}

	ctx := r.Context()

	// project_id may be NULL when the user was created before any projects existed.
	// org_id / org_role may be NULL in rows created before the organisations migration.
	// Use COALESCE in the query so we always scan into plain strings.
	var userID, passwordHash, role, orgID, orgRole string
	var projectID *string // nullable

	row := h.db.Pool().QueryRow(ctx,
		`SELECT id, project_id, password_hash,
		        COALESCE(role,     'member'),
		        COALESCE(org_id::text, ''),
		        COALESCE(org_role, 'member')
		   FROM users WHERE email = $1`, req.Email)
	if err := row.Scan(&userID, &projectID, &passwordHash, &role, &orgID, &orgRole); err != nil {
		// Unknown user — return 401 (don't leak whether email exists).
		api.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid credentials", middleware.GetReqID(ctx))
		return
	}

	// Verify bcrypt hash. Legacy dev rows stored "dev-nohash" — accept any
	// password for those so existing dev environments keep working.
	if passwordHash != "dev-nohash" { //nolint:gosec // dev sentinel value, not a real credential
		if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
			api.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid credentials", middleware.GetReqID(ctx))
			return
		}
	}

	// If the user has no home project, auto-assign the first project in their org
	// and persist the assignment so subsequent logins also get it.
	resolvedProjectID := ""
	if projectID != nil && *projectID != "" {
		resolvedProjectID = *projectID
	} else {
		var firstProjectID string
		err := h.db.Pool().QueryRow(ctx,
			`SELECT id FROM projects WHERE org_id = $1 ORDER BY created_at LIMIT 1`, orgID,
		).Scan(&firstProjectID)
		if err == nil && firstProjectID != "" {
			resolvedProjectID = firstProjectID
			// Persist so the user has a home project from now on.
			if _, err := h.db.Pool().Exec(ctx,
				`UPDATE users SET project_id = $1 WHERE id = $2`, firstProjectID, userID,
			); err != nil {
				h.log.Warn("failed to persist auto-assigned project_id", zap.Error(err))
			}
			// Ensure membership in that project.
			if _, err := h.db.Pool().Exec(ctx,
				`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				firstProjectID, userID,
			); err != nil {
				h.log.Warn("failed to seed project_members for auto-assigned project", zap.Error(err))
			}
		}
		// If no projects exist yet (org_admin setting up fresh org) resolvedProjectID stays "".
		// The JWT will carry an empty project_id; ProjectScopeMiddleware allows org_admin through.
	}

	jti := id.New() // unique token ID used for revocation
	expiry := time.Now().Add(h.cfg.Controller.JWT.Expiry)
	claims := jwt.MapClaims{
		"jti":        jti,
		"sub":        userID,
		"email":      req.Email,
		"project_id": resolvedProjectID,
		"role":       role,
		"org_id":     orgID,
		"org_role":   orgRole,
		"exp":        expiry.Unix(),
		"iat":        time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(h.cfg.Controller.JWT.Secret))
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to sign token", middleware.GetReqID(ctx))
		return
	}

	api.WriteJSON(w, http.StatusCreated, tokenResponse{Token: signed, ExpiresAt: expiry}, middleware.GetReqID(ctx))
}

// DeleteToken adds the token's jti to the etcd revocation list so it can no
// longer be used, even before its expiry.
func (h *Handler) DeleteToken(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusNoContent) // already not authenticated — nothing to do
		return
	}
	parts := splitBearer(authHeader)
	if parts == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	token, err := jwt.Parse(parts, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(h.cfg.Controller.JWT.Secret), nil
	})
	if err != nil || !token.Valid {
		// Token already invalid — treat as success.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	jti, _ := claims["jti"].(string)
	if jti == "" {
		// Legacy token without jti — can't revoke precisely; just return 204.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Store the revocation with a TTL equal to the token's remaining lifetime
	// so etcd self-cleans expired entries.
	var ttl int64 = 3600
	if exp, ok := claims["exp"].(float64); ok {
		remaining := int64(exp) - time.Now().Unix()
		if remaining > 0 {
			ttl = remaining
		}
	}

	if err := h.store.PutWithTTL(r.Context(), revokedTokenKeyPrefix+jti, "1", ttl); err != nil {
		h.log.Error("failed to revoke token", zap.String("jti", jti), zap.Error(err))
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to revoke token", reqID)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// splitBearer extracts the token string from a "Bearer <token>" header value.
func splitBearer(header string) string {
	const prefix = "bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return header[len(prefix):]
	}
	return ""
}

// ─── Organizations ────────────────────────────────────────────────────────────

type Organization struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListOrgs returns all organizations. Only platform_admin callers may call this.
func (h *Handler) ListOrgs(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requirePlatformAdmin(w, r, reqID) {
		return
	}
	ctx := r.Context()
	rows, err := h.db.Pool().Query(ctx,
		`SELECT id, name, slug, description, status, is_default, created_at
		   FROM organizations ORDER BY created_at`)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	defer rows.Close()
	orgs := []Organization{}
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Description, &o.Status, &o.IsDefault, &o.CreatedAt); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
			return
		}
		orgs = append(orgs, o)
	}
	api.WriteJSON(w, http.StatusOK, orgs, reqID)
}

// CreateOrg creates a new tenant organization. Only platform_admin callers may call this.
func (h *Handler) CreateOrg(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requirePlatformAdmin(w, r, reqID) {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}
	if req.Name == "" || req.Slug == "" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name and slug are required", reqID)
		return
	}
	ctx := r.Context()
	var o Organization
	err := h.db.Pool().QueryRow(ctx,
		`INSERT INTO organizations (id, name, slug, description)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, name, slug, description, status, is_default, created_at`,
		id.New(), req.Name, req.Slug, req.Description,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Description, &o.Status, &o.IsDefault, &o.CreatedAt)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	api.WriteJSON(w, http.StatusCreated, o, reqID)
}

// GetOrg returns a single organization by ID. Only platform_admin callers may call this.
func (h *Handler) GetOrg(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requirePlatformAdmin(w, r, reqID) {
		return
	}
	ctx := r.Context()
	orgID := chi.URLParam(r, "id")
	var o Organization
	err := h.db.Pool().QueryRow(ctx,
		`SELECT id, name, slug, description, status, is_default, created_at
		   FROM organizations WHERE id = $1`, orgID,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Description, &o.Status, &o.IsDefault, &o.CreatedAt)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "organization not found", reqID)
		return
	}
	api.WriteJSON(w, http.StatusOK, o, reqID)
}

// UpdateOrg renames or updates description/status of an org. Only platform_admin may call this.
// The default org cannot be deleted or suspended via this endpoint.
func (h *Handler) UpdateOrg(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requirePlatformAdmin(w, r, reqID) {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Status      string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}
	ctx := r.Context()
	orgID := chi.URLParam(r, "id")

	// Guard: cannot suspend or modify the status of the default org.
	if req.Status != "" && req.Status != "active" {
		var isDefault bool
		if err := h.db.Pool().QueryRow(ctx,
			`SELECT is_default FROM organizations WHERE id = $1`, orgID,
		).Scan(&isDefault); err == nil && isDefault {
			api.WriteError(w, http.StatusConflict, "DEFAULT_ORG",
				"cannot suspend or deactivate the default organization", reqID)
			return
		}
	}

	var o Organization
	err := h.db.Pool().QueryRow(ctx,
		`UPDATE organizations
		    SET name        = COALESCE(NULLIF($1,''), name),
		        description = COALESCE(NULLIF($2,''), description),
		        status      = COALESCE(NULLIF($3,''), status)
		  WHERE id = $4
		  RETURNING id, name, slug, description, status, is_default, created_at`,
		req.Name, req.Description, req.Status, orgID,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Description, &o.Status, &o.IsDefault, &o.CreatedAt)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "organization not found", reqID)
		return
	}
	api.WriteJSON(w, http.StatusOK, o, reqID)
}

// DeleteOrg removes an org and all its projects/users (cascade). Only platform_admin may call this.
// The default org is protected.
func (h *Handler) DeleteOrg(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requirePlatformAdmin(w, r, reqID) {
		return
	}
	ctx := r.Context()
	orgID := chi.URLParam(r, "id")

	var isDefault bool
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT is_default FROM organizations WHERE id = $1`, orgID,
	).Scan(&isDefault); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "organization not found", reqID)
		return
	}
	if isDefault {
		api.WriteError(w, http.StatusConflict, "DEFAULT_ORG",
			"cannot delete the default organization", reqID)
		return
	}

	if _, err := h.db.Pool().Exec(ctx,
		`DELETE FROM organizations WHERE id = $1`, orgID); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Projects ─────────────────────────────────────────────────────────────────

type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) ListProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)

	// platform_admin may pass ?org_id= to list projects for a specific org.
	filterOrgID := r.URL.Query().Get("org_id")
	if filterOrgID != "" && api.OrgRoleFromContext(ctx) != "platform_admin" {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN",
			"only platform_admin may filter projects by org_id", reqID)
		return
	}

	// Query selection:
	//   platform_admin (role=="admin"): sees everything, optionally filtered by org
	//   org_admin:                      sees all projects in their own org (no project_members join needed)
	//   member:                         sees only projects they are explicitly a member of, within their org
	var (
		query     string
		queryArgs []any
	)
	switch {
	case api.RoleFromContext(ctx) == "admin" && filterOrgID != "":
		query = `SELECT id, name, COALESCE(status, 'active'), created_at FROM projects
		          WHERE org_id = $1 ORDER BY created_at DESC`
		queryArgs = []any{filterOrgID}
	case api.RoleFromContext(ctx) == "admin":
		query = `SELECT id, name, COALESCE(status, 'active'), created_at FROM projects ORDER BY created_at DESC`
	case api.OrgRoleFromContext(ctx) == "org_admin":
		// org_admin sees all projects that belong to their org.
		query = `SELECT id, name, COALESCE(status, 'active'), created_at FROM projects
		          WHERE org_id = $1 ORDER BY created_at DESC`
		queryArgs = []any{api.OrgIDFromContext(ctx)}
	default:
		// Regular member: only projects they are explicitly a member of, within their org.
		query = `SELECT p.id, p.name, COALESCE(p.status, 'active'), p.created_at
		           FROM projects p
		           JOIN project_members pm ON pm.project_id = p.id
		          WHERE pm.user_id = $1
		            AND p.org_id   = $2
		          ORDER BY p.created_at DESC`
		queryArgs = []any{api.UserIDFromContext(ctx), api.OrgIDFromContext(ctx)}
	}

	rows, err := h.db.Pool().Query(ctx, query, queryArgs...)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	defer rows.Close()

	projects := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Status, &p.CreatedAt); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
			return
		}
		projects = append(projects, p)
	}
	api.WriteJSON(w, http.StatusOK, projects, reqID)
}

func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	// org_admin and platform_admin may create projects; regular members may not.
	callerOrgRole := api.OrgRoleFromContext(r.Context())
	if callerOrgRole != "org_admin" && callerOrgRole != "platform_admin" {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN", "org_admin role required", reqID)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	ctx := r.Context()
	orgID := api.OrgIDFromContext(ctx)
	callerUserID := api.UserIDFromContext(ctx)
	var p Project
	err := h.db.Pool().QueryRow(ctx,
		`INSERT INTO projects (id, org_id, name, status) VALUES ($1, $2, $3, 'active')
		 RETURNING id, name, status, created_at`,
		id.New(), orgID, req.Name,
	).Scan(&p.ID, &p.Name, &p.Status, &p.CreatedAt)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(ctx))
		return
	}

	// Seed the creator as a member of the new project.
	if _, err := h.db.Pool().Exec(ctx,
		`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		p.ID, callerUserID,
	); err != nil {
		h.log.Warn("failed to seed project_members for new project creator", zap.Error(err))
	}

	// If the creator has no home project yet, assign this one.
	if _, err := h.db.Pool().Exec(ctx,
		`UPDATE users SET project_id = $1 WHERE id = $2 AND project_id IS NULL`,
		p.ID, callerUserID,
	); err != nil {
		h.log.Warn("failed to auto-assign home project to creator", zap.Error(err))
	}

	api.WriteJSON(w, http.StatusCreated, p, middleware.GetReqID(ctx))
}

func (h *Handler) GetProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := chi.URLParam(r, "id")
	var p Project
	err := h.db.Pool().QueryRow(ctx,
		`SELECT id, name, COALESCE(status, 'active'), created_at FROM projects WHERE id = $1`, projectID,
	).Scan(&p.ID, &p.Name, &p.Status, &p.CreatedAt)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "project not found", middleware.GetReqID(ctx))
		return
	}
	api.WriteJSON(w, http.StatusOK, p, middleware.GetReqID(ctx))
}

func (h *Handler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	// org_admin may only rename projects in their own org; platform_admin may rename any.
	callerOrgRole := api.OrgRoleFromContext(r.Context())
	if callerOrgRole != "org_admin" && callerOrgRole != "platform_admin" {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN", "org_admin role required", reqID)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}
	if req.Name == "" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required", reqID)
		return
	}
	ctx := r.Context()
	projectID := chi.URLParam(r, "id")
	// For org_admin, scope the UPDATE to their org so they can't rename foreign projects.
	orgID := api.OrgIDFromContext(ctx)
	var p Project
	var err error
	if callerOrgRole == "platform_admin" {
		err = h.db.Pool().QueryRow(ctx,
			`UPDATE projects SET name = $1 WHERE id = $2
			 RETURNING id, name, COALESCE(status, 'active'), created_at`,
			req.Name, projectID,
		).Scan(&p.ID, &p.Name, &p.Status, &p.CreatedAt)
	} else {
		err = h.db.Pool().QueryRow(ctx,
			`UPDATE projects SET name = $1 WHERE id = $2 AND org_id = $3
			 RETURNING id, name, COALESCE(status, 'active'), created_at`,
			req.Name, projectID, orgID,
		).Scan(&p.ID, &p.Name, &p.Status, &p.CreatedAt)
	}
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "project not found", reqID)
		return
	}
	api.WriteJSON(w, http.StatusOK, p, reqID)
}

func (h *Handler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	callerOrgRole := api.OrgRoleFromContext(r.Context())
	if callerOrgRole != "org_admin" && callerOrgRole != "platform_admin" {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN", "org_admin role required", reqID)
		return
	}
	ctx := r.Context()
	projectID := chi.URLParam(r, "id")
	orgID := api.OrgIDFromContext(ctx)
	var res interface{}
	var err error
	if callerOrgRole == "platform_admin" {
		_, err = h.db.Pool().Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
	} else {
		// org_admin may only delete projects within their own org.
		var deletedID string
		err = h.db.Pool().QueryRow(ctx,
			`DELETE FROM projects WHERE id = $1 AND org_id = $2 RETURNING id`,
			projectID, orgID,
		).Scan(&deletedID)
		res = deletedID
		_ = res
	}
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "project not found", reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Users ────────────────────────────────────────────────────────────────────

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	ProjectID string    `json:"project_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Admins see all users; members see only users who share at least one project with them.
	var (
		query     string
		queryArgs []any
	)
	if api.RoleFromContext(ctx) == "admin" {
		query = `SELECT id, email,
		                COALESCE(role, 'member'),
		                COALESCE(project_id::text, ''),
		                created_at
		           FROM users ORDER BY created_at DESC`
	} else {
		// Non-platform-admin: only users who share a project with the caller
		// AND belong to the caller's org (prevents cross-org user enumeration).
		query = `SELECT DISTINCT u.id, u.email,
		                COALESCE(u.role, 'member'),
		                COALESCE(u.project_id::text, ''),
		                u.created_at
		           FROM users u
		           JOIN project_members pm ON pm.user_id = u.id
		          WHERE pm.project_id IN (
		                SELECT project_id FROM project_members WHERE user_id = $1
		          )
		            AND u.org_id = $2
		          ORDER BY u.created_at DESC`
		queryArgs = []any{api.UserIDFromContext(ctx), api.OrgIDFromContext(ctx)}
	}

	rows, err := h.db.Pool().Query(ctx, query, queryArgs...)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(ctx))
		return
	}
	defer rows.Close()

	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.ProjectID, &u.CreatedAt); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(ctx))
			return
		}
		users = append(users, u)
	}
	api.WriteJSON(w, http.StatusOK, users, middleware.GetReqID(ctx))
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email     string `json:"email"`
		Password  string `json:"password"`
		Role      string `json:"role"`
		OrgRole   string `json:"org_role"`
		OrgID     string `json:"org_id"`
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}

	callerOrgRole := api.OrgRoleFromContext(r.Context())
	callerOrgID := api.OrgIDFromContext(r.Context())

	// Only platform_admin may create a user with role='admin' (platform-level superuser).
	// org_admin callers always get role='member' regardless of what was requested.
	if req.Role == "admin" && callerOrgRole != "platform_admin" {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN",
			"only platform_admin can create users with role 'admin'", middleware.GetReqID(r.Context()))
		return
	}
	// Clamp to known values to prevent garbage.
	if req.Role != "admin" && req.Role != "member" {
		req.Role = "member"
	}

	// Resolve which org to create the user in.
	// - platform_admin: may supply any org_id, defaults to their own.
	// - org_admin: may only create users in their own org.
	// - member: same, though creating users requires admin anyway.
	orgID := callerOrgID
	if req.OrgID != "" {
		if callerOrgRole != "platform_admin" && req.OrgID != callerOrgID {
			api.WriteError(w, http.StatusForbidden, "FORBIDDEN",
				"you can only create users in your own organization", middleware.GetReqID(r.Context()))
			return
		}
		orgID = req.OrgID
	}

	// Determine the org_role to assign.
	// platform_admin callers may assign org_admin; everyone else gets member.
	orgRole := "member"
	if req.OrgRole != "" {
		if req.OrgRole == "org_admin" && callerOrgRole != "platform_admin" {
			api.WriteError(w, http.StatusForbidden, "FORBIDDEN",
				"only platform_admin can assign org_admin role", middleware.GetReqID(r.Context()))
			return
		}
		if req.OrgRole != "member" && req.OrgRole != "org_admin" {
			api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
				"org_role must be 'member' or 'org_admin'", middleware.GetReqID(r.Context()))
			return
		}
		orgRole = req.OrgRole
	}

	passwordHash := ""
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to hash password", middleware.GetReqID(r.Context()))
			return
		}
		passwordHash = string(hash)
	}

	ctx := r.Context()
	var u User

	// project_id is optional — a user may be created before any projects exist in the org.
	// Use NULL when not supplied so the FK constraint isn't violated.
	var projectIDArg *string
	if req.ProjectID != "" {
		projectIDArg = &req.ProjectID
	}

	err := h.db.Pool().QueryRow(ctx,
		`INSERT INTO users (id, org_id, email, password_hash, role, org_role, project_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, email, COALESCE(role, 'member'), COALESCE(project_id::text, ''), created_at`,
		id.New(), orgID, req.Email, passwordHash, req.Role, orgRole, projectIDArg,
	).Scan(&u.ID, &u.Email, &u.Role, &u.ProjectID, &u.CreatedAt)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(ctx))
		return
	}

	// Seed project_members only when a home project was supplied.
	if req.ProjectID != "" {
		if _, err := h.db.Pool().Exec(ctx,
			`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			req.ProjectID, u.ID,
		); err != nil {
			h.log.Error("failed to seed project_members", zap.Error(err))
		}
	}

	api.WriteJSON(w, http.StatusCreated, u, middleware.GetReqID(ctx))
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requireAdmin(w, r, reqID) {
		return
	}
	var req struct {
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}
	if req.Role != "" && req.Role != "admin" && req.Role != "member" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "role must be 'admin' or 'member'", reqID)
		return
	}
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	if req.Role != "" {
		// Guard: refuse to demote the last admin.
		if req.Role == "member" {
			if err := h.guardLastAdmin(ctx, w, userID, reqID); err != nil {
				return
			}
		}
		if _, err := h.db.Pool().Exec(ctx,
			`UPDATE users SET role = $1 WHERE id = $2`, req.Role, userID); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
			return
		}
	}
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to hash password", reqID)
			return
		}
		if _, err := h.db.Pool().Exec(ctx,
			`UPDATE users SET password_hash = $1 WHERE id = $2`, string(hash), userID); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
			return
		}
	}

	var u User
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT id, email, role, project_id, created_at FROM users WHERE id = $1`, userID,
	).Scan(&u.ID, &u.Email, &u.Role, &u.ProjectID, &u.CreatedAt); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "user not found", reqID)
		return
	}
	api.WriteJSON(w, http.StatusOK, u, reqID)
}

func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requireAdmin(w, r, reqID) {
		return
	}
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	// Guard: refuse to delete the last admin.
	if err := h.guardLastAdmin(ctx, w, userID, reqID); err != nil {
		return
	}

	if _, err := h.db.Pool().Exec(ctx,
		`DELETE FROM users WHERE id = $1`, userID); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Project membership ───────────────────────────────────────────────────────

// GrantProjectAccess adds a user to a project (admin only).
// PUT /v1/users/{id}/projects/{project_id}
func (h *Handler) GrantProjectAccess(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requireAdmin(w, r, reqID) {
		return
	}
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	projectID := chi.URLParam(r, "project_id")

	if _, err := h.db.Pool().Exec(ctx,
		`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		projectID, userID,
	); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RevokeProjectAccess removes a user from a project (admin only).
// DELETE /v1/users/{id}/projects/{project_id}
// Refuses to remove the user's home project (users.project_id).
func (h *Handler) RevokeProjectAccess(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requireAdmin(w, r, reqID) {
		return
	}
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	projectID := chi.URLParam(r, "project_id")

	// Refuse to revoke the user's home project — they must always have one.
	var homeProjectID string
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT COALESCE(project_id::text, '') FROM users WHERE id = $1`, userID,
	).Scan(&homeProjectID); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "user not found", reqID)
		return
	}
	if homeProjectID == projectID {
		api.WriteError(w, http.StatusConflict, "HOME_PROJECT",
			"cannot revoke access to the user's home project; reassign the home project first", reqID)
		return
	}

	if _, err := h.db.Pool().Exec(ctx,
		`DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`,
		projectID, userID,
	); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListUserProjects returns the list of projects a user is a member of (admin only).
// GET /v1/users/{id}/projects
func (h *Handler) ListUserProjects(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	if !requireAdmin(w, r, reqID) {
		return
	}
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	rows, err := h.db.Pool().Query(ctx,
		`SELECT p.id, p.name, p.status, p.created_at
		   FROM projects p
		   JOIN project_members pm ON pm.project_id = p.id
		  WHERE pm.user_id = $1
		  ORDER BY p.name`, userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	defer rows.Close()

	projects := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Status, &p.CreatedAt); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
			return
		}
		projects = append(projects, p)
	}
	api.WriteJSON(w, http.StatusOK, projects, reqID)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// requireAdmin returns true if the request carries an admin JWT, otherwise
// writes a 403 and returns false.
func requireAdmin(w http.ResponseWriter, r *http.Request, reqID string) bool {
	if api.RoleFromContext(r.Context()) != "admin" {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN", "admin role required", reqID)
		return false
	}
	return true
}

// requirePlatformAdmin returns true when the caller's org_role is 'platform_admin'.
// All org_role checks use the claim embedded in the JWT; the legacy 'admin'
// role (stored in the role column) is treated as platform_admin for backwards
// compatibility.
func requirePlatformAdmin(w http.ResponseWriter, r *http.Request, reqID string) bool {
	orgRole := api.OrgRoleFromContext(r.Context())
	if orgRole != "platform_admin" {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN", "platform_admin role required", reqID)
		return false
	}
	return true
}

// guardLastAdmin returns a non-nil error (and writes a 409) when userID is the
// only remaining admin, preventing an accidental lock-out.  Call this before
// any demotion or deletion of a user.
func (h *Handler) guardLastAdmin(ctx context.Context, w http.ResponseWriter, userID, reqID string) error {
	// Is this user currently an admin?
	var currentRole string
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT role FROM users WHERE id = $1`, userID,
	).Scan(&currentRole); err != nil {
		// User not found — nothing to guard.
		return nil
	}
	if currentRole != "admin" {
		return nil // not an admin, no risk
	}

	// Count remaining admins excluding this user.
	var otherAdmins int
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE role = 'admin' AND id != $1`, userID,
	).Scan(&otherAdmins); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return err
	}
	if otherAdmins == 0 {
		err := errors.New("cannot remove the last admin user")
		api.WriteError(w, http.StatusConflict, "LAST_ADMIN", err.Error(), reqID)
		return err
	}
	return nil
}

// ─── Personal Access Tokens ───────────────────────────────────────────────────

// PersonalAccessToken is a long-lived opaque token suitable for CLI / IaC use.
// The raw token value (pat_*) is returned only on creation; afterwards only
// the metadata is accessible.
type PersonalAccessToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type createPATResponse struct {
	PersonalAccessToken
	Token string `json:"token"` // raw value, returned once only
}

// generatePATToken creates a cryptographically random token prefixed with "pat_".
func generatePATToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	raw = "pat_" + base64.RawURLEncoding.EncodeToString(b)
	h, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
	if err != nil {
		return
	}
	hash = string(h)
	return
}

// ListPATs returns all personal access tokens for the authenticated user.
// GET /v1/auth/tokens/personal
func (h *Handler) ListPATs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	userID := api.UserIDFromContext(ctx)

	rows, err := h.db.Pool().Query(ctx,
		`SELECT id, name, last_used_at, expires_at, created_at
		   FROM personal_access_tokens
		  WHERE user_id = $1
		  ORDER BY created_at DESC`, userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	defer rows.Close()

	tokens := []PersonalAccessToken{}
	for rows.Next() {
		var t PersonalAccessToken
		if err := rows.Scan(&t.ID, &t.Name, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
			return
		}
		tokens = append(tokens, t)
	}
	api.WriteJSON(w, http.StatusOK, tokens, reqID)
}

// CreatePAT issues a new personal access token for the authenticated user.
// POST /v1/auth/tokens/personal
func (h *Handler) CreatePAT(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	userID := api.UserIDFromContext(ctx)

	var req struct {
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}
	if req.Name == "" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required", reqID)
		return
	}

	raw, hash, err := generatePATToken()
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to generate token", reqID)
		return
	}

	var t PersonalAccessToken
	err = h.db.Pool().QueryRow(ctx,
		`INSERT INTO personal_access_tokens (id, user_id, name, token_hash, expires_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, name, last_used_at, expires_at, created_at`,
		id.New(), userID, req.Name, hash, req.ExpiresAt,
	).Scan(&t.ID, &t.Name, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}

	api.WriteJSON(w, http.StatusCreated, createPATResponse{PersonalAccessToken: t, Token: raw}, reqID)
}

// DeletePAT revokes a personal access token owned by the authenticated user.
// DELETE /v1/auth/tokens/personal/{id}
func (h *Handler) DeletePAT(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	userID := api.UserIDFromContext(ctx)
	tokenID := chi.URLParam(r, "id")

	ct, err := h.db.Pool().Exec(ctx,
		`DELETE FROM personal_access_tokens WHERE id = $1 AND user_id = $2`,
		tokenID, userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	if ct.RowsAffected() == 0 {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "token not found", reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
