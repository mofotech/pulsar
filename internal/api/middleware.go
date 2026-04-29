package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/internal/store/postgres"
)

type contextKey string

const (
	ctxKeyUserID    contextKey = "user_id"
	ctxKeyProjectID contextKey = "project_id"
	ctxKeyRole      contextKey = "role"
	ctxKeyOrgID     contextKey = "org_id"
	ctxKeyOrgRole   contextKey = "org_role"

	// revokedTokenKeyPrefix mirrors the value in the identity handler.
	revokedTokenKeyPrefix = "/pulsar/auth/revoked/" //nolint:gosec // etcd key prefix, not a credential
)

// AuthMiddleware validates a Bearer token (JWT or PAT) and injects user claims
// into the request context.
//
//   - store: optional etcd client used for JWT revocation checks (nil = skip)
//   - db:    optional postgres DB used for PAT lookups (nil = PATs rejected)
func AuthMiddleware(secret string, store *etcd.Client, db *postgres.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := middleware.GetReqID(r.Context())

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing Authorization header", reqID)
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid Authorization format", reqID)
				return
			}

			raw := parts[1]

			// ── PAT path ──────────────────────────────────────────────────────
			// Personal access tokens start with "pat_". Look them up in postgres
			// rather than parsing as JWT.
			if strings.HasPrefix(raw, "pat_") {
				if db == nil {
					WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token", reqID)
					return
				}
				ctx := r.Context()

				// Fetch all unexpired PATs that could match — then bcrypt-compare.
				// We scope by token prefix length as a cheap pre-filter (impossible
				// to query by hash directly), so we pull candidate rows per-user.
				// In practice a user has very few PATs so this is fast.
				rows, err := db.Pool().Query(ctx,
					`SELECT p.id, p.token_hash, p.expires_at,
					        u.id, u.org_id, u.role, u.org_role, u.project_id
					   FROM personal_access_tokens p
					   JOIN users u ON u.id = p.user_id
					  WHERE (p.expires_at IS NULL OR p.expires_at > now())`)
				if err != nil {
					WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "token lookup failed", reqID)
					return
				}
				defer rows.Close()

				type candidate struct {
					patID, hash, userID, orgID, role, orgRole string
					projectID                                 *string
				}
				var matched *candidate
				for rows.Next() {
					var c candidate
					var expiresAt interface{}
					if err := rows.Scan(&c.patID, &c.hash, &expiresAt,
						&c.userID, &c.orgID, &c.role, &c.orgRole, &c.projectID); err != nil {
						continue
					}
					if bcrypt.CompareHashAndPassword([]byte(c.hash), []byte(raw)) == nil {
						matched = &c
						break
					}
				}
				rows.Close()

				if matched == nil {
					WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token", reqID)
					return
				}

				// Bump last_used_at asynchronously so we don't block the request.
				go func() {
					db.Pool().Exec(context.Background(), //nolint:errcheck
						`UPDATE personal_access_tokens SET last_used_at = now() WHERE id = $1`,
						matched.patID)
				}()

				projectID := ""
				if matched.projectID != nil {
					projectID = *matched.projectID
				}
				// Allow X-Project-Id override just like JWTs.
				if hdr := r.Header.Get("X-Project-Id"); hdr != "" {
					projectID = hdr
				}

				ctx = context.WithValue(ctx, ctxKeyUserID, matched.userID)
				ctx = context.WithValue(ctx, ctxKeyProjectID, projectID)
				ctx = context.WithValue(ctx, ctxKeyRole, matched.role)
				ctx = context.WithValue(ctx, ctxKeyOrgID, matched.orgID)
				ctx = context.WithValue(ctx, ctxKeyOrgRole, matched.orgRole)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// ── JWT path ──────────────────────────────────────────────────────
			token, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return []byte(secret), nil
			})
			if err != nil || !token.Valid {
				WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token", reqID)
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid claims", reqID)
				return
			}

			// Check revocation list if etcd is available.
			if store != nil {
				if jti, ok := claims["jti"].(string); ok && jti != "" {
					if val, _ := store.Get(r.Context(), revokedTokenKeyPrefix+jti); val != "" {
						WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "token has been revoked", reqID)
						return
					}
				}
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, ctxKeyUserID, claims["sub"])

			projectID := r.Header.Get("X-Project-Id")
			if projectID == "" {
				projectID, _ = claims["project_id"].(string)
			}
			ctx = context.WithValue(ctx, ctxKeyProjectID, projectID)
			ctx = context.WithValue(ctx, ctxKeyRole, claims["role"])
			ctx = context.WithValue(ctx, ctxKeyOrgID, claims["org_id"])
			ctx = context.WithValue(ctx, ctxKeyOrgRole, claims["org_role"])
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ZapMiddleware logs each request using zap structured logging.
func ZapMiddleware(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("http request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Duration("latency", time.Since(start)),
				zap.String("request_id", middleware.GetReqID(r.Context())),
			)
		})
	}
}

// UserIDFromContext extracts the user ID injected by authMiddleware.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserID).(string)
	return v
}

// ProjectIDFromContext extracts the project ID injected by authMiddleware.
func ProjectIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyProjectID).(string)
	return v
}

// RoleFromContext extracts the role injected by authMiddleware.
func RoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRole).(string)
	return v
}

// OrgIDFromContext extracts the org_id claim injected by AuthMiddleware.
func OrgIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyOrgID).(string)
	return v
}

// OrgRoleFromContext extracts the org_role claim injected by AuthMiddleware.
// Returns 'platform_admin', 'org_admin', or 'member'.
func OrgRoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyOrgRole).(string)
	return v
}

// ProjectScopeMiddleware validates that the active project (from X-Project-Id
// header or JWT) is one the calling user is actually a member of AND belongs to
// the same organisation as the calling user.
// Platform admins (role == "admin") bypass the check entirely.
// Must be applied after AuthMiddleware.
func ProjectScopeMiddleware(db *postgres.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			reqID := middleware.GetReqID(ctx)

			// Platform admins may access any project in any org.
			if RoleFromContext(ctx) == "admin" {
				next.ServeHTTP(w, r)
				return
			}

			userID := UserIDFromContext(ctx)
			projectID := ProjectIDFromContext(ctx)
			if userID == "" || projectID == "" {
				// org_admin with no project yet (fresh org) — let identity routes
				// through; resource routes will see an empty project and return nothing.
				next.ServeHTTP(w, r)
				return
			}

			// Validate two things in one query:
			//   1. The project exists and belongs to the caller's org.
			//   2. The caller is a member of that project.
			// This prevents cross-org access even when X-Project-Id is supplied.
			orgID := OrgIDFromContext(ctx)
			var count int
			err := db.Pool().QueryRow(ctx,
				`SELECT COUNT(*)
				   FROM project_members pm
				   JOIN projects p ON p.id = pm.project_id
				  WHERE pm.project_id = $1
				    AND pm.user_id    = $2
				    AND p.org_id      = $3`,
				projectID, userID, orgID,
			).Scan(&count)
			if err != nil || count == 0 {
				WriteError(w, http.StatusForbidden, "FORBIDDEN",
					"you do not have access to the requested project", reqID)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
