package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"relayhub/internal/api"
	"relayhub/internal/browser"
)

func TestBrowserAPI_GetStatus(t *testing.T) {
	// Set up fake browser via RELAYHUB_BROWSER_PATH
	tmpDir := t.TempDir()
	fakeBinary := filepath.Join(tmpDir, "fake-chrome")
	script := "#!/bin/sh\necho \"FakeChrome 999.0.0\"\n"
	if err := os.WriteFile(fakeBinary, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}

	t.Setenv("RELAYHUB_BROWSER_PATH", fakeBinary)

	srv := api.NewServer("test-token")
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GET /api/v1/browser, got %d: %s", w.Code, w.Body.String())
	}

	var resp browser.Info
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	if !resp.Available {
		t.Fatalf("expected available browser, got %+v", resp)
	}
	if resp.Path != fakeBinary {
		t.Errorf("expected path %q, got %q", fakeBinary, resp.Path)
	}
	if resp.Version != "FakeChrome 999.0.0" {
		t.Errorf("expected version FakeChrome 999.0.0, got %q", resp.Version)
	}
	if resp.Tier != 1 {
		t.Errorf("expected Tier 1, got %d", resp.Tier)
	}
}
