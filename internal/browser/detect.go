package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Info describes the status of the browser runtime.
type Info struct {
	Available bool   `json:"available"`
	Path      string `json:"path"`
	Version   string `json:"version"`
	Tier      int    `json:"tier"` // 1: system installed, 2: downloaded shell, 3: manual fallback
	Error     string `json:"error,omitempty"`
}

// macOSSearchPaths lists candidate browser binary paths on darwin.
var macOSSearchPaths = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
}

// macOSUserRelativePaths lists paths relative to user's home directory on darwin.
var macOSUserRelativePaths = []string{
	"Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	"Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	"Applications/Chromium.app/Contents/MacOS/Chromium",
}

// linuxLookPathCandidates lists candidate binaries to search via exec.LookPath on linux.
var linuxLookPathCandidates = []string{
	"google-chrome",
	"google-chrome-stable",
	"chromium",
	"chromium-browser",
	"microsoft-edge",
}

// Detect locates a usable Chromium-based browser according to Tier 1 strategy.
// Priority:
// 1. RELAYHUB_BROWSER_PATH env var
// 2. Platform-specific known locations (macOS / Linux)
func Detect() Info {
	if envPath := os.Getenv("RELAYHUB_BROWSER_PATH"); envPath != "" {
		if ver, err := probeVersion(envPath); err == nil {
			return Info{
				Available: true,
				Path:      envPath,
				Version:   ver,
				Tier:      1,
			}
		} else {
			return Info{
				Available: false,
				Path:      envPath,
				Tier:      1,
				Error:     fmt.Sprintf("specified RELAYHUB_BROWSER_PATH invalid or failed --version: %v", err),
			}
		}
	}

	candidates := candidatePaths()
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			if ver, err := probeVersion(p); err == nil {
				return Info{
					Available: true,
					Path:      p,
					Version:   ver,
					Tier:      1,
				}
			}
		}
	}

	return Info{
		Available: false,
		Tier:      3, // Fallback to manual if no browser detected
		Error:     "no chromium-based browser detected on system",
	}
}

func candidatePaths() []string {
	var candidates []string
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, macOSSearchPaths...)
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			for _, rel := range macOSUserRelativePaths {
				candidates = append(candidates, filepath.Join(home, rel))
			}
		}
	} else if runtime.GOOS == "linux" {
		for _, name := range linuxLookPathCandidates {
			if path, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, path)
			}
		}
	}
	return candidates
}

func probeVersion(binaryPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New(strings.TrimSpace(string(out)) + " (" + err.Error() + ")")
	}
	return strings.TrimSpace(string(out)), nil
}
