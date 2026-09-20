package browser

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type procEntry struct {
	cmd      *exec.Cmd
	pid      int
	pgid     int
	exitDone chan struct{}
}

// Runtime manages spawned browser processes and ensures clean lifecycle disposal.
// It uses process groups (Setpgid) so that all descendants are tracked and cleanly reaped,
// with a single Wait owner and wait-for-reaping logic in Close.
type Runtime struct {
	mu        sync.Mutex
	processes map[int]*procEntry
	closed    bool
}

// NewRuntime initializes a new browser process manager Runtime.
func NewRuntime() *Runtime {
	return &Runtime{
		processes: make(map[int]*procEntry),
	}
}

// StartProcess configures the cmd into its own process group, starts it, and tracks it.
// cmd.Wait is owned exclusively by the runtime's single background goroutine.
func (r *Runtime) StartProcess(cmd *exec.Cmd) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return errors.New("browser runtime is closed")
	}

	// Place child process into its own new process group
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0

	if err := cmd.Start(); err != nil {
		return err
	}

	pid := cmd.Process.Pid
	// With Setpgid = true, pgid == pid
	pgid := pid

	entry := &procEntry{
		cmd:      cmd,
		pid:      pid,
		pgid:     pgid,
		exitDone: make(chan struct{}),
	}
	r.processes[pid] = entry

	// Single Wait owner goroutine
	go func(e *procEntry) {
		_ = e.cmd.Wait()
		// A browser leader may exit before its helpers. Keep ownership of the
		// group until those helpers are gone rather than forgetting the tree.
		killProcessGroup(e.pgid, syscall.SIGKILL)
		for isProcessGroupAlive(e.pgid) {
			time.Sleep(10 * time.Millisecond)
		}
		r.mu.Lock()
		delete(r.processes, e.pid)
		r.mu.Unlock()
		close(e.exitDone)
	}(entry)

	return nil
}

// killProcessGroup sends the given signal to the entire process group (-pgid).
func killProcessGroup(pgid int, sig syscall.Signal) {
	if pgid > 0 {
		_ = syscall.Kill(-pgid, sig)
	}
}

// isProcessGroupAlive checks if any process in the process group is still alive.
func isProcessGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	// syscall.Kill(-pgid, 0) checks if any process in the group exists
	err := syscall.Kill(-pgid, 0)
	return err == nil
}

// Close gracefully terminates all tracked process trees (SIGTERM), and if they do not
// exit within the context or a short grace period, escalates to SIGKILL on the process groups.
// It waits until the process groups are confirmed reaped or the context expires.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		entries := make([]*procEntry, 0, len(r.processes))
		for _, e := range r.processes {
			entries = append(entries, e)
		}
		r.mu.Unlock()
		for _, e := range entries {
			select {
			case <-e.exitDone:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	r.closed = true

	// Take snapshot of tracked entries
	entries := make([]*procEntry, 0, len(r.processes))
	for _, e := range r.processes {
		entries = append(entries, e)
	}
	r.mu.Unlock()

	if len(entries) == 0 {
		return nil
	}

	// 1. Send SIGTERM to entire process groups
	for _, e := range entries {
		killProcessGroup(e.pgid, syscall.SIGTERM)
	}

	// 2. Wait for process groups to terminate, or escalate to SIGKILL
	gracePeriod := 1 * time.Second
	graceTimer := time.NewTimer(gracePeriod)
	defer graceTimer.Stop()

	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()

	for {
		// Check if all process groups and Wait owners are finished
		allDead := true
		for _, e := range entries {
			select {
			case <-e.exitDone:
			default:
				allDead = false
			}
			if isProcessGroupAlive(e.pgid) {
				allDead = false
			}
		}

		if allDead {
			r.mu.Lock()
			r.processes = make(map[int]*procEntry)
			r.mu.Unlock()
			return nil
		}

		select {
		case <-ctx.Done():
			// Forced SIGKILL on timeout / cancellation
			for _, e := range entries {
				killProcessGroup(e.pgid, syscall.SIGKILL)
			}
			// The single Wait owner continues reaping; do not forget unfinished
			// groups or consume a one-shot timer repeatedly across entries.
			return ctx.Err()

		case <-graceTimer.C:
			// Grace period expired, escalate to SIGKILL
			for _, e := range entries {
				killProcessGroup(e.pgid, syscall.SIGKILL)
			}

		case <-ticker.C:
			// Next check iteration
		}
	}
}

// StopProcess reaps only this owned command, leaving the shared runtime usable.
func (r *Runtime) StopProcess(ctx context.Context, cmd *exec.Cmd) error {
	r.mu.Lock()
	var entry *procEntry
	if cmd.Process != nil {
		entry = r.processes[cmd.Process.Pid]
	}
	if entry == nil || entry.cmd != cmd {
		r.mu.Unlock()
		return nil
	}
	killProcessGroup(entry.pgid, syscall.SIGTERM)
	r.mu.Unlock()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-entry.exitDone:
		return nil
	case <-ctx.Done():
		killProcessGroup(entry.pgid, syscall.SIGKILL)
		return ctx.Err()
	case <-timer.C:
		killProcessGroup(entry.pgid, syscall.SIGKILL)
	}
	select {
	case <-entry.exitDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ActiveProcesses returns the count of currently running processes.
func (r *Runtime) ActiveProcesses() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.processes)
}
