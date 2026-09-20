package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsUnsafeManagementBeforeWriting(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8790", "192.0.2.1:8790", "localhost:bad", "127.0.0.1:99999", "http://127.0.0.1:8790", ""} {
		t.Run(addr, func(t *testing.T) {
			t.Setenv("RELAYHUB_MANAGEMENT_ADDR", addr)
			dir := filepath.Join(t.TempDir(), "untouched")
			err := run([]string{"-data-dir", dir, "unexpected-positional"})
			if err == nil {
				t.Fatal("expected invalid invocation to fail before startup")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("invalid invocation wrote data: %v", err)
			}
		})
	}
}

func TestManagementAddressValidation(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8790", "[::1]:8790", "127.0.0.2:12345", "localhost:8790"} {
		if err := validateManagementAddress(addr); err != nil {
			t.Errorf("%q: %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:8790", "[::]:8790", "127.0.0.1:0", "127.0.0.1:65536", "localhost:bad", "https://localhost:8790", ""} {
		if err := validateManagementAddress(addr); err == nil || !strings.Contains(err.Error(), "management") {
			t.Errorf("expected management validation error for %q, got %v", addr, err)
		}
	}
}
