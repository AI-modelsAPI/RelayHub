package browser

// Tier 2 runtime download (BROWSER-PLAN §3): when no system Chromium exists,
// fetch the pinned chrome-headless-shell into <dataDir>/runtime after user
// consent, verify its SHA-256, and expose it like a Tier 1 browser.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// runtimeVersion is pinned for reproducible behavior (BROWSER-PLAN §3:
// "版本号写死在配置里可复现"); upgrades are deliberate edits, not whatever
// latest is at download time.
const runtimeVersion = "153.0.8010.36"

// maxRuntimeBytes bounds the download (plan-specified ~94-114MB archives).
const maxRuntimeBytes = 300 << 20

type platform struct{ os, arch string }

func hostPlatform() platform { return platform{runtime.GOOS, runtime.GOARCH} }

// archivePath mirrors the Chrome for Testing layout:
// <version>/chrome-headless-shell-<platform>.zip
func archivePath(p platform) string {
	switch p.os {
	case "darwin":
		if p.arch == "arm64" {
			return "chrome-headless-shell-mac-arm64.zip"
		}
		return "chrome-headless-shell-mac-x64.zip"
	case "linux":
		return "chrome-headless-shell-linux64.zip"
	case "windows":
		return "chrome-headless-shell-win64.zip"
	}
	return ""
}

// pinnedChecksums holds the exact SHA-256 per platform; an empty entry means
// "not provisioned" and download refuses rather than installing unverified
// bytes. Values come from the CfT last-known-good JSON at pin time.
var pinnedChecksums = map[platform]string{
	{"darwin", "arm64"}:  "",
	{"darwin", "amd64"}:  "",
	{"linux", "amd64"}:   "",
	{"windows", "amd64"}: "",
}

// RuntimePath returns where the Tier 2 executable lives for a data dir.
func RuntimePath(dataDir string) string {
	name := "chrome-headless-shell"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dataDir, "runtime", "chrome-headless-shell-"+runtimeVersion, name)
}

// DownloadedRuntime reports a previously installed Tier 2 runtime, if any.
func DownloadedRuntime(dataDir string) Info {
	p := RuntimePath(dataDir)
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return Info{Tier: 2, Error: "runtime not downloaded"}
	}
	ver, err := probeVersion(p)
	if err != nil {
		return Info{Tier: 2, Path: p, Error: fmt.Sprintf("downloaded runtime unusable: %v", err)}
	}
	return Info{Available: true, Path: p, Version: ver, Tier: 2}
}

// DownloadRuntime installs the pinned runtime for the host platform from
// src.BaseURL (override for tests). checksums must contain an entry for the
// host platform; mismatch aborts without leaving artifacts. It never touches
// the install directory, only <dataDir>/runtime.
func DownloadRuntime(ctx context.Context, dataDir string, src DownloadSource, checksums map[platform]string) (Info, error) {
	if existing := DownloadedRuntime(dataDir); existing.Available {
		return existing, nil
	}
	p := hostPlatform()
	want := checksums[p]
	if checksums == nil || want == "" {
		want = pinnedChecksums[p]
	}
	if want == "" {
		return Info{}, fmt.Errorf("no pinned checksum for %s/%s; refusing unverified runtime", p.os, p.arch)
	}
	archive := archivePath(p)
	if archive == "" {
		return Info{}, fmt.Errorf("unsupported platform %s/%s", p.os, p.arch)
	}
	url := fmt.Sprintf("%s/%s/%s", src.BaseURL, runtimeVersion, archive)
	if src.Client == nil {
		src.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Info{}, err
	}
	resp, err := src.Client.Do(req)
	if err != nil {
		return Info{}, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("download failed: HTTP %d from %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRuntimeBytes))
	if err != nil {
		return Info{}, fmt.Errorf("read download: %w", err)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != want {
		return Info{}, fmt.Errorf("checksum mismatch: got %s want %s", got, want)
	}
	dest := RuntimePath(dataDir)
	if err := extractShell(body, dest); err != nil {
		return Info{}, err
	}
	ver, err := probeVersion(dest)
	if err != nil {
		return Info{}, fmt.Errorf("installed runtime failed --version: %w", err)
	}
	return Info{Available: true, Path: dest, Version: ver, Tier: 2}, nil
}

// extractShell unpacks only the flat chrome-headless-shell binary from the
// CfT zip layout into dest, atomically (write .part, rename).
func extractShell(body []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return fmt.Errorf("invalid runtime archive: %w", err)
	}
	name := "chrome-headless-shell"
	if hostPlatform().os == "windows" {
		name += ".exe"
	}
	var exe *zip.File
	for _, f := range zr.File {
		if filepath.Base(f.Name) == name {
			exe = f
			break
		}
	}
	if exe == nil {
		return fmt.Errorf("runtime archive lacks %s", name)
	}
	rc, err := exe.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// DownloadSource abstracts where archives come from; production points at the
// Chrome for Testing CDN, tests use httptest servers.
type DownloadSource struct {
	BaseURL string
	Client  *http.Client
}
