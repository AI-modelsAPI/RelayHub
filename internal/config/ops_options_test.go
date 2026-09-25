package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The B4 hold (and its opt-out) and the B5 recovery probe both need operator
// knobs; a typo must fail startup instead of silently changing behaviour.
func TestStreamCommitWindowOption(t *testing.T) {
	for _, tc := range []struct {
		in       string
		window   time.Duration
		disabled bool
	}{
		{"", DefaultStreamCommitWindow, false},
		{"0", 0, true},
		{"off", 0, true},
		{" 5s ", 5 * time.Second, false},
		{"1500ms", 1500 * time.Millisecond, false},
	} {
		got, disabled, err := Config{StreamCommitWindow: tc.in}.StreamCommit()
		if err != nil || got != tc.window || disabled != tc.disabled {
			t.Errorf("StreamCommit(%q) = %v, %t, %v; want %v, %t", tc.in, got, disabled, err, tc.window, tc.disabled)
		}
	}
	if DefaultStreamCommitWindow != 10*time.Second {
		t.Fatalf("the default hold changed: %v", DefaultStreamCommitWindow)
	}
	for _, bad := range []string{"soon", "-1s", "50ms"} {
		if _, _, err := (Config{StreamCommitWindow: bad}).StreamCommit(); err == nil {
			t.Errorf("StreamCommit(%q) accepted", bad)
		}
	}
}

func TestHealthProbeIntervalOption(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"":      DefaultHealthProbeInterval,
		"off":   0,
		"0":     0,
		" 1m ":  time.Minute,
		"300ms": 300 * time.Millisecond,
	} {
		got, err := Config{HealthProbeInterval: in}.HealthProbe()
		if err != nil || got != want {
			t.Errorf("HealthProbe(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if DefaultHealthProbeInterval != 30*time.Second {
		t.Fatalf("the default recovery interval changed: %v", DefaultHealthProbeInterval)
	}
	for _, bad := range []string{"soon", "-1m", "10ms"} {
		if _, err := (Config{HealthProbeInterval: bad}).HealthProbe(); err == nil {
			t.Errorf("HealthProbe(%q) accepted", bad)
		}
	}
}

func TestOpsOptionsFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{"stream_commit_window":"2s","health_probe_interval":"1m"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d, disabled, err := c.StreamCommit(); err != nil || d != 2*time.Second || disabled {
		t.Fatalf("config.json stream_commit_window: %v %v %v", d, disabled, err)
	}
	if d, err := c.HealthProbe(); err != nil || d != time.Minute {
		t.Fatalf("config.json health_probe_interval: %v %v", d, err)
	}

	t.Setenv("RELAYHUB_STREAM_COMMIT_WINDOW", "off")
	t.Setenv("RELAYHUB_HEALTH_PROBE_INTERVAL", "off")
	ApplyEnv(&c)
	if _, disabled, err := c.StreamCommit(); err != nil || !disabled {
		t.Fatalf("env stream_commit_window: %v %v", disabled, err)
	}
	if d, err := c.HealthProbe(); err != nil || d != 0 {
		t.Fatalf("env health_probe_interval: %v %v", d, err)
	}
}
