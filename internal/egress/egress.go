// Package egress decides which network path an outbound RelayHub request
// takes. It is the single place where "per-channel exit" is implemented, so
// the AI gateway, the check-in adapters and the browser launcher all agree:
//
//	Channel.ProxyURL  >  global default (config / RELAYHUB_EGRESS_PROXY)
//	                  >  HTTPS_PROXY / HTTP_PROXY environment  >  direct
//
// The keyword "direct" on either level forces a direct connection even when
// the environment has a proxy configured.
package egress

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"relayhub/internal/domain"
)

// Direct is the keyword that forces a direct connection.
const Direct = "direct"

// DefaultTimeout applies when a caller passes 0.
const DefaultTimeout = 15 * time.Second

var ErrUnsupportedProxyScheme = errors.New("unsupported proxy scheme")

// Selector resolves proxies to cached *http.Client values. Clients are shared
// per (proxy, timeout) pair so connection pools are reused across channels
// that exit through the same proxy.
type Selector struct {
	// Default is the global egress proxy applied to channels without their
	// own ProxyURL. Empty means "use the process environment".
	Default string

	mu      sync.Mutex
	clients map[string]*http.Client
}

// New returns a Selector with the given global default proxy.
func New(defaultProxy string) *Selector {
	return &Selector{Default: strings.TrimSpace(defaultProxy), clients: map[string]*http.Client{}}
}

// Normalize validates a proxy URL and returns its canonical form. It accepts
// http, https, socks5 and socks5h (mapped to socks5, which Go's transport
// resolves remotely anyway), the keyword "direct", and the empty string.
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.EqualFold(raw, Direct) || strings.EqualFold(raw, "none") {
		return Direct, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid proxy url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5":
	case "socks5h":
		u.Scheme = "socks5"
	default:
		return "", fmt.Errorf("%w: %q (use http, https, socks5 or direct)", ErrUnsupportedProxyScheme, u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid proxy url %q: missing host", raw)
	}
	return u.String(), nil
}

// ProxyFor returns the effective proxy setting for a channel: its own
// ProxyURL, else the selector default, else "" (environment).
func (s *Selector) ProxyFor(ch domain.Channel) string {
	if p := strings.TrimSpace(ch.ProxyURL); p != "" {
		if n, err := Normalize(p); err == nil {
			return n
		}
		// An invalid per-channel proxy must fail closed to the channel's
		// own setting, not silently fall through to a different exit.
		return p
	}
	if s == nil {
		return ""
	}
	return s.Default
}

// ClientFor returns the cached client for a channel.
func (s *Selector) ClientFor(ch domain.Channel, timeout time.Duration) *http.Client {
	return s.Client(s.ProxyFor(ch), timeout)
}

// Client returns the cached client for a proxy setting. An unparsable proxy
// yields a client whose every request fails with the parse error, so a typo
// never leaks traffic through the wrong exit.
func (s *Selector) Client(proxy string, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	key := proxy + "|" + timeout.String()
	if s == nil {
		return newClient(proxy, timeout)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[key]; ok {
		return c
	}
	c := newClient(proxy, timeout)
	s.clients[key] = c
	return c
}

// StreamingClientFor returns a client without an overall timeout for the
// channel, sharing the proxy's transport. Callers bound it with a context.
func (s *Selector) StreamingClientFor(ch domain.Channel) *http.Client {
	return s.StreamingClient(s.ProxyFor(ch))
}

// StreamingClient is Client without an overall timeout (for SSE/long bodies).
func (s *Selector) StreamingClient(proxy string) *http.Client {
	key := proxy + "|stream"
	if s == nil {
		return &http.Client{Transport: NewTransport(proxy), CheckRedirect: SameOriginRedirect}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[key]; ok {
		return c
	}
	c := &http.Client{Transport: NewTransport(proxy), CheckRedirect: SameOriginRedirect}
	s.clients[key] = c
	return c
}

func newClient(proxy string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: NewTransport(proxy), CheckRedirect: SameOriginRedirect}
}

// SameOriginRedirect is the redirect policy for every upstream client. Go's
// default follows up to 10 redirects to any host, so an upstream answering
// "302 Location: http://127.0.0.1:8790/…" (or a metadata address) made
// RelayHub fetch internal URLs, and custom credential headers such as
// x-api-key survive cross-host redirects (AUDIT 2026-09-24 F9/F16). Only
// same-origin hops (plus an http→https upgrade of the same host on default
// ports) are followed, at most three; anything else is returned to the caller
// as the 3xx response itself.
func SameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= 3 {
		return http.ErrUseLastResponse
	}
	from, to := via[0].URL, req.URL
	if !strings.EqualFold(from.Hostname(), to.Hostname()) {
		return http.ErrUseLastResponse
	}
	if from.Scheme == to.Scheme && effectivePort(from) == effectivePort(to) {
		return nil
	}
	if from.Scheme == "http" && to.Scheme == "https" && effectivePort(from) == "80" && effectivePort(to) == "443" {
		return nil
	}
	return http.ErrUseLastResponse
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// NewTransport builds a transport honouring the proxy setting.
func NewTransport(proxy string) http.RoundTripper {
	base := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	norm, err := Normalize(proxy)
	if err != nil {
		return errTransport{err: err}
	}
	switch norm {
	case "":
		// environment
	case Direct:
		base.Proxy = nil
	default:
		u, _ := url.Parse(norm)
		base.Proxy = http.ProxyURL(u)
	}
	return base
}

type errTransport struct{ err error }

func (e errTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, e.err }

// Describe renders a proxy setting for logs/UI without credentials.
func Describe(proxy string) string {
	norm, err := Normalize(proxy)
	if err != nil {
		return "invalid"
	}
	switch norm {
	case "":
		return "environment/direct"
	case Direct:
		return Direct
	}
	u, _ := url.Parse(norm)
	if u.User != nil {
		u.User = url.User(u.User.Username())
		return strings.Replace(u.String(), u.User.Username()+"@", u.User.Username()+":***@", 1)
	}
	return u.String()
}
