package identity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// IdP URLs (issuer, and the token_endpoint named by its discovery document)
// are chosen by org admins, but fetched by the controller — discovery even
// from the unauthenticated /authorize endpoint. Without restrictions that is
// server-side request forgery into the controller's network (cloud metadata
// at 169.254.169.254, internal services, etcd/Postgres ports, …).
//
// newIssuerHTTPClient returns the only client used for IdP traffic. Policy,
// enforced on the IP actually being connected to (after DNS, so rebinding
// and redirects can't bypass it):
//
//   - IPs inside an operator-configured allowed network
//     (controller.oidc_allowed_networks) are always permitted, over http or
//     https — this is how an on-prem Keycloak is enabled.
//   - Otherwise only public unicast IPs are permitted, and only over https
//     (http would expose the client secret and codes in transit).
func newIssuerHTTPClient(allowed []netip.Prefix) *http.Client {
	policy := &issuerDialPolicy{allowed: allowed}
	dialer := &net.Dialer{
		Timeout:        5 * time.Second,
		ControlContext: policy.control,
	}
	transport := &http.Transport{
		Proxy:                 nil, // a proxy would hide the real destination from the check
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: plaintextMarker{transport},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil // each hop is re-checked by the dialer
		},
	}
}

// errIssuerBlocked is returned (wrapped) when policy forbids a connection.
var errIssuerBlocked = errors.New("connection to identity provider blocked by policy")

type plaintextKey struct{}

// plaintextMarker tags plain-http requests so the dialer can apply the
// stricter rule; the dial context carries the request's context values.
type plaintextMarker struct{ next http.RoundTripper }

func (p plaintextMarker) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme == "http" {
		r = r.WithContext(context.WithValue(r.Context(), plaintextKey{}, true))
	}
	return p.next.RoundTrip(r)
}

type issuerDialPolicy struct {
	allowed []netip.Prefix
}

// control runs after the socket is created and before connect(), with the
// resolved destination address.
func (p *issuerDialPolicy) control(ctx context.Context, _, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: unparseable address %q", errIssuerBlocked, address)
	}
	plaintext, _ := ctx.Value(plaintextKey{}).(bool)
	return p.check(ap.Addr(), plaintext)
}

func (p *issuerDialPolicy) check(ip netip.Addr, plaintext bool) error {
	ip = ip.Unmap()
	for _, pfx := range p.allowed {
		if pfx.Contains(ip) {
			return nil
		}
	}
	if !isPublicIP(ip) {
		return fmt.Errorf("%w: %s is not a public address (add it to controller.oidc_allowed_networks to permit)", errIssuerBlocked, ip)
	}
	if plaintext {
		return fmt.Errorf("%w: plain http to %s; use https, or add the network to controller.oidc_allowed_networks", errIssuerBlocked, ip)
	}
	return nil
}

// nonPublicRanges are special-purpose ranges not covered by netip's Is* helpers.
var nonPublicRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, incl. broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64: embeds an IPv4 address
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
}

func isPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, pfx := range nonPublicRanges {
		if pfx.Contains(ip) {
			return false
		}
	}
	return true
}

// parseAllowedNetworks parses CIDRs or bare IPs (treated as single hosts).
func parseAllowedNetworks(entries []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(entries))
	for _, e := range entries {
		if e == "" {
			continue
		}
		if pfx, err := netip.ParsePrefix(e); err == nil {
			out = append(out, pfx.Masked())
			continue
		}
		ip, err := netip.ParseAddr(e)
		if err != nil {
			return nil, fmt.Errorf("invalid oidc_allowed_networks entry %q: want a CIDR or IP", e)
		}
		out = append(out, netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen()))
	}
	return out, nil
}

// validateIssuerURL is the save-time check for an IdP issuer; where it may
// connect is enforced at dial time by newIssuerHTTPClient.
func validateIssuerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("issuer_url must be an absolute URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("issuer_url must use https (or http for an operator-allowed internal network)")
	}
	if u.User != nil {
		return errors.New("issuer_url must not contain credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("issuer_url must not contain a query or fragment")
	}
	return nil
}
