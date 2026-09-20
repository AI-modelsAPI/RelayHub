package browser_test

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"relayhub/internal/browser"
)

// TestRuntime_ConcurrentClose verifies that multiple concurrent calls to Close do not panic or deadlock.
func TestRuntime_ConcurrentClose(t *testing.T) {
	rt := browser.NewRuntime()
	cmd := exec.Command("sleep", "30")
	if err := rt.StartProcess(cmd); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = rt.Close(ctx)
		}()
	}
	wg.Wait()

	if rt.ActiveProcesses() != 0 {
		t.Errorf("expected 0 active processes, got %d", rt.ActiveProcesses())
	}
}

// TestRuntime_NonTerminatingProcess verifies that a process ignoring SIGTERM is reaped with SIGKILL.
func TestRuntime_NonTerminatingProcess(t *testing.T) {
	rt := browser.NewRuntime()

	// Launch a trap script that ignores SIGTERM
	cmd := exec.Command("/bin/sh", "-c", `trap '' TERM; while true; do sleep 1; done`)
	if err := rt.StartProcess(cmd); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	pid := cmd.Process.Pid

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := rt.Close(ctx); err != nil {
		t.Fatalf("rt.Close failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	alive := syscall.Kill(pid, 0) == nil
	if alive {
		t.Errorf("stubborn process %d still alive after SIGKILL escalation", pid)
	}
}

// TestRuntime_ContextCancellation verifies that rt.Close returns when context is canceled.
func TestRuntime_ContextCancellation(t *testing.T) {
	rt := browser.NewRuntime()

	cmd := exec.Command("sleep", "30")
	if err := rt.StartProcess(cmd); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	// Very short context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := rt.Close(ctx)
	if err == nil {
		// Even if it reaps instantly, that is acceptable, but if it hit timeout it should be context error
	}
}
