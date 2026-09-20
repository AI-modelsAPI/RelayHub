package browser_test

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"relayhub/internal/browser"
)

func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, signal 0 checks if process exists without killing it
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

func TestRuntime_LifecycleReap(t *testing.T) {
	rt := browser.NewRuntime()

	// Launch a couple of long-running dummy child processes (e.g. sleep 60)
	cmd1 := exec.Command("sleep", "60")
	if err := rt.StartProcess(cmd1); err != nil {
		t.Fatalf("failed to start process 1: %v", err)
	}

	cmd2 := exec.Command("sleep", "60")
	if err := rt.StartProcess(cmd2); err != nil {
		t.Fatalf("failed to start process 2: %v", err)
	}

	pid1 := cmd1.Process.Pid
	pid2 := cmd2.Process.Pid

	if !isProcessAlive(pid1) {
		t.Fatalf("expected pid1 %d to be alive", pid1)
	}
	if !isProcessAlive(pid2) {
		t.Fatalf("expected pid2 %d to be alive", pid2)
	}

	// Close runtime and reap all processes
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := rt.Close(ctx); err != nil {
		t.Fatalf("rt.Close failed: %v", err)
	}

	// Wait a tiny moment for OS to finish reaping
	time.Sleep(50 * time.Millisecond)

	if isProcessAlive(pid1) {
		t.Errorf("expected pid1 %d to be dead after Close", pid1)
	}
	if isProcessAlive(pid2) {
		t.Errorf("expected pid2 %d to be dead after Close", pid2)
	}
}
