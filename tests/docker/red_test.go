package docker_test

import (
	"context"
	"encoding/json"
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

// buildRelayHub compiles the real binary once per test into a temp dir.
func buildRelayHub(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "relayhub")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/relayhub")
	buildCmd.Dir = filepath.Join("..", "..")
	buildCmd.Env = append(os.Environ(), "PATH="+os.Getenv("PATH"))
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build relayhub: %v\nOutput: %s", err, string(out))
	}
	return binPath
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitHealthy(t *testing.T, mgmtAddr string, out io.Reader) {
	t.Helper()
	probeURL := fmt.Sprintf("http://%s/healthz", mgmtAddr)
	client := http.Client{Timeout: 500 * time.Millisecond}
	for i := 0; i < 30; i++ {
		resp, err := client.Get(probeURL)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 && string(body) != "" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	logs, _ := io.ReadAll(out)
	t.Fatalf("relayhub did NOT become healthy on %s. Output: %s", mgmtAddr, string(logs))
}

// TestDockerManagementAddrRED reproduces B6:
// When RELAYHUB_MANAGEMENT_ADDR is set in the environment, Core should listen on that address,
// and healthcheck.sh should succeed probing it, while the old/default address (e.g. 127.0.0.1:8790)
// should NOT be listened on by this process.
func TestDockerManagementAddrRED(t *testing.T) {
	binPath := buildRelayHub(t)
	customAddr := freeLoopbackAddr(t)

	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}

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

	waitHealthy(t, customAddr, outPipe)
}

// TestDataPlaneListenAddrEnvOverrides is the container-reachability guard for
// AUDIT RH-04: the proxy and gateway listeners were hardwired to loopback, so
// `docker run -p 8789:8789` published ports that nothing inside the container
// answered on. The env overrides must move every data-plane listener, and the
// management API must advertise a dialable (non-wildcard) gateway address.
func TestDataPlaneListenAddrEnvOverrides(t *testing.T) {
	binPath := buildRelayHub(t)
	mgmt := freeLoopbackAddr(t)
	httpProxy := freeLoopbackAddr(t)
	socks := freeLoopbackAddr(t)
	gateway := freeLoopbackAddr(t)
	_, gwPort, _ := net.SplitHostPort(gateway)

	dataDir := filepath.Join(t.TempDir(), "data")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "-full-stack", "-data-dir", dataDir)
	cmd.Env = append(os.Environ(),
		"RELAYHUB_MANAGEMENT_ADDR="+mgmt,
		"RELAYHUB_HTTP_PROXY_ADDR="+httpProxy,
		"RELAYHUB_SOCKS5_ADDR="+socks,
		// Wildcard host, the shape a container deployment uses.
		"RELAYHUB_GATEWAY_ADDR=0.0.0.0:"+gwPort,
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
	waitHealthy(t, mgmt, outPipe)

	// Both proxies accept TCP on their configured addresses.
	for _, addr := range []string{httpProxy, socks} {
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatalf("proxy listener not reachable on configured address %s: %v", addr, err)
		}
		_ = c.Close()
	}

	// The gateway answers on the wildcard-bound port; without a key that is a
	// 401 envelope, which proves the listener is the gateway and not a proxy.
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + gwPort + "/v1/models")
	if err != nil {
		t.Fatalf("gateway not reachable on configured port %s: %v", gwPort, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 from unauthenticated gateway, got %d", resp.StatusCode)
	}

	// Management settings must advertise a dialable gateway address, not the
	// wildcard the socket was bound to (CLI sync writes this into configs).
	sresp, err := client.Get("http://" + mgmt + "/api/v1/settings")
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	defer sresp.Body.Close()
	var settings struct {
		GatewayAddress string `json:"gateway_address"`
	}
	if err := json.NewDecoder(sresp.Body).Decode(&settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if want := "127.0.0.1:" + gwPort; settings.GatewayAddress != want {
		t.Fatalf("advertised gateway address = %q, want %q", settings.GatewayAddress, want)
	}
}
