package identity

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8":                true,
		"1.1.1.1":                true,
		"2606:4700::1111":        true,
		"127.0.0.1":              false,
		"10.11.3.190":            false,
		"172.16.0.1":             false,
		"192.168.1.1":            false,
		"169.254.169.254":        false, // cloud metadata
		"100.64.0.1":             false, // CGNAT
		"0.0.0.0":                false,
		"255.255.255.255":        false,
		"224.0.0.1":              false,
		"::1":                    false,
		"fd00::1":                false, // unique local
		"fe80::1":                false,
		"::ffff:127.0.0.1":       false, // v4-mapped loopback
		"::ffff:169.254.169.254": false,
		"64:ff9b::a9fe:a9fe":     false, // NAT64 → 169.254.169.254
	} {
		if got := isPublicIP(netip.MustParseAddr(ip)); got != want {
			t.Errorf("isPublicIP(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestIssuerDialPolicy(t *testing.T) {
	p := &issuerDialPolicy{allowed: []netip.Prefix{netip.MustParsePrefix("10.11.0.0/16")}}
	cases := []struct {
		ip        string
		plaintext bool
		ok        bool
	}{
		{"8.8.8.8", false, true},     // public https
		{"8.8.8.8", true, false},     // public http leaks secrets
		{"10.11.3.190", true, true},  // allow-listed internal Keycloak over http
		{"10.11.3.190", false, true}, // …or https
		{"10.12.0.1", false, false},  // private, not allow-listed
		{"169.254.169.254", false, false},
		{"127.0.0.1", false, false},
	}
	for _, c := range cases {
		err := p.check(netip.MustParseAddr(c.ip), c.plaintext)
		if (err == nil) != c.ok {
			t.Errorf("check(%s, plaintext=%v) err=%v, want ok=%v", c.ip, c.plaintext, err, c.ok)
		}
		if err != nil && !errors.Is(err, errIssuerBlocked) {
			t.Errorf("check(%s): error %v does not wrap errIssuerBlocked", c.ip, err)
		}
	}
}

func TestIssuerClientBlocksBeforeConnecting(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()

	blocked := newIssuerHTTPClient(nil)
	for _, u := range []string{
		srv.URL, // loopback
		"http://169.254.169.254/latest/meta-data/",
		"http://8.8.8.8:1/", // public but plain http: refused before connect
	} {
		resp, err := blocked.Get(u) //nolint:noctx // test
		if err == nil {
			resp.Body.Close()
			t.Fatalf("GET %s succeeded; want blocked", u)
		}
		if !errors.Is(err, errIssuerBlocked) {
			t.Fatalf("GET %s: %v; want errIssuerBlocked", u, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("blocked server received %d requests", n)
	}

	allowed := newIssuerHTTPClient([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")})
	resp, err := allowed.Get(srv.URL) //nolint:noctx // test
	if err != nil {
		t.Fatalf("allow-listed GET: %v", err)
	}
	resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatal("allow-listed request did not reach the server")
	}
}

// A redirect is a new connection and must pass the same check.
func TestIssuerClientRechecksRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()
	c := newIssuerHTTPClient([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")})
	resp, err := c.Get(srv.URL) //nolint:noctx // test
	if err == nil {
		resp.Body.Close()
		t.Fatal("redirect to metadata endpoint was followed")
	}
	if !errors.Is(err, errIssuerBlocked) {
		t.Fatalf("got %v, want errIssuerBlocked", err)
	}
}

func TestValidateIssuerURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://accounts.google.com":            true,
		"http://keycloak.lab:8080/realms/pulsar": true, // reachability is decided at dial time
		"ftp://idp.example":                      false,
		"file:///etc/passwd":                     false,
		"https://user:pw@idp.example":            false,
		"https://idp.example/?x=1":               false,
		"/relative":                              false,
		"":                                       false,
	} {
		if err := validateIssuerURL(raw); (err == nil) != ok {
			t.Errorf("validateIssuerURL(%q) err=%v, want ok=%v", raw, err, ok)
		}
	}
}

func TestParseAllowedNetworks(t *testing.T) {
	got, err := parseAllowedNetworks([]string{"10.11.0.0/16", "192.168.1.5", "10.0.0.7/8"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.11.0.0/16", "192.168.1.5/32", "10.0.0.0/8"}
	for i, p := range got {
		if p.String() != want[i] {
			t.Errorf("entry %d = %s, want %s", i, p, want[i])
		}
	}
	if _, err := parseAllowedNetworks([]string{"not-a-cidr"}); err == nil || !strings.Contains(err.Error(), "not-a-cidr") {
		t.Fatalf("want error naming the bad entry, got %v", err)
	}
}
