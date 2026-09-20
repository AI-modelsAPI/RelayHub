package app_test

import (
	"context"
	"testing"

	"relayhub/internal/app"
	"relayhub/internal/buildinfo"
)

func newTestApp(t *testing.T) *app.App {
	t.Helper()
	a, err := app.New(app.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("app.New returned error: %v", err)
	}
	return a
}

// New must reject an empty data directory rather than guessing a location.
func TestNewRequiresDataDir(t *testing.T) {
	if _, err := app.New(app.Config{}); err == nil {
		t.Fatal("expected error when DataDir is empty, got nil")
	}
}

// Constructing the app must not open any listeners; that only happens on Start.
func TestNoListenersUntilStart(t *testing.T) {
	a := newTestApp(t)
	if a.Running() {
		t.Fatal("app should not be running immediately after New")
	}
	if got := a.Listeners(); got != 0 {
		t.Fatalf("expected 0 listeners before Start, got %d", got)
	}
}

// Start followed by Shutdown must return cleanly and toggle the running flag.
func TestStartThenShutdownReturnsCleanly(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !a.Running() {
		t.Fatal("app should be running after Start")
	}

	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	if a.Running() {
		t.Fatal("app should not be running after Shutdown")
	}
}

// Starting an already-started app must be reported, not silently ignored.
func TestDoubleStartFails(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("first Start returned error: %v", err)
	}
	defer func() { _ = a.Shutdown(ctx) }()

	if err := a.Start(ctx); err == nil {
		t.Fatal("expected error on second Start, got nil")
	}
}

// Shutdown must be safe to call before Start (idempotent, no panic).
func TestShutdownBeforeStartIsSafe(t *testing.T) {
	a := newTestApp(t)
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown before Start returned error: %v", err)
	}
}

// The version/build information exposed by the app must be populated from
// build defaults so a future version endpoint has data to report.
func TestVersionInfoPopulatedFromBuildDefaults(t *testing.T) {
	a := newTestApp(t)
	info := a.Info()

	if info.Name == "" {
		t.Error("build info Name should not be empty")
	}
	if info.Version == "" {
		t.Error("build info Version should not be empty")
	}
	if info.GoVersion == "" {
		t.Error("build info GoVersion should not be empty")
	}

	def := buildinfo.Get()
	if info.Version != def.Version {
		t.Errorf("app version %q does not match buildinfo default %q", info.Version, def.Version)
	}
	if info.Name != def.Name {
		t.Errorf("app name %q does not match buildinfo default %q", info.Name, def.Name)
	}
}
