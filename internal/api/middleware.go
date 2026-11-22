package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
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
	// ctxKeyProjectRole is the caller's role in the active project
	// (reader | member | admin), set by ProjectScopeMiddleware.
	ctxKeyProjectRole contextKey = "project_role"

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

				matched, err := lookupPAT(ctx, db, raw)
				if err != nil {
					WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "token lookup failed", reqID)
					return
				}
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

// HashPAT returns the lookup key stored for a personal access token: the hex
// SHA-256 of the raw token. PATs carry 256 bits of randomness, so a fast
// unsalted hash is safe here (unlike passwords) and lets the token be found
// with a single indexed query.
func HashPAT(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type patMatch struct {
	patID, userID, orgID, role, orgRole string
	projectID                           *string
}

// lookupPAT resolves a raw PAT to its owner, or returns (nil, nil).
//
// Fast path: one indexed lookup by SHA-256, so cost is independent of how
// many PATs exist. Fallback: rows created before migration 010 only have a
// bcrypt hash; those (and only those) are compared, and a match is upgraded
// to the fast path so the fallback set shrinks to nothing as tokens are used.
func lookupPAT(ctx context.Context, db *postgres.DB, raw string) (*patMatch, error) {
	const cols = `p.id, u.id, u.org_id, u.role, u.org_role, u.project_id`
	sum := HashPAT(raw)

	var m patMatch
	err := db.Pool().QueryRow(ctx, `SELECT `+cols+`
		   FROM personal_access_tokens p
		   JOIN users u ON u.id = p.user_id
		  WHERE p.token_sha256 = $1
		    AND (p.expires_at IS NULL OR p.expires_at > now())`, sum,
	).Scan(&m.patID, &m.userID, &m.orgID, &m.role, &m.orgRole, &m.projectID)
	if err == nil {
		return &m, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	rows, err := db.Pool().Query(ctx, `SELECT p.token_hash, `+cols+`
		   FROM personal_access_tokens p
		   JOIN users u ON u.id = p.user_id
		  WHERE p.token_sha256 IS NULL AND p.token_hash IS NOT NULL
		    AND (p.expires_at IS NULL OR p.expires_at > now())`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found *patMatch
	for rows.Next() {
		var hash string
		var c patMatch
		if err := rows.Scan(&hash, &c.patID, &c.userID, &c.orgID, &c.role, &c.orgRole, &c.projectID); err != nil {
			return nil, err
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(raw)) == nil {
			found = &c
			break
		}
	}
	rows.Close()
	if found == nil {
		return nil, rows.Err()
	}
	if _, err := db.Pool().Exec(ctx,
		`UPDATE personal_access_tokens SET token_sha256 = $1, token_hash = NULL WHERE id = $2`,
		sum, found.patID); err != nil {
		return nil, err
	}
	return found, nil
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

// IsPlatformAdmin reports whether the caller administers the whole platform
// (legacy role 'admin' or org_role 'platform_admin'). Every platform-admin
// check uses this, so project scope and membership management agree.
func IsPlatformAdmin(ctx context.Context) bool {
	return RoleFromContext(ctx) == "admin" || OrgRoleFromContext(ctx) == "platform_admin"
}

// Project roles (project_members.role).
const (
	ProjectRoleReader = "reader"
	ProjectRoleMember = "member"
	ProjectRoleAdmin  = "admin"
)

// ValidProjectRole reports whether r is a known project role.
func ValidProjectRole(r string) bool {
	return r == ProjectRoleReader || r == ProjectRoleMember || r == ProjectRoleAdmin
}

// ProjectRoleFromContext returns the caller's role in the active project, as
// established by ProjectScopeMiddleware ("" outside project-scoped routes).
func ProjectRoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyProjectRole).(string)
	return v
}

// RequireProjectWrite rejects project readers on a route that is a read by
// HTTP method but grants more than read access — e.g. GET …/console hands
// out keyboard-and-mouse control of the guest. Mount it after
// ProjectScopeMiddleware.
func RequireProjectWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ProjectRoleFromContext(r.Context()) == ProjectRoleReader {
			WriteError(w, http.StatusForbidden, "PROJECT_READ_ONLY",
				"your role in this project is 'reader', which is read-only", middleware.GetReqID(r.Context()))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isMutatingMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
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
			if IsPlatformAdmin(ctx) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxKeyProjectRole, ProjectRoleAdmin)))
				return
			}

			userID := UserIDFromContext(ctx)
			projectID := ProjectIDFromContext(ctx)
			if userID == "" || projectID == "" {
				// No active project (e.g. an org_admin in a fresh org): reads
				// pass and see nothing, but there is no project whose role
				// could authorize a change, so mutations are refused.
				if isMutatingMethod(r.Method) {
					WriteError(w, http.StatusForbidden, "PROJECT_REQUIRED",
						"no active project; select one with the X-Project-Id header", reqID)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Validate two things in one query:
			//   1. The project exists and belongs to the caller's org.
			//   2. The caller is a member of that project.
			// This prevents cross-org access even when X-Project-Id is supplied.
			orgID := OrgIDFromContext(ctx)
			var role string
			err := db.Pool().QueryRow(ctx,
				`SELECT pm.role
				   FROM project_members pm
				   JOIN projects p ON p.id = pm.project_id
				  WHERE pm.project_id = $1
				    AND pm.user_id    = $2
				    AND p.org_id      = $3`,
				projectID, userID, orgID,
			).Scan(&role)
			if err != nil {
				WriteError(w, http.StatusForbidden, "FORBIDDEN",
					"you do not have access to the requested project", reqID)
				return
			}

			// Readers may look but not touch. Enforced here, on every
			// project-scoped route, so new routes are read-only for readers
			// by default rather than by remembering a per-handler check.
			if role == ProjectRoleReader && isMutatingMethod(r.Method) {
				WriteError(w, http.StatusForbidden, "PROJECT_READ_ONLY",
					"your role in this project is 'reader', which is read-only", reqID)
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxKeyProjectRole, role)))
		})
	}
}
