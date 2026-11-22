package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/agomez/pulsar/internal/api"
)

// handlerRecorded maps authentication routes to their audit action. Their
// handlers record events explicitly (so an event can carry details the
// middleware can't know, like the login email) and call MarkRecorded. Any
// path through the handler that doesn't — a malformed body, an internal
// error, an invalid logout token, a bad SSO state — is recorded here instead
// from the response status, so no attempt goes unrecorded.
var handlerRecorded = map[string]string{
	"POST /v1/auth/tokens":                  "login",
	"DELETE /v1/auth/tokens":                "logout",
	"GET /v1/auth/oidc/{idp_slug}/callback": "sso_login",
}

type recordedKey struct{}

// MarkRecorded tells the middleware the handler has recorded this request
// itself, so it must not record a fallback event too.
func MarkRecorded(req *http.Request) {
	if p, ok := req.Context().Value(recordedKey{}).(*bool); ok {
		*p = true
	}
}

// Middleware records every mutating /v1 request after it completes. Mount it
// on the root router, before any auth middleware.
func (r *Recorder) Middleware(next http.Handler) http.Handler {
	if r == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Reads are not audited, except the SSO callback (a GET); /v1/auth/
		// reads are wrapped so recordRequest can pick that route out.
		if !strings.HasPrefix(req.URL.Path, "/v1/") ||
			(!isMutating(req.Method) && !strings.HasPrefix(req.URL.Path, "/v1/auth/")) {
			next.ServeHTTP(w, req)
			return
		}
		ctx, principal := api.WithPrincipalSlot(req.Context())
		recorded := new(bool)
		ctx = context.WithValue(ctx, recordedKey{}, recorded)
		req = req.WithContext(ctx)

		ww := middleware.NewWrapResponseWriter(w, req.ProtoMajor)
		// Creates return the new resource's id in the body; capture just
		// enough to read it. The body itself is never stored.
		var body *capped
		if req.Method == http.MethodPost {
			body = &capped{limit: 64 << 10}
			ww.Tee(body)
		}
		start := time.Now()
		// Record from a defer so a handler that panics (and is turned into a
		// 500 by Recoverer, mounted outside this middleware) still leaves a
		// trail: a half-run mutation is exactly what an audit log is for.
		completed := false
		defer func() {
			if completed {
				return
			}
			p := recover()
			r.recordRequest(req, ww, body, principal, *recorded, start, http.StatusInternalServerError)
			panic(p)
		}()
		next.ServeHTTP(ww, req)
		completed = true
		r.recordRequest(req, ww, body, principal, *recorded, start, 0)
	})
}

// recordRequest builds and records the event for a finished request.
// forceStatus, when non-zero, overrides the status the handler wrote (used
// for panics, where the handler may have written nothing or a partial 200).
func (r *Recorder) recordRequest(req *http.Request, ww middleware.WrapResponseWriter, body *capped,
	principal *api.Principal, handlerDidRecord bool, start time.Time, forceStatus int) {
	rctx := chi.RouteContext(req.Context())
	route := ""
	if rctx != nil {
		route = rctx.RoutePattern()
	}
	if route == "" {
		return // unrouted (404)
	}
	status := ww.Status()
	if status == 0 {
		status = http.StatusOK
	}
	if forceStatus != 0 {
		status = forceStatus
	}
	if action, ok := handlerRecorded[req.Method+" "+route]; ok {
		if !handlerDidRecord {
			e := RequestEvent(req, "auth", action, outcomeFor(status))
			e.Status = status
			e.DurationMS = int(time.Since(start).Milliseconds())
			r.Record(e)
		}
		return
	}
	if !isMutating(req.Method) {
		return
	}
	// A pattern still ending in /* means the request never reached a
	// handler: either no route matched (skip — just noise) or group
	// middleware such as auth rejected it first (keep: that's a denied
	// attempt). Describe the latter from the matched prefix; the exact
	// path is stored too.
	describeRoute := route
	if strings.HasSuffix(route, "/*") {
		if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
			return
		}
		describeRoute = strings.TrimSuffix(route, "/*")
	}
	resType, resID, action := describe(req.Method, describeRoute, rctx)
	if resType == "" {
		resType = "api"
	}
	if body != nil && status < 300 {
		created := createdID(body.Bytes())
		switch {
		case resID == "":
			resID = created // POST /v1/compute/instances
		case created != "" && created != resID && action != "create":
			// POST /v1/orgs/{org_id}/idps: the response names a new child
			// resource, so this was a create in a sub-collection, not an
			// action on the parent (as POST …/instances/{id}/resize is).
			resType, resID, action = resType+"."+action, created, "create"
		}
	}
	r.Record(Event{
		OrgID:        principal.OrgID,
		ProjectID:    principal.ProjectID,
		UserID:       principal.UserID,
		ActorEmail:   principal.Email,
		Action:       action,
		ResourceType: resType,
		ResourceID:   resID,
		Outcome:      outcomeFor(status),
		Method:       req.Method,
		Route:        route,
		Path:         req.URL.Path,
		Status:       status,
		RequestID:    middleware.GetReqID(req.Context()),
		SourceIP:     clientIP(req),
		DurationMS:   int(time.Since(start).Milliseconds()),
	})
}

