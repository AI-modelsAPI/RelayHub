package browser

import (
	"strings"
	"testing"
)

// AUDIT 2026-09-24 F15: DevTools must not listen on a TCP port that any
// local process could attach to; the browser is driven over fds 3/4.
func TestLaunchArgsUseDevToolsPipe(t *testing.T) {
	args, err := launchArgs(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--remote-debugging-pipe") {
		t.Fatalf("launch args must use the DevTools pipe: %v", args)
	}
	if strings.Contains(joined, "--remote-debugging-port") || strings.Contains(joined, "--remote-debugging-address") {
		t.Fatalf("launch args must not open a DevTools TCP port: %v", args)
	}
}
