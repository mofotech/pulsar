package quota

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/store/postgres"
)

// WriteIfExceeded writes a 403 QUOTA_EXCEEDED response and returns true if
// err is a quota violation. Resource handlers call it before their generic
// error mapping.
func WriteIfExceeded(w http.ResponseWriter, err error, reqID string) bool {
	var e *ExceededError
	if !errors.As(err, &e) {
		return false
	}
	api.WriteError(w, http.StatusForbidden, "QUOTA_EXCEEDED", e.Error(), reqID)
	return true
}

// Handler serves GET/PUT /v1/projects/{id}/quotas.
type Handler struct {
	db       *postgres.DB
	store    *PGStore
	enforcer *Enforcer
	defaults Limits
	log      *zap.Logger
}

func NewHandler(db *postgres.DB, store *PGStore, enforcer *Enforcer, defaults Limits, log *zap.Logger) *Handler {
	return &Handler{db: db, store: store, enforcer: enforcer, defaults: defaults, log: log}
}

// QuotaEntry is one row of a project's quota report.
type QuotaEntry struct {
	Resource Resource `json:"resource"`
	Limit    int      `json:"limit"` // -1 = unlimited
	Used     int      `json:"used"`
	// Default is true when no per-project override is set.
	Default bool `json:"default"`
}

type quotaReport struct {
	ProjectID string       `json:"project_id"`
	Quotas    []QuotaEntry `json:"quotas"`
}

// Get reports limits and live usage. Visible to project members, their org
// admins, and platform admins.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	projectID := chi.URLParam(r, "id")
	if !h.canView(r, projectID) {
		// 404 rather than 403: don't confirm that other tenants' projects exist.
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "project not found", reqID)
		return
	}
	h.writeReport(w, r, projectID)
}

// Put changes limits: {"vcpus": 40, "instances": -1, "volumes": null}.
// A number sets an override (-1 = unlimited); null reverts to the default.
// Platform admins only — quotas are the provider's control, so org admins
// can't raise their own.
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	if !isPlatformAdmin(r) {
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN", "platform_admin role required to change quotas", reqID)
		return
	}
	projectID := chi.URLParam(r, "id")
	if !h.projectExists(r, projectID) {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "project not found", reqID)
		return
	}
	var body map[string]*int
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "body must be an object of resource: limit", reqID)
		return
	}
	changes := make(map[Resource]*int, len(body))
	for k, v := range body {
		changes[Resource(k)] = v
	}
	if err := h.store.Set(ctx, projectID, changes); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}
	h.log.Info("project quotas changed",
		zap.String("project_id", projectID), zap.String("by", api.UserIDFromContext(ctx)), zap.Any("changes", body))
	h.writeReport(w, r, projectID)
}

func (h *Handler) writeReport(w http.ResponseWriter, r *http.Request, projectID string) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	limits, err := h.store.Limits(ctx, projectID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load quotas", reqID)
		return
	}
	used, err := h.enforcer.Usage(ctx, projectID)
	if err != nil {
		h.log.Error("quota usage", zap.String("project_id", projectID), zap.Error(err))
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to compute usage", reqID)
		return
	}
	overridden := map[Resource]bool{}
	rows, err := h.db.Pool().Query(ctx, `SELECT resource FROM quotas WHERE project_id = $1`, projectID)
	if err == nil {
		for rows.Next() {
			var res string
			if rows.Scan(&res) == nil {
				overridden[Resource(res)] = true
			}
		}
		rows.Close()
	}
	report := quotaReport{ProjectID: projectID, Quotas: make([]QuotaEntry, 0, len(All))}
	for _, res := range All {
		report.Quotas = append(report.Quotas, QuotaEntry{
			Resource: res, Limit: limits[res], Used: used[res], Default: !overridden[res],
		})
	}
	api.WriteJSON(w, http.StatusOK, report, reqID)
}

func isPlatformAdmin(r *http.Request) bool {
	ctx := r.Context()
	return api.RoleFromContext(ctx) == "admin" || api.OrgRoleFromContext(ctx) == "platform_admin"
}

func (h *Handler) projectExists(r *http.Request, projectID string) bool {
	var n int
	err := h.db.Pool().QueryRow(r.Context(), `SELECT COUNT(*) FROM projects WHERE id = $1`, projectID).Scan(&n)
	return err == nil && n == 1
}

func (h *Handler) canView(r *http.Request, projectID string) bool {
	ctx := r.Context()
	if isPlatformAdmin(r) {
		return h.projectExists(r, projectID)
	}
	var n int
	if api.OrgRoleFromContext(ctx) == "org_admin" {
		err := h.db.Pool().QueryRow(ctx,
			`SELECT COUNT(*) FROM projects WHERE id = $1 AND org_id = $2`,
			projectID, api.OrgIDFromContext(ctx)).Scan(&n)
		if err == nil && n == 1 {
			return true
		}
	}
	err := h.db.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM project_members pm JOIN projects p ON p.id = pm.project_id
		  WHERE pm.project_id = $1 AND pm.user_id = $2 AND p.org_id = $3`,
		projectID, api.UserIDFromContext(ctx), api.OrgIDFromContext(ctx)).Scan(&n)
	return err == nil && n == 1
}
