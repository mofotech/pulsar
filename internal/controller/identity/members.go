package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/agomez/pulsar/internal/api"
)

// ProjectMember is one membership row.
type ProjectMember struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"` // reader | member | admin
}

// projectAccess describes what the caller may do with a project's membership.
type projectAccess struct {
	exists     bool
	orgID      string
	canView    bool // any member, or an admin above them
	canManage  bool // project admin, org admin of its org, platform admin
	callerRole string
}

func (h *Handler) projectAccess(ctx context.Context, projectID string) (projectAccess, error) {
	var a projectAccess
	err := h.db.Pool().QueryRow(ctx, `SELECT org_id::text FROM projects WHERE id = $1`, projectID).Scan(&a.orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	a.exists = true

	platform := api.RoleFromContext(ctx) == "admin" || api.OrgRoleFromContext(ctx) == "platform_admin"
	orgAdmin := api.OrgRoleFromContext(ctx) == "org_admin" && api.OrgIDFromContext(ctx) == a.orgID
	err = h.db.Pool().QueryRow(ctx,
		`SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`,
		projectID, api.UserIDFromContext(ctx)).Scan(&a.callerRole)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return a, err
	}
	// Membership only counts within the caller's own org (JWT org claim).
	sameOrg := api.OrgIDFromContext(ctx) == a.orgID
	member := a.callerRole != "" && sameOrg
	a.canView = platform || orgAdmin || member
	a.canManage = platform || orgAdmin || (member && a.callerRole == api.ProjectRoleAdmin)
	return a, nil
}

// ListProjectMembers — GET /v1/projects/{id}/members.
func (h *Handler) ListProjectMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	projectID := chi.URLParam(r, "id")
	a, err := h.projectAccess(ctx, projectID)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid project id", reqID)
		return
	}
	if !a.exists || !a.canView {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "project not found", reqID)
		return
	}
	rows, err := h.db.Pool().Query(ctx,
		`SELECT u.id, u.email, pm.role FROM project_members pm JOIN users u ON u.id = pm.user_id
		  WHERE pm.project_id = $1 ORDER BY u.email`, projectID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list members", reqID)
		return
	}
	defer rows.Close()
	members := []ProjectMember{}
	for rows.Next() {
		var m ProjectMember
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list members", reqID)
			return
		}
		members = append(members, m)
	}
	api.WriteJSON(w, http.StatusOK, members, reqID)
}

// PutProjectMember — PUT /v1/projects/{id}/members/{user_id} {"role": "..."}.
// Adds the user to the project or changes their role.
func (h *Handler) PutProjectMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !api.ValidProjectRole(req.Role) {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "role must be 'reader', 'member' or 'admin'", reqID)
		return
	}
	h.setProjectMember(w, r, chi.URLParam(r, "id"), chi.URLParam(r, "user_id"), req.Role)
}

// DeleteProjectMember — DELETE /v1/projects/{id}/members/{user_id}.
func (h *Handler) DeleteProjectMember(w http.ResponseWriter, r *http.Request) {
	h.removeProjectMember(w, r, chi.URLParam(r, "id"), chi.URLParam(r, "user_id"))
}

func (h *Handler) setProjectMember(w http.ResponseWriter, r *http.Request, projectID, userID, role string) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	a, ok := h.requireManage(w, r, projectID)
	if !ok {
		return
	}
	// Members must belong to the project's org.
	var userOrg string
	if err := h.db.Pool().QueryRow(ctx, `SELECT org_id::text FROM users WHERE id = $1`, userID).Scan(&userOrg); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "user not found", reqID)
		return
	}
	if userOrg != a.orgID {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "user belongs to a different organization", reqID)
		return
	}

	tx, err := h.db.Pool().Begin(ctx)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if role != api.ProjectRoleAdmin {
		if err := guardLastProjectAdmin(ctx, tx, projectID, userID); err != nil {
			api.WriteError(w, http.StatusConflict, "LAST_PROJECT_ADMIN", err.Error(), reqID)
			return
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)
		 ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		projectID, userID, role); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	var m ProjectMember
	_ = h.db.Pool().QueryRow(ctx,
		`SELECT u.id, u.email, pm.role FROM project_members pm JOIN users u ON u.id = pm.user_id
		  WHERE pm.project_id = $1 AND pm.user_id = $2`, projectID, userID).Scan(&m.UserID, &m.Email, &m.Role)
	api.WriteJSON(w, http.StatusOK, m, reqID)
}

func (h *Handler) removeProjectMember(w http.ResponseWriter, r *http.Request, projectID, userID string) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	if _, ok := h.requireManage(w, r, projectID); !ok {
		return
	}
	// A user's home project can't be revoked — they must always have one.
	var home string
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT COALESCE(project_id::text, '') FROM users WHERE id = $1`, userID).Scan(&home); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "user not found", reqID)
		return
	}
	if home == projectID {
		api.WriteError(w, http.StatusConflict, "HOME_PROJECT",
			"cannot revoke access to the user's home project; reassign the home project first", reqID)
		return
	}
	tx, err := h.db.Pool().Begin(ctx)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := guardLastProjectAdmin(ctx, tx, projectID, userID); err != nil {
		api.WriteError(w, http.StatusConflict, "LAST_PROJECT_ADMIN", err.Error(), reqID)
		return
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireManage writes 404 (project hidden) or 403 and returns false unless
// the caller may manage the project's membership.
func (h *Handler) requireManage(w http.ResponseWriter, r *http.Request, projectID string) (projectAccess, bool) {
	reqID := middleware.GetReqID(r.Context())
	a, err := h.projectAccess(r.Context(), projectID)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid project id", reqID)
		return a, false
	}
	if !a.exists || !a.canView {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "project not found", reqID)
		return a, false
	}
	if !a.canManage {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN", "project admin role required", reqID)
		return a, false
	}
	return a, true
}

var errLastProjectAdmin = errors.New("cannot remove or demote the project's last admin; promote another member first")

// guardLastProjectAdmin fails if userID is the project's only admin. It locks
// the project's admin rows so two concurrent demotions can't both pass.
func guardLastProjectAdmin(ctx context.Context, tx pgx.Tx, projectID, userID string) error {
	rows, err := tx.Query(ctx,
		`SELECT user_id::text FROM project_members WHERE project_id = $1 AND role = 'admin' FOR UPDATE`, projectID)
	if err != nil {
		return err
	}
	var admins []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return err
		}
		admins = append(admins, u)
	}
	rows.Close()
	if len(admins) == 1 && admins[0] == userID {
		return errLastProjectAdmin
	}
	return nil
}
