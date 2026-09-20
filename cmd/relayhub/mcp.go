package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// runMCPStdio forwards one JSON-RPC body from stdin to the management MCP endpoint.
func runMCPStdio(managementAddr string) error {
	body, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	}
	addr := strings.TrimSpace(managementAddr)
	if addr == "" {
		addr = "127.0.0.1:8790"
	}
	url := "http://" + addr + "/api/v1/mcp"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	_, err = os.Stdout.Write(out)
	return err
}
