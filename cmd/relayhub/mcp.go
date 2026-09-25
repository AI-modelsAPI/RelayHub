package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// runMCPStdio bridges the management MCP endpoint over stdio as a persistent
// MCP session: each newline-delimited JSON-RPC request on stdin is forwarded
// and its response written back on one line, until EOF (AUDIT RH-25: the
// previous implementation waited for full stdin EOF and served one request).
//
// token is asked for on every request (clientManagementToken reads
// RELAYHUB_MANAGEMENT_TOKEN, config.json or <data-dir>/management.token,
// AUDIT 2026-09-24 F4), so a bridge started before RelayHub generated its
// token, or across a token change, keeps working.
func runMCPStdio(managementAddr string, token func() string, stdin io.Reader, stdout io.Writer) error {
	addr := strings.TrimSpace(managementAddr)
	if addr == "" {
		addr = "127.0.0.1:8790"
	}
	url := "http://" + addr + "/api/v1/mcp"
	client := &http.Client{Timeout: 120 * time.Second}

	forward := func(body []byte) ([]byte, error) {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if t := strings.TrimSpace(token()); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("mcp: %w", err)
		}
		out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			return mcpAuthError(body), nil
		}
		return bytes.TrimSpace(out), nil
	}

	scanner := bufio.NewScanner(stdin)
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
		if _, err := stdout.Write(append(out, '\n')); err != nil {
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
		if _, err := stdout.Write(append(out, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// mcpAuthError answers a request the management API refused with a JSON-RPC
// error the MCP client can show, instead of relaying an HTTP error body that
// is not JSON-RPC. Notifications (no id) get no answer.
func mcpAuthError(request []byte) []byte {
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(request, &req) != nil || len(req.ID) == 0 || string(req.ID) == "null" {
		return nil
	}
	out, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"error": map[string]any{
			"code":    -32001,
			"message": "RelayHub rejected the management token. Point RELAYHUB_DATA_DIR at the RelayHub data directory (it holds management.token) or set RELAYHUB_MANAGEMENT_TOKEN.",
		},
	})
	return out
}
