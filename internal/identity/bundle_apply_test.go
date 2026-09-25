package identity

import (
	"testing"

	"relayhub/internal/domain"
)

// AUDIT 2026-09-24 F12: an empty bundle proxy means "unspecified" and must
// never clear the channel's configured proxy.
func TestApplyToChannelKeepsProxyWhenBundleOmitsIt(t *testing.T) {
	ch := domain.Channel{ID: "c1", ProxyURL: "socks5://127.0.0.1:1080"}
	ApplyToChannel(&ch, Bundle{UserAgent: "ua"})
	if ch.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("proxy cleared: %q", ch.ProxyURL)
	}
	if ch.CustomHeaders["User-Agent"] != "ua" {
		t.Fatalf("user agent not applied: %+v", ch.CustomHeaders)
	}
	ApplyToChannel(&ch, Bundle{ProxyURL: "http://10.0.0.1:3128"})
	if ch.ProxyURL != "http://10.0.0.1:3128" {
		t.Fatalf("proxy not replaced: %q", ch.ProxyURL)
	}
}
