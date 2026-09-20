package browser_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"relayhub/internal/browser"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRuntimeLeaderExitRetainsDescendant(t *testing.T) {
	rt := browser.NewRuntime()
	pidfile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command("/bin/sh", "-c", `sleep 60 & printf '%s' "$!" > "$1"; exit 0`, "probe", pidfile)
	if err := rt.StartProcess(cmd); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	var pid int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(pidfile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		if pid > 0 && syscall.Kill(cmd.Process.Pid, 0) != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatal("child did not become ready")
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rt.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(pid, 0) == nil {
		// Capture identity evidence before failing: distinguishes a genuine
		// survivor from a reaped-then-reused PID (kill(pid,0) hits any process).
		out, psErr := exec.Command("ps", "-o", "pid=,ppid=,stat=,command=", "-p", strconv.Itoa(pid)).CombinedOutput()
		t.Fatalf("Close returned success after leader exit but descendant remains alive: ps(%d)=%q err=%v", pid, strings.TrimSpace(string(out)), psErr)
	}
}
