package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeArchive zips a probe-able fake shell under the expected inner name.
func fakeArchive(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("chrome-headless-shell-" + runtimeVersion + "/chrome-headless-shell")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const fakeShellScript = "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"chrome-headless-shell " + runtimeVersion + "\"; fi\n"

func TestDownloadRuntimeChecksumEnforced(t *testing.T) {
	plat := hostPlatform()
	archive := fakeArchive(t, fakeShellScript)
	sumBytes := sha256.Sum256(archive)
	sum := hex.EncodeToString(sumBytes[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()
	dataDir := t.TempDir()

	// RED path: wrong checksum must fail and leave nothing behind.
	wrong := map[platform]string{plat: sum[:len(sum)-2] + "aa"}
	if _, err := DownloadRuntime(context.Background(), dataDir, DownloadSource{BaseURL: srv.URL, Client: srv.Client()}, wrong); err == nil {
		t.Fatal("mismatched checksum must be rejected")
	}
	if _, err := os.Stat(RuntimePath(dataDir)); !os.IsNotExist(err) {
		t.Fatal("rejected download must not leave artifacts")
	}

	// GREEN path: pinned checksum installs and probeVersion succeeds.
	info, err := DownloadRuntime(context.Background(), dataDir, DownloadSource{BaseURL: srv.URL, Client: srv.Client()}, map[platform]string{plat: sum})
	if err != nil {
		t.Fatalf("pinned download failed: %v", err)
	}
	if !info.Available || info.Tier != 2 || info.Version == "" {
		t.Fatalf("unexpected info: %+v", info)
	}
	// Idempotency: second call returns the same path without network.
	info2, err := DownloadRuntime(context.Background(), dataDir, DownloadSource{BaseURL: "http://127.0.0.1:1", Client: srv.Client()}, nil)
	if err != nil || info2.Path != info.Path {
		t.Fatalf("re-download must be idempotent: %+v %v", info2, err)
	}
}

func TestDownloadedRuntimeMissing(t *testing.T) {
	got := DownloadedRuntime(t.TempDir())
	if got.Available || got.Tier != 2 {
		t.Fatalf("empty data dir must report unavailable Tier 2, got %+v", got)
	}
}

func TestRuntimePathScopedToDataDir(t *testing.T) {
	if RuntimePath("/a") == RuntimePath("/b") {
		t.Fatal("runtime path must be scoped to data dir")
	}
	if filepath.Base(filepath.Dir(RuntimePath("/x"))) != "chrome-headless-shell-"+runtimeVersion {
		t.Fatal("versioned directory required")
	}
}