func isMutating(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func outcomeFor(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return Denied
	case status >= 400:
		return Failure
	default:
		return Success
	}
}

// describe derives (resource type, resource id, action) from the route:
//
//	POST   /v1/compute/instances                → compute.instances, -, create
//	DELETE /v1/compute/instances/{id}           → compute.instances, <id>, delete
//	POST   /v1/compute/instances/{id}/resize    → compute.instances, <id>, resize
//	PUT    /v1/network/routers/{id}/gateway     → network.routers, <id>, update.gateway
//	PATCH  /v1/orgs/{org_id}/idps/{idp_id}      → orgs.idps, <idp_id>, update
func describe(method, route string, rctx *chi.Context) (resType, resID, action string) {
	trimmed := strings.Trim(strings.TrimPrefix(route, "/v1"), "/")
	var segs []string
	if trimmed != "" {
		segs = strings.Split(trimmed, "/")
	}
	var static []string
	var trailing []string
	lastParam := ""
	for _, s := range segs {
		if strings.HasPrefix(s, "{") {
			lastParam = strings.Trim(s, "{}")
			static = append(static, trailing...)
			trailing = nil
			continue
		}
		trailing = append(trailing, s)
	}
	if lastParam == "" { // collection route, e.g. POST /v1/compute/instances
		static, trailing = trailing, nil
	}
	resType = strings.Join(static, ".")
	if lastParam != "" && rctx != nil {
		resID = rctx.URLParam(lastParam)
	}
	verb := map[string]string{
		http.MethodPost: "create", http.MethodPut: "update",
		http.MethodPatch: "update", http.MethodDelete: "delete",
	}[method]
	switch {
	case len(trailing) == 0:
		action = verb
	case method == http.MethodPost:
		action = strings.Join(trailing, ".") // sub-resource verb: resize, migrate, action…
	default:
		action = verb + "." + strings.Join(trailing, ".")
	}
	return resType, resID, action
}

// createdID reads data.id from an API envelope.
func createdID(body []byte) string {
	var env struct {
		Data struct {
			ID any `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &env) != nil {
		return ""
	}
	switch v := env.Data.ID.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// clientIP is RemoteAddr after chi's RealIP middleware has applied
// X-Forwarded-For / X-Real-IP; the port is dropped.
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 && !strings.HasSuffix(addr, "]") {
		addr = addr[:i]
	}
	return strings.Trim(addr, "[]")
}

// capped buffers at most limit bytes and silently discards the rest.
type capped struct {
	buf   bytes.Buffer
	limit int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *capped) Bytes() []byte { return c.buf.Bytes() }

// RequestEvent pre-fills request metadata for handlers that record events
// explicitly (login, logout, SSO).
func RequestEvent(req *http.Request, resourceType, action, outcome string) Event {
	e := Event{
		ResourceType: resourceType,
		Action:       action,
		Outcome:      outcome,
		Method:       req.Method,
		Path:         req.URL.Path,
		RequestID:    middleware.GetReqID(req.Context()),
		SourceIP:     clientIP(req),
	}
	if rctx := chi.RouteContext(req.Context()); rctx != nil {
		e.Route = rctx.RoutePattern()
	}
	return e
}
