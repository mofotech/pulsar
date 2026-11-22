package api

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseNetworks parses CIDRs or bare IPs (as in controller.trusted_proxies).
func ParseNetworks(entries []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if pfx, err := netip.ParsePrefix(e); err == nil {
			out = append(out, pfx.Masked())
			continue
		}
		ip, err := netip.ParseAddr(e)
		if err != nil {
			return nil, fmt.Errorf("%q is not a CIDR or IP", e)
		}
		ip = ip.Unmap()
		out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
	}
	return out, nil
}

// RealIP replaces r.RemoteAddr with the client address from X-Forwarded-For
// or X-Real-IP — but only when the request arrived from a trusted proxy.
// Unlike chi's middleware.RealIP, a client connecting directly cannot set
// its own recorded address by sending the header.
//
// X-Forwarded-For is walked right to left, skipping trusted hops, so the
// first untrusted address is the client: anything left of it was supplied
// by the client and is ignored.
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(ip netip.Addr) bool {
		ip = ip.Unmap()
		for _, p := range trusted {
			if p.Contains(ip) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		if len(trusted) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, ok := parseIP(r.RemoteAddr)
			if ok && isTrusted(peer) {
				if client, ok := forwardedClient(r, isTrusted); ok {
					r.RemoteAddr = client.String()
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func forwardedClient(r *http.Request, isTrusted func(netip.Addr) bool) (netip.Addr, bool) {
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
		hops := strings.Split(strings.Join(xff, ","), ",")
		var last netip.Addr
		for i := len(hops) - 1; i >= 0; i-- {
			ip, ok := parseIP(strings.TrimSpace(hops[i]))
			if !ok {
				break // garbage: stop at the last address we could trust
			}
			last = ip
			if !isTrusted(ip) {
				return ip, true
			}
		}
		if last.IsValid() {
			return last, true // every hop was a trusted proxy
		}
	}
	if ip, ok := parseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ok {
		return ip, true
	}
	return netip.Addr{}, false
}

// parseIP accepts "ip", "ip:port", "[v6]" and "[v6]:port".
func parseIP(s string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	ip, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}
