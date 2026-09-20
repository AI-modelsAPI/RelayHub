package api

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// browserBoundary protects every network-served management route, including
// read endpoints. Native clients without Origin remain supported; bearer auth
// does not exempt browser requests from same-origin checks.
func (s *Server) browserBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// net/http sets LocalAddrContextKey on real accepted connections. In-process
		// Handler tests have no listener authority and commonly use example.com.
		if s.LocalOnly && r.Context().Value(http.LocalAddrContextKey) != nil {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
				// RFC 3986 IPv6 authority with the default port omitted.
				host = host[1 : len(host)-1]
			}
			ip, err := netip.ParseAddr(host)
			if !strings.EqualFold(host, "localhost") && (err != nil || !ip.IsLoopback()) {
				s.fail(w, r, forbidden("management Host must be loopback"))
				return
			}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			s.fail(w, r, forbidden("cross-site management request denied"))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if err != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, r.Host) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				s.fail(w, r, forbidden("cross-origin management request denied"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
