package identity

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
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
	if b.ProxyURL != "" || ch.ProxyURL != "" {
		ch.ProxyURL = b.ProxyURL
	}
	if b.UserAgent != "" {
		ch.CustomHeaders["User-Agent"] = b.UserAgent
	}
	if b.Timezone != "" {
		ch.CustomHeaders["X-Timezone"] = b.Timezone
	}
}

func HTTPClient(proxyRaw string, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	c := &http.Client{Timeout: timeout}
	proxyRaw = strings.TrimSpace(proxyRaw)
	if proxyRaw == "" {
		return c
	}
	u, err := url.Parse(proxyRaw)
	if err != nil || u.Scheme == "" {
		return c
	}
	c.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
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
	client := HTTPClient(proxyRaw, 8*time.Second)
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
