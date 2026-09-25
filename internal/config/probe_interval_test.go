package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeInterval(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"":      DefaultVerifyProbeInterval,
		"off":   0,
		"0":     0,
		"6h":    6 * time.Hour,
		" 30m ": 30 * time.Minute,
	} {
		got, err := Config{VerifyProbeInterval: in}.ProbeInterval()
		if err != nil || got != want {
			t.Errorf("ProbeInterval(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if DefaultVerifyProbeInterval != 12*time.Hour {
		t.Fatalf("default probe interval changed: %v", DefaultVerifyProbeInterval)
	}
	// Typos must fail startup, and relays must not be hammered.
	for _, bad := range []string{"soon", "5m", "-1h"} {
		if _, err := (Config{VerifyProbeInterval: bad}).ProbeInterval(); err == nil {
			t.Errorf("ProbeInterval(%q) accepted", bad)
		}
	}
}

func TestProbeIntervalFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{"verify_probe_interval":"6h"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.VerifyProbeInterval != "6h" {
		t.Fatalf("config.json value lost: %q", c.VerifyProbeInterval)
	}
	t.Setenv("RELAYHUB_VERIFY_PROBE_INTERVAL", "off")
	ApplyEnv(&c)
	if d, err := c.ProbeInterval(); err != nil || d != 0 {
		t.Fatalf("env override: %v %v", d, err)
	}
}
