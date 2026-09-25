package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// AUDIT 2026-09-24 F10: custom auth headers were echoed verbatim by the
// management API and MCP list_channels returned raw proxy passwords.
func TestChannelSecretsMaskedInAPIAndMCPAndPreservedOnEcho(t *testing.T) {
	h, repo := identityTestServer(t)
	ctx := context.Background()
	ch, _ := repo.GetChannel(ctx, "c1")
	ch.CustomHeaders = map[string]string{"Authorization": "Bearer upstream-secret", "X-Session-Token": "tok-123", "User-Agent": "ua/1"}
	if err := repo.UpdateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}

	w := request(t, h, http.MethodGet, "/api/v1/channels/c1", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	for _, leak := range []string{"upstream-secret", "tok-123", "s3cret"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("GET /channels leaked %q: %s", leak, w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), "ua/1") {
		t.Fatalf("non-sensitive header should stay visible: %s", w.Body.String())
	}

	// GET-modify-PUT round trip keeps every stored secret.
	var got struct {
		Channel map[string]any `json:"channel"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	got.Channel["remark"] = "edited"
	body, _ := json.Marshal(got.Channel)
	if w := request(t, h, http.MethodPut, "/api/v1/channels/c1", "", string(body)); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	after, _ := repo.GetChannel(ctx, "c1")
	if after.ProxyURL != "socks5://alice:s3cret@127.0.0.1:1080" {
		t.Fatalf("masked proxy echo overwrote stored proxy: %q", after.ProxyURL)
	}
	if after.CustomHeaders["Authorization"] != "Bearer upstream-secret" || after.CustomHeaders["X-Session-Token"] != "tok-123" {
		t.Fatalf("masked header echo overwrote stored headers: %+v", after.CustomHeaders)
	}
	if after.Remark != "edited" {
		t.Fatalf("edit not applied: %+v", after)
	}

	rpc := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_channels","arguments":{}}}`
	w = request(t, h, http.MethodPost, "/api/v1/mcp", "", rpc)
	if w.Code != http.StatusOK {
		t.Fatalf("mcp: %d %s", w.Code, w.Body.String())
	}
	for _, leak := range []string{"upstream-secret", "tok-123", "s3cret"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("MCP list_channels leaked %q: %s", leak, w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), "c1") {
		t.Fatalf("MCP list_channels returned no channels: %s", w.Body.String())
	}
}
