package docker_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestDockerManagementAddrRED reproduces B6:
// When RELAYHUB_MANAGEMENT_ADDR is set in the environment, Core should listen on that address,
// and healthcheck.sh should succeed probing it, while the old/default address (e.g. 127.0.0.1:8790)
// should NOT be listened on by this process.
func TestDockerManagementAddrRED(t *testing.T) {
	// Find a free TCP port on 127.0.0.1
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate free port: %v", err)
	}
	customPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	customAddr := fmt.Sprintf("127.0.0.1:%d", customPort)

	// Build relayhub binary for testing
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "relayhub")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/relayhub")
	buildCmd.Dir = filepath.Join("..", "..")
	buildCmd.Env = append(os.Environ(), "PATH="+os.Getenv("PATH"))
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build relayhub: %v\nOutput: %s", err, string(out))
	}

	dataDir := filepath.Join(tmpDir, "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Run relayhub with RELAYHUB_MANAGEMENT_ADDR=customAddr
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "-full-stack", "-data-dir", dataDir)
	cmd.Env = append(os.Environ(),
		"RELAYHUB_MANAGEMENT_ADDR="+customAddr,
	)
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start relayhub: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = cmd.Wait()
		}
	}()

	// Wait up to 3 seconds for server to be up
	probeURL := fmt.Sprintf("http://%s/healthz", customAddr)
	client := http.Client{Timeout: 500 * time.Millisecond}

	healthy := false
	for i := 0; i < 30; i++ {
		resp, err := client.Get(probeURL)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 && string(body) != "" {
				healthy = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !healthy {
		out, _ := io.ReadAll(outPipe)
		t.Fatalf("RED reproduction: relayhub did NOT listen on customAddr %s (RELAYHUB_MANAGEMENT_ADDR not passed to core listener). Output: %s", customAddr, string(out))
	}
}
