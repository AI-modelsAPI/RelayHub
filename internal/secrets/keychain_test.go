package secrets

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeKeychain emulates the parts of security(1) the provider uses.
type fakeKeychain struct {
	items     map[string]string
	argv      []string
	stdin     []string
	failWrite bool
	corrupt   bool
}

func (f *fakeKeychain) run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, int, error) {
	f.argv = append(f.argv, name+" "+strings.Join(args, " "))
	if stdin != nil {
		f.stdin = append(f.stdin, string(stdin))
	}
	arg := func(list []string, flag string) string {
		for i := 0; i+1 < len(list); i++ {
			if list[i] == flag {
				return list[i+1]
			}
		}
		return ""
	}
	switch {
	case len(args) > 0 && args[0] == "find-generic-password":
		v, ok := f.items[arg(args, "-s")+"/"+arg(args, "-a")]
		if !ok {
			return nil, securityItemNotFound, nil
		}
		return []byte(v + "\n"), 0, nil
	case len(args) == 1 && args[0] == "-i":
		if f.failWrite {
			return nil, 0, nil // interactive mode may exit 0 even on failure
		}
		fields := strings.Fields(string(stdin))
		if len(fields) == 0 || fields[0] != "add-generic-password" {
			return nil, 1, nil
		}
		v := arg(fields, "-w")
		if f.corrupt {
			v = strings.Repeat("0", 64)
		}
		f.items[arg(fields, "-s")+"/"+arg(fields, "-a")] = v
		return nil, 0, nil
	}
	return nil, 1, errors.New("unexpected command")
}

func newFake() *fakeKeychain { return &fakeKeychain{items: map[string]string{}} }

func writeKey(t *testing.T, path string, key []byte) {
	t.Helper()
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatal(err)
	}
}

// AUDIT 2026-09-24 F18.
func TestKeychainProviderCreatesKeyWithoutFile(t *testing.T) {
	fk := newFake()
	path := filepath.Join(t.TempDir(), "master.key")
	p, err := NewKeychainKeyProvider(context.Background(), KeychainOptions{FilePath: path, Run: fk.run})
	if err != nil {
		t.Fatal(err)
	}
	key, err := p.Key(context.Background())
	if err != nil || len(key) != 32 {
		t.Fatalf("key %x err %v", key, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("keychain mode must not create master.key")
	}
	secret := hex.EncodeToString(key)
	for _, a := range fk.argv {
		if strings.Contains(a, secret) {
			t.Fatalf("secret leaked into argv: %q", a)
		}
	}
	if len(fk.stdin) != 1 || !strings.Contains(fk.stdin[0], secret) {
		t.Fatalf("secret should travel over stdin only: %q", fk.stdin)
	}
	// A second start reuses the stored key.
	p2, err := NewKeychainKeyProvider(context.Background(), KeychainOptions{FilePath: path, Run: fk.run})
	if err != nil {
		t.Fatal(err)
	}
	key2, _ := p2.Key(context.Background())
	if !bytes.Equal(key, key2) {
		t.Fatal("restart produced a different key")
	}
}

func TestKeychainProviderMigratesFileKey(t *testing.T) {
	fk := newFake()
	path := filepath.Join(t.TempDir(), "master.key")
	old := bytes.Repeat([]byte{7}, 32)
	writeKey(t, path, old)
	p, err := NewKeychainKeyProvider(context.Background(), KeychainOptions{FilePath: path, Run: fk.run})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := p.Key(context.Background())
	if !bytes.Equal(key, old) {
		t.Fatal("migration must keep the existing key, or every stored secret becomes unreadable")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("migrated master.key must be removed so backups stop carrying it")
	}
}

func TestKeychainProviderKeepsFileWhenWriteFails(t *testing.T) {
	for name, fk := range map[string]*fakeKeychain{
		"silent failure":  {items: map[string]string{}, failWrite: true},
		"wrong read-back": {items: map[string]string{}, corrupt: true},
	} {
		path := filepath.Join(t.TempDir(), "master.key")
		writeKey(t, path, bytes.Repeat([]byte{9}, 32))
		if _, err := NewKeychainKeyProvider(context.Background(), KeychainOptions{FilePath: path, Run: fk.run}); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: key file must survive a failed migration: %v", name, err)
		}
	}
}

func TestKeychainProviderConflict(t *testing.T) {
	fk := newFake()
	path := filepath.Join(t.TempDir(), "master.key")
	fk.items[keychainService+"/"+keychainAccount(path)] = hex.EncodeToString(bytes.Repeat([]byte{1}, 32))
	writeKey(t, path, bytes.Repeat([]byte{2}, 32))
	if _, err := NewKeychainKeyProvider(context.Background(), KeychainOptions{FilePath: path, Run: fk.run}); err == nil {
		t.Fatal("differing keys must stop startup")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("conflicting key file must be kept")
	}
	// Identical copies: the file is redundant and removed.
	writeKey(t, path, bytes.Repeat([]byte{1}, 32))
	if _, err := NewKeychainKeyProvider(context.Background(), KeychainOptions{FilePath: path, Run: fk.run}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("redundant key file should be removed")
	}
}

func TestKeychainProviderLockedKeychainFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	locked := func(context.Context, []byte, string, ...string) ([]byte, int, error) { return nil, 36, nil }
	if _, err := NewKeychainKeyProvider(context.Background(), KeychainOptions{FilePath: path, Run: locked}); err == nil {
		t.Fatal("a locked keychain must not silently create a new key")
	}
}

func TestKeychainProviderExplicitKeychainFile(t *testing.T) {
	f := newFake()
	path := filepath.Join(t.TempDir(), "master.key")
	if _, err := NewKeychainKeyProvider(context.Background(), KeychainOptions{FilePath: path, Run: f.run, Keychain: "/tmp/rh-test.keychain-db"}); err != nil {
		t.Fatal(err)
	}
	for _, argv := range f.argv {
		if strings.Contains(argv, "find-generic-password") && !strings.HasSuffix(argv, " /tmp/rh-test.keychain-db") {
			t.Fatalf("read does not target the keychain file: %q", argv)
		}
	}
	if len(f.stdin) == 0 || !strings.HasSuffix(strings.TrimSpace(f.stdin[0]), " /tmp/rh-test.keychain-db") {
		t.Fatalf("write does not target the keychain file: %q", f.stdin)
	}
	for _, bad := range []string{"/tmp/a b.keychain", "/tmp/a\"b", "/tmp/a\nadd-generic-password"} {
		if _, err := NewKeychainKeyProvider(context.Background(), KeychainOptions{FilePath: path, Run: f.run, Keychain: bad}); err == nil {
			t.Fatalf("keychain path %q must be rejected (it travels through security -i)", bad)
		}
	}
}

// A locked keychain makes security(1) wait for the unlock dialog; startup
// must give up with a clear error instead of hanging (seen on a real macOS
// runner, where the call only ended when the test's deadline killed it).
func TestKeychainProviderBoundsSecurityCalls(t *testing.T) {
	blocking := func(ctx context.Context, _ []byte, _ string, _ ...string) ([]byte, int, error) {
		<-ctx.Done()
		return nil, -1, nil // what exec reports for a killed process
	}
	start := time.Now()
	_, err := NewKeychainKeyProvider(context.Background(), KeychainOptions{FilePath: filepath.Join(t.TempDir(), "master.key"), Run: blocking, Timeout: 50 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("expected a locked-keychain timeout error, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the call was not bounded: %s", time.Since(start))
	}
}
