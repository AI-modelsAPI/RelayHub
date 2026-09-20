package browser_test

import (
	"path/filepath"
	"strings"
	"testing"

	"relayhub/internal/browser"
)

func TestProfileDir(t *testing.T) {
	dataDir := t.TempDir()

	// Normal case
	dir1, err := browser.ProfileDir(dataDir, "providerA", "channel1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dir2, err := browser.ProfileDir(dataDir, "providerA", "channel2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dir1 == dir2 {
		t.Errorf("expected different profile dirs for different channels, got same: %q", dir1)
	}

	baseProfiles := filepath.Join(dataDir, "runtime", "profiles")
	if !strings.HasPrefix(dir1, baseProfiles) {
		t.Errorf("expected %q to start with %q", dir1, baseProfiles)
	}

	// Path escape / sanitization cases
	dangerousCases := []struct {
		provider string
		channel  string
	}{
		{"../../etc", "passwd"},
		{"providerA", "../../../etc/shadow"},
		{"prov/ider", "chan/nel"},
		{"prov\\ider", "chan\\nel"},
		{"..", ".."},
		{"", "chan"},
		{"prov", ""},
	}

	for _, tc := range dangerousCases {
		res, err := browser.ProfileDir(dataDir, tc.provider, tc.channel)
		if err != nil {
			// Returning an error on unsafe input is valid
			continue
		}
		// If it succeeded, verify it is strictly within dataDir/runtime/profiles
		baseProfiles := filepath.Join(dataDir, "runtime", "profiles")
		rel, err := filepath.Rel(baseProfiles, res)
		if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
			t.Errorf("path escape detected for provider=%q channel=%q: result=%q rel=%q", tc.provider, tc.channel, res, rel)
		}
	}
}
