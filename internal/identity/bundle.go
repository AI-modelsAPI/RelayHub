package identity

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"relayhub/internal/domain"
)

// Bundle is the egress identity shared by check-in, gateway, and probes.
type Bundle struct {
	ChannelID string `json:"channel_id"`
	ProxyURL  string `json:"proxy_url"`
	UserAgent string `json:"user_agent"`
	Timezone  string `json:"timezone"`
	EgressIP  string `json:"egress_ip,omitempty"`
	Drift     bool   `json:"drift,omitempty"`
}

func FromChannel(ch domain.Channel) Bundle {
	ua, tz := "", ""
	if ch.CustomHeaders != nil {
		ua = ch.CustomHeaders["User-Agent"]
		tz = ch.CustomHeaders["X-Timezone"]
	}
	return Bundle{ChannelID: ch.ID, ProxyURL: ch.ProxyURL, UserAgent: ua, Timezone: tz}
}

func ApplyToChannel(ch *domain.Channel, b Bundle) {
	if ch.CustomHeaders == nil {
		ch.CustomHeaders = map[string]string{}
	}
	// An empty bundle field means "not specified", never "clear": the old
	// condition wiped a configured proxy whenever the bundle omitted it,
	// silently switching the channel to direct egress (AUDIT 2026-09-24 F12).
	if b.ProxyURL != "" {
		ch.ProxyURL = b.ProxyURL
	}
	if b.UserAgent != "" {
		ch.CustomHeaders["User-Agent"] = b.UserAgent
	}
	if b.Timezone != "" {
		ch.CustomHeaders["X-Timezone"] = b.Timezone
	}
}

// clientCache shares transports per proxy configuration so check-in, probes
// and management fetches reuse connections instead of building a fresh
// Transport per call (AUDIT RH-20). Keyed by proxy URL + timeout; both are
// operator-configured, so cardinality is bounded in practice.
var clientCache sync.Map // string -> *http.Client

// HTTPClientE is HTTPClient with error reporting: an invalid proxy URL is a
// configuration failure (fail closed), never a silent direct egress
// (AUDIT RH-10).
func HTTPClientE(proxyRaw string, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	proxyRaw = strings.TrimSpace(proxyRaw)
	key := proxyRaw + "|" + timeout.String()
	if cached, ok := clientCache.Load(key); ok {
		return cached.(*http.Client), nil
	}
	c := &http.Client{Timeout: timeout}
	if proxyRaw != "" {
		u, err := url.Parse(proxyRaw)
		if err != nil || u.Scheme == "" {
			return nil, fmt.Errorf("invalid proxy url %q", proxyRaw)
		}
		c.Transport = &http.Transport{
			Proxy:           http.ProxyURL(u),
			MaxIdleConns:    32,
			IdleConnTimeout: 90 * time.Second,
		}
	}
	actual, _ := clientCache.LoadOrStore(key, c)
	return actual.(*http.Client), nil
}

func HTTPClient(proxyRaw string, timeout time.Duration) *http.Client {
	c, err := HTTPClientE(proxyRaw, timeout)
	if err != nil {
		// Legacy callers that cannot surface the error keep the previous
		// degrade-to-direct behaviour; new call sites must use HTTPClientE.
		fallback, _ := HTTPClientE("", timeout)
		return fallback
	}
	return c
}

func ApplyRequestHeaders(h http.Header, ch domain.Channel) {
	if ch.CustomHeaders == nil {
		return
	}
	for k, v := range ch.CustomHeaders {
		if strings.TrimSpace(v) != "" {
			h.Set(k, v)
		}
	}
}

// ProbeEgress fetches a public IP via the channel proxy. Empty on failure.
func ProbeEgress(ctx context.Context, proxyRaw string) string {
	// An invalid proxy must not degrade to a direct probe: that reported the
	// operator's real IP as the channel's egress and disclosed it to the
	// probe service (AUDIT 2026-09-24 F13).
	client, err := HTTPClientE(proxyRaw, 8*time.Second)
	if err != nil {
		return ""
	}
	return ProbeEgressWith(ctx, client)
}

// ProbeEgressWith asks the public IP echo service which address client exits
// from. Callers pass the same client the channel's real traffic uses.
func ProbeEgressWith(ctx context.Context, client *http.Client) string {
	if client == nil {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return strings.TrimSpace(string(b))
}
