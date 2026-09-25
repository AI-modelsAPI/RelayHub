package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"relayhub/internal/api"
	"relayhub/internal/app"
	"relayhub/internal/config"
)

// clientManagementToken finds the credential a local client (relayhub pair,
// relayhub mcp) presents to the management API: RELAYHUB_MANAGEMENT_TOKEN,
// then management_token from config.json, then <data-dir>/management.token
// (AUDIT 2026-09-24 F4). An empty result means "send no token", which is
// right when the operator disabled management authentication.
func clientManagementToken(dataDir string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("RELAYHUB_MANAGEMENT_TOKEN")); v != "" {
		return v, nil
	}
	if cfg, err := config.Load(dataDir); err == nil {
		config.ApplyEnv(&cfg)
		if t := strings.TrimSpace(cfg.ManagementToken); t != "" {
			return t, nil
		}
		if strings.EqualFold(strings.TrimSpace(cfg.ManagementAuth), "off") {
			return "", nil
		}
	}
	b, err := os.ReadFile(filepath.Join(dataDir, app.ManagementTokenFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read management token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// runPair prints a fresh one-time console pairing link.
func runPair(managementAddr, dataDir string, stdout, stderr io.Writer) error {
	addr := strings.TrimSpace(managementAddr)
	if addr == "" {
		addr = config.DefaultManagementAddr
	}
	token, err := clientManagementToken(dataDir)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/v1/auth/pair-codes", nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("RelayHub is not reachable at %s: %w", addr, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusCreated:
	case http.StatusConflict:
		return fmt.Errorf("management authentication is disabled (management_auth=off); open http://%s/ directly", addr)
	case http.StatusUnauthorized:
		return fmt.Errorf("the management API rejected the token; check RELAYHUB_MANAGEMENT_TOKEN, management_token in config.json, or %s", filepath.Join(dataDir, app.ManagementTokenFile))
	default:
		return fmt.Errorf("pairing failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.URL == "" {
		return fmt.Errorf("pairing failed: unexpected response %q", strings.TrimSpace(string(body)))
	}
	fmt.Fprintln(stdout, out.URL)
	fmt.Fprintf(stderr, "Open the link above in your browser to pair the RelayHub console. It works once and expires at %s.\n", out.ExpiresAt)
	return nil
}

// logManagementAccess tells the operator how to reach the console after
// startup: a one-time pairing link when authentication is on, a loud warning
// when it was turned off.
func logManagementAccess(rt *app.Runtime) {
	if rt == nil || rt.APIServer == nil || rt.APIListener == nil {
		return
	}
	addr := rt.APIListener.Addr().String()
	if rt.ManagementTokenSource == "off" {
		log.Printf("relayhub: WARNING management API authentication is disabled (management_auth=off): any local process or user can read and change this RelayHub at http://%s/", addr)
		return
	}
	code, exp, err := rt.APIServer.NewPairingCode()
	if err != nil {
		log.Printf("relayhub: could not create a console pairing link: %v", err)
		return
	}
	where := "management_token / RELAYHUB_MANAGEMENT_TOKEN"
	if rt.ManagementTokenSource != "config" {
		where = rt.ManagementTokenSource
	}
	log.Printf("relayhub: management console: %s (one-time pairing link, valid until %s; run `relayhub pair` for a new one; token: %s)",
		api.PairingURL(addr, code), exp.Local().Format("15:04:05"), where)
}
