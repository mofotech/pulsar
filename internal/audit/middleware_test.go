package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestDescribe(t *testing.T) {
	cases := []struct {
		method, route   string
		params          map[string]string
		typ, id, action string
	}{
		{"POST", "/v1/compute/instances", nil, "compute.instances", "", "create"},
		{"DELETE", "/v1/compute/instances/{id}", map[string]string{"id": "i-1"}, "compute.instances", "i-1", "delete"},
		{"POST", "/v1/compute/instances/{id}/resize", map[string]string{"id": "i-1"}, "compute.instances", "i-1", "resize"},
		{"PUT", "/v1/network/routers/{id}/gateway", map[string]string{"id": "r-1"}, "network.routers", "r-1", "update.gateway"},
		{"DELETE", "/v1/network/routers/{id}/interfaces", map[string]string{"id": "r-1"}, "network.routers", "r-1", "delete.interfaces"},
		{"PATCH", "/v1/orgs/{org_id}/idps/{idp_id}", map[string]string{"org_id": "o", "idp_id": "p"}, "orgs.idps", "p", "update"},
		// Ambiguous from the route alone; the middleware resolves it from the response.
		{"POST", "/v1/orgs/{org_id}/idps/", map[string]string{"org_id": "o"}, "orgs", "o", "idps"},
		{"PUT", "/v1/projects/{id}/quotas", map[string]string{"id": "p-1"}, "projects", "p-1", "update.quotas"},
		{"DELETE", "/v1/compute/instances/{id}/metadata/{key}", map[string]string{"id": "i", "key": "env"}, "compute.instances.metadata", "env", "delete"},
	}
	for _, c := range cases {
		rctx := chi.NewRouteContext()
		for k, v := range c.params {
			rctx.URLParams.Add(k, v)
		}
		typ, id, action := describe(c.method, c.route, rctx)
		if typ != c.typ || id != c.id || action != c.action {
			t.Errorf("%s %s → (%q, %q, %q), want (%q, %q, %q)", c.method, c.route, typ, id, action, c.typ, c.id, c.action)
		}
	}
}

func TestCreatedID(t *testing.T) {
	for body, want := range map[string]string{
		`{"data":{"id":"abc-123","name":"x"},"meta":{}}`: "abc-123",
		`{"data":{"id":42}}`:                             "42",
		`{"data":[{"id":"a"}]}`:                          "",
		`not json`:                                       "",
	} {
		if got := createdID([]byte(body)); got != want {
			t.Errorf("createdID(%s) = %q, want %q", body, got, want)
		}
	}
}

func TestOutcomeFor(t *testing.T) {
	for status, want := range map[int]string{200: Success, 201: Success, 204: Success, 401: Denied, 403: Denied, 404: Failure, 422: Failure, 500: Failure} {
		if got := outcomeFor(status); got != want {
			t.Errorf("outcomeFor(%d) = %s, want %s", status, got, want)
		}
	}
}

func TestClientIP(t *testing.T) {
	for addr, want := range map[string]string{
		"10.0.0.5:51234": "10.0.0.5",
		"[::1]:8080":     "::1",
		"203.0.113.9":    "203.0.113.9", // RealIP already stripped the port
		"2001:db8::1":    "2001:db8::1", // bare IPv6 from a forwarding header
		"[2001:db8::1]":  "2001:db8::1",
		"::1":            "::1",
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		if got := clientIP(r); got != want {
			t.Errorf("clientIP(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestCappedBuffer(t *testing.T) {
	c := &capped{limit: 5}
	n, err := c.Write([]byte("hello world"))
	if n != 11 || err != nil || string(c.Bytes()) != "hello" {
		t.Fatalf("n=%d err=%v buf=%q", n, err, c.Bytes())
	}
	_, _ = c.Write([]byte("more"))
	if string(c.Bytes()) != "hello" {
		t.Fatalf("grew past limit: %q", c.Bytes())
	}
}

func TestNilRecorderIsANoop(t *testing.T) {
	var r *Recorder
	r.Record(Event{Action: "x"})
	r.Close()
	r.StartRetention(context.Background(), 1)
	h := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader("{}")))
	if rec.Code != 201 {
		t.Fatal("nil recorder must pass requests through")
	}
}

func TestSanitizeEmail(t *testing.T) {
	for in, want := range map[string]string{
		"ada@example.test":                 "ada@example.test",
		"  ada@example.test ":              "ada@example.test",
		"hunter2":                          "", // a password typed into the email box
		"correct horse@battery.staple":     "",
		"a@b@example.test":                 "",
		"@example.test":                    "",
		"ada@":                             "",
		"ada@localhost":                    "ada@localhost",
		strings.Repeat("a", 250) + "@x.io": "",
	} {
		if got := SanitizeEmail(in); got != want {
			t.Errorf("SanitizeEmail(%.40q) = %q, want %q", in, got, want)
		}
	}
}

func TestUUIDOrNil(t *testing.T) {
	for in, valid := range map[string]bool{
		"6f1c2a9e-3b7d-4c8e-9a0f-1d2e3f4a5b6c":   true,
		"zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz":   false, // right shape, not hex
		"{6f1c2a9e-3b7d-4c8e-9a0f-1d2e3f4a5b6c}": false,
		"not-a-uuid":                             false,
		"":                                       false,
	} {
		if got := uuidOrNil(in); (got != nil) != valid {
			t.Errorf("uuidOrNil(%q) = %v, want valid=%v", in, got, valid)
		}
	}
}
