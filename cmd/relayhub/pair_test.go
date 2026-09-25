package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relayhub/internal/api"
	"relayhub/internal/app"
)

// AUDIT 2026-09-24 F4: local clients find the token without it being put in
// their own configuration files.
func TestClientManagementTokenPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RELAYHUB_MANAGEMENT_TOKEN", "")
	t.Setenv("RELAYHUB_MANAGEMENT_AUTH", "")
	if tok, err := clientManagementToken(dir); err != nil || tok != "" {
		t.Fatalf("nothing configured: %q %v", tok, err)
	}
	if err := os.WriteFile(filepath.Join(dir, app.ManagementTokenFile), []byte("file-token-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, _ := clientManagementToken(dir); tok != "file-token-0123456789" {
		t.Fatalf("file token: %q", tok)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"management_token":"config-token-0123456789"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, _ := clientManagementToken(dir); tok != "config-token-0123456789" {
		t.Fatalf("config token should beat the file: %q", tok)
	}
	t.Setenv("RELAYHUB_MANAGEMENT_TOKEN", "env-token-0123456789")
	if tok, _ := clientManagementToken(dir); tok != "env-token-0123456789" {
		t.Fatalf("env token should win: %q", tok)
	}
	t.Setenv("RELAYHUB_MANAGEMENT_TOKEN", "")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"management_auth":"off"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, _ := clientManagementToken(dir); tok != "" {
		t.Fatalf("auth off: no token should be sent, got %q", tok)
	}
}

func TestRunPairPrintsOneTimeLink(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RELAYHUB_MANAGEMENT_TOKEN", "")
	const token = "pair-test-token-0123456789"
	if err := os.WriteFile(filepath.Join(dir, app.ManagementTokenFile), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := api.NewConfiguredServer(api.Config{LocalOnly: true, Management: "127.0.0.1:8790", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var out, errOut bytes.Buffer
	if err := runPair(addr, dir, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	link := strings.TrimSpace(out.String())
	if !strings.HasPrefix(link, "http://"+addr+"/#pair=") || strings.Contains(link, token) {
		t.Fatalf("pairing link %q", link)
	}
	if !strings.Contains(errOut.String(), "expires") {
		t.Fatalf("expected an expiry note, got %q", errOut.String())
	}

	// A wrong token is reported clearly instead of printing a dead link.
	if err := os.WriteFile(filepath.Join(dir, app.ManagementTokenFile), []byte("wrong-token-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runPair(addr, dir, &out, &errOut); err == nil || !strings.Contains(err.Error(), "rejected the token") || out.Len() != 0 {
		t.Fatalf("wrong token: err=%v out=%q", err, out.String())
	}
}
