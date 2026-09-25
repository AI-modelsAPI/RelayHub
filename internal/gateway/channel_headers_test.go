package gateway

import (
	"net/http"
	"testing"
)

// AUDIT 2026-09-24 F8: channels can now drop forwarded SDK fingerprint
// headers; default forwarding is unchanged.
func TestApplyChannelHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Stainless-Os", "MacOS")
	h.Set("X-Stainless-Arch", "arm64")
	h.Set("X-Stainless-Runtime-Version", "v22.1.0")
	h.Set("User-Agent", "claude-cli/1.0")
	h.Set("Anthropic-Beta", "tools-2024")
	h.Set("Content-Type", "application/json")
	applyChannelHeaders(h, map[string]string{
		"x-stainless-*":  "",
		"X-Stainless-Os": "Linux",
		"Anthropic-Beta": "",
		"*":              "",
		"X-Custom":       "v",
	})
	if h.Get("X-Stainless-Arch") != "" || h.Get("X-Stainless-Runtime-Version") != "" {
		t.Fatalf("wildcard strip failed: %v", h)
	}
	if h.Get("X-Stainless-Os") != "Linux" || h.Get("X-Custom") != "v" {
		t.Fatalf("pinned/custom headers missing: %v", h)
	}
	if h.Get("Anthropic-Beta") != "" {
		t.Fatalf("empty value should remove the header: %v", h)
	}
	if h.Get("User-Agent") != "claude-cli/1.0" || h.Get("Content-Type") != "application/json" {
		t.Fatalf("unrelated headers changed: %v", h)
	}

	plain := http.Header{}
	plain.Set("X-Stainless-Os", "MacOS")
	applyChannelHeaders(plain, nil)
	if plain.Get("X-Stainless-Os") != "MacOS" {
		t.Fatal("default behaviour must keep forwarding SDK headers")
	}
}
