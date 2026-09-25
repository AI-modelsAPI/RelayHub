package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AUDIT 2026-09-24 F15: the debugging port is chosen by Chrome and read from
// the private profile dir instead of being pre-picked (TOCTOU squatting).
func TestReadDevToolsActivePort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "DevToolsActivePort")
	if _, err := readDevToolsActivePort(path); err == nil {
		t.Fatal("missing file must be an error")
	}
	if err := os.WriteFile(path, []byte("53117\n/devtools/browser/0b1c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if port, err := readDevToolsActivePort(path); err != nil || port != 53117 {
		t.Fatalf("port=%d err=%v", port, err)
	}
	for _, bad := range []string{"", "0\n", "70000\n", "abc\n"} {
		_ = os.WriteFile(path, []byte(bad), 0o600)
		if _, err := readDevToolsActivePort(path); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	args, err := launchArgs(0, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "--remote-debugging-port=0") {
		t.Fatalf("launch args must let Chrome pick its port: %v", args)
	}
}
