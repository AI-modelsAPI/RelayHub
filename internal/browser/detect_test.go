package browser_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"relayhub/internal/browser"
)

func TestDetect_RealSystemChrome(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("real Chrome test only runs on darwin in this environment")
	}
	realChrome := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(realChrome); os.IsNotExist(err) {
		t.Skip("real Chrome binary not found on this machine")
	}

	info := browser.Detect()
	if !info.Available {
		t.Fatalf("expected browser to be available, got unavailable (err: %v)", info.Error)
	}
	if info.Path != realChrome {
		t.Errorf("expected path %q, got %q", realChrome, info.Path)
	}
	if info.Tier != 1 {
		t.Errorf("expected Tier 1, got %d", info.Tier)
	}
	if info.Version == "" {
		t.Errorf("expected non-empty Version")
	}
}

func TestDetect_EnvVarOverride(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBinary := filepath.Join(tmpDir, "fake-chrome")
	script := "#!/bin/sh\necho \"FakeChrome 123.456.789\"\n"
	if err := os.WriteFile(fakeBinary, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}

	t.Setenv("RELAYHUB_BROWSER_PATH", fakeBinary)

	info := browser.Detect()
	if !info.Available {
		t.Fatalf("expected browser to be available, got err: %v", info.Error)
	}
	if info.Path != fakeBinary {
		t.Errorf("expected path %q, got %q", fakeBinary, info.Path)
	}
	if info.Tier != 1 {
		t.Errorf("expected Tier 1, got %d", info.Tier)
	}
	if info.Version != "FakeChrome 123.456.789" {
		t.Errorf("expected 'FakeChrome 123.456.789', got %q", info.Version)
	}
}
