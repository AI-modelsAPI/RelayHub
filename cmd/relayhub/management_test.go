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

func TestListenAddressValidation(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8787", "0.0.0.0:8787", ":8787", "[::]:8788", "localhost:8789", "192.168.1.4:8789"} {
		if err := validateListenAddress("gateway", addr); err != nil {
			t.Errorf("%q: %v", addr, err)
		}
	}
	for _, addr := range []string{"", "8787", "127.0.0.1:0", "127.0.0.1:65536", "example.com:8787", "http://127.0.0.1:8787", "127.0.0.1:bad"} {
		if err := validateListenAddress("gateway", addr); err == nil || !strings.Contains(err.Error(), "gateway") {
			t.Errorf("expected gateway validation error for %q, got %v", addr, err)
		}
	}
}

// Data-plane env overrides must be rejected before any state is written, like
// the management address is; an invalid RELAYHUB_GATEWAY_ADDR must not start
// a half-configured stack.
func TestRunRejectsInvalidDataPlaneAddressBeforeWriting(t *testing.T) {
	for _, env := range []string{"RELAYHUB_HTTP_PROXY_ADDR", "RELAYHUB_SOCKS5_ADDR", "RELAYHUB_GATEWAY_ADDR"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "example.com:99999")
			dir := filepath.Join(t.TempDir(), "untouched")
			err := run([]string{"-data-dir", dir, "-full-stack"})
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("expected invalid listen address error, got %v", err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("invalid invocation wrote data: %v", err)
			}
		})
	}
}

func TestEnvOrTreatsEmptyAsUnset(t *testing.T) {
	t.Setenv("RELAYHUB_GATEWAY_ADDR", "")
	if got := envOr("RELAYHUB_GATEWAY_ADDR", "fallback"); got != "fallback" {
		t.Fatalf("empty env should fall back, got %q", got)
	}
	t.Setenv("RELAYHUB_GATEWAY_ADDR", " 0.0.0.0:8789 ")
	if got := envOr("RELAYHUB_GATEWAY_ADDR", "fallback"); got != "0.0.0.0:8789" {
		t.Fatalf("env should be trimmed, got %q", got)
	}
}
