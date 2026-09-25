package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// runMCPStdio bridges the management MCP endpoint over stdio as a persistent
// MCP session: each newline-delimited JSON-RPC request on stdin is forwarded
// and its response written back on one line, until EOF (AUDIT RH-25: the
// previous implementation waited for full stdin EOF and served one request).
func runMCPStdio(managementAddr, token string) error {
	addr := strings.TrimSpace(managementAddr)
	if addr == "" {
		addr = "127.0.0.1:8790"
	}
	url := "http://" + addr + "/api/v1/mcp"
	// token comes from RELAYHUB_MANAGEMENT_TOKEN, config.json or
	// <data-dir>/management.token (clientManagementToken, AUDIT 2026-09-24 F4).
	token = strings.TrimSpace(token)
	client := &http.Client{Timeout: 120 * time.Second}

	forward := func(body []byte) ([]byte, error) {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("mcp: %w", err)
		}
		out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		return bytes.TrimSpace(out), nil
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	served := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		out, err := forward(line)
		if err != nil {
			return err
		}
		if len(out) == 0 {
			continue
		}
		if _, err := os.Stdout.Write(append(out, '\n')); err != nil {
			return err
		}
		served++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if served == 0 {
		// Empty stdin (e.g. `relayhub mcp < /dev/null`): keep the old one-shot
		// behaviour so the command still prints something useful.
		out, err := forward([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(append(out, '\n')); err != nil {
			return err
		}
	}
	return nil
}
