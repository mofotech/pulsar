package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRealIP(t *testing.T) {
	trusted, err := ParseNetworks([]string{"10.0.0.0/8", "fd00::1"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, peer, xff, xri, want string
	}{
		{"untrusted peer: header ignored", "203.0.113.9:1234", "10.9.9.9", "", "203.0.113.9:1234"},
		{"trusted proxy: client from XFF", "10.0.0.2:1234", "198.51.100.7", "", "198.51.100.7"},
		{"client-supplied hops left of the real client are ignored", "10.0.0.2:1234", "1.2.3.4, 198.51.100.7, 10.0.0.3", "", "198.51.100.7"},
		{"bare IPv6 client", "[fd00::1]:443", "2001:db8::5", "", "2001:db8::5"},
		{"X-Real-IP fallback", "10.0.0.2:1234", "", "198.51.100.8", "198.51.100.8"},
		{"garbage header: keep peer", "10.0.0.2:1234", "not-an-ip", "", "10.0.0.2:1234"},
		{"all hops trusted", "10.0.0.2:1234", "10.0.0.5", "", "10.0.0.5"},
	}
	for _, c := range cases {
		var got string
		h := RealIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.xri != "" {
			r.Header.Set("X-Real-IP", c.xri)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != c.want {
			t.Errorf("%s: RemoteAddr = %q, want %q", c.name, got, c.want)
		}
	}

	// No trusted proxies configured: never believe the header.
	var got string
	h := RealIP(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:1"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "203.0.113.9:1" {
		t.Errorf("no trusted proxies: RemoteAddr = %q", got)
	}
}
