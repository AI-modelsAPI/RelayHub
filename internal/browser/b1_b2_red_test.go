package browser_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"relayhub/internal/browser"
)

func TestProfileDir_CollisionResistance(t *testing.T) {
	dir := t.TempDir()

	// Case 1: Slash in ID vs sanitized segment
	a, err := browser.ProfileDir(dir, "provider", "a/b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := browser.ProfileDir(dir, "provider", filepath.Base(a))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == b {
		t.Errorf("expected distinct profile dirs for 'a/b' and %q, but got collision %q", filepath.Base(a), a)
	}

	// Case 2: ID with surrounding whitespace vs trimmed ID
	c, err := browser.ProfileDir(dir, "provider", "account")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d, err := browser.ProfileDir(dir, "provider", " account ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == d {
		t.Errorf("expected distinct profile dirs for 'account' and ' account ', but got collision %q", c)
	}

	// Case 3: Case-folding collision on macOS/APFS
	// e.g. "myAccount" vs "myaccount" should not collide in a destructive way
	e, err := browser.ProfileDir(dir, "provider", "myAccount")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, err := browser.ProfileDir(dir, "provider", "myaccount")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(e) == filepath.Base(f) {
		t.Errorf("expected distinct segment for 'myAccount' and 'myaccount' to protect case-insensitive FS, got %q", e)
	}
}

func TestRuntime_ProcessTreeReaping(t *testing.T) {
	rt := browser.NewRuntime()
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "child.pid")

	cmd := exec.Command("/bin/sh", "-c", `sleep 60 & child=$!; printf '%s' "$child" > "$1"; wait`, "probe", pidfile)
	if err := rt.StartProcess(cmd); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pidfile)
		if err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
			if pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("child pid was not written")
	}
	defer syscall.Kill(pid, syscall.SIGKILL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := rt.Close(ctx); err != nil {
		t.Fatalf("rt.Close failed: %v", err)
	}

	if rt.ActiveProcesses() != 0 {
		t.Errorf("expected 0 active processes, got %d", rt.ActiveProcesses())
	}

	// Verify descendant is dead
	time.Sleep(50 * time.Millisecond)
	alive := syscall.Kill(pid, 0) == nil
	if alive {
		t.Errorf("descendant process %d is still alive after rt.Close()", pid)
	}
}
