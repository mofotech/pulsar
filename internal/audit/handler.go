package audit

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/store/postgres"
)

// Handler serves GET /v1/audit.
type Handler struct {
	db  *postgres.DB
	log *zap.Logger
}

func NewHandler(db *postgres.DB, log *zap.Logger) *Handler {
	return &Handler{db: db, log: log}
}

const maxLimit = 1000

// List returns events newest first. Platform admins see everything; org
// admins see their own org only; everyone else gets 403.
//
// Query params (all optional): org_id, project_id, user_id, resource_type,
// resource_id, action, outcome, since, until (RFC 3339), limit (≤1000,
// default 100), before_id (page backwards from an event id).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.GetReqID(ctx)
	q := r.URL.Query()

	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}

	platform := api.RoleFromContext(ctx) == "admin" || api.OrgRoleFromContext(ctx) == "platform_admin"
	switch {
	case platform:
		if v := q.Get("org_id"); v != "" {
			add("org_id = $%d", v)
		}
	case api.OrgRoleFromContext(ctx) == "org_admin":
		// Pinned to the caller's org regardless of any org_id param.
		add("org_id = $%d", api.OrgIDFromContext(ctx))
	default:
		api.WriteError(w, http.StatusForbidden, "FORBIDDEN", "org_admin or platform_admin role required", reqID)
		return
	}

	for _, col := range []string{"project_id", "user_id", "resource_type", "resource_id", "action", "outcome"} {
		if v := q.Get(col); v != "" {
			add(col+" = $%d", v)
		}
	}
	for param, op := range map[string]string{"since": ">=", "until": "<"} {
		if v := q.Get(param); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", param+" must be RFC 3339, e.g. 2026-11-01T00:00:00Z", reqID)
				return
			}
			add("occurred_at "+op+" $%d", t)
		}
	}
	if v := q.Get("before_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "before_id must be an integer", reqID)
			return
		}
		add("id < $%d", n)
	}
	limit := 100
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "limit must be a positive integer", reqID)
			return
		}
		limit = min(n, maxLimit)
	}

	sql := `SELECT id, occurred_at, COALESCE(org_id::text,''), COALESCE(project_id::text,''),
	               COALESCE(user_id::text,''), COALESCE(actor_email,''), action, resource_type,
	               COALESCE(resource_id,''), COALESCE(outcome,''), COALESCE(method,''),
	               COALESCE(route,''), COALESCE(path,''), COALESCE(status,0),
	               COALESCE(request_id,''), COALESCE(source_ip,''), COALESCE(duration_ms,0)
	          FROM audit_events`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	sql += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))

	rows, err := h.db.Pool().Query(ctx, sql, args...)
	if err != nil {
		// Typically a malformed uuid filter.
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid filter value", reqID)
		return
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.OrgID, &e.ProjectID, &e.UserID, &e.ActorEmail,
			&e.Action, &e.ResourceType, &e.ResourceID, &e.Outcome, &e.Method, &e.Route, &e.Path,
			&e.Status, &e.RequestID, &e.SourceIP, &e.DurationMS); err != nil {
			h.log.Error("audit list scan", zap.Error(err))
			api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read audit log", reqID)
			return
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid filter value", reqID)
		return
	}
	api.WriteJSON(w, http.StatusOK, events, reqID)
}
