package secrets

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestStoreRoundTripAndDelete(t *testing.T) {
	p, err := NewStaticKeyProvider(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewTestStore(p)
	if err := s.Put(context.Background(), "x", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	v, err := s.Get(context.Background(), "x")
	if err != nil || string(v) != "secret" {
		t.Fatalf("%q %v", v, err)
	}
	v[0] = 'X'
	v2, err := s.Get(context.Background(), "x")
	if err != nil || string(v2) != "secret" {
		t.Fatalf("stored value was mutated: %q %v", v2, err)
	}
	if err = s.Delete(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(context.Background(), "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing secret, got %v", err)
	}
}

func TestStoreWrongKeyAndTamperRejected(t *testing.T) {
	p, _ := NewStaticKeyProvider(make([]byte, 32))
	s := NewTestStore(p)
	if err := s.Put(context.Background(), "x", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	wrong, _ := NewStaticKeyProvider(bytesOf(1, 32))
	s.provider = wrong
	if _, err := s.Get(context.Background(), "x"); err == nil {
		t.Fatal("expected wrong-key rejection")
	}
	s.provider = p
	backend := s.backend.(*MemoryBackend)
	backend.mu.Lock()
	backend.values["x"][len(backend.values["x"])-1] ^= 1
	backend.mu.Unlock()
	if _, err := s.Get(context.Background(), "x"); err == nil {
		t.Fatal("expected tamper rejection")
	}
}

func TestStoreBindsCiphertextToReference(t *testing.T) {
	p, _ := NewStaticKeyProvider(make([]byte, 32))
	s := NewTestStore(p)
	if err := s.Put(context.Background(), "first", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	backend := s.backend.(*MemoryBackend)
	backend.mu.Lock()
	sealed := append([]byte(nil), backend.values["first"]...)
	backend.values["second"] = sealed
	backend.mu.Unlock()
	if _, err := s.Get(context.Background(), "second"); err == nil {
		t.Fatal("expected reference swap rejection")
	}
}

func TestFileKeyProviderCreatesSecureStableKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	p, err := NewFileKeyProvider(path)
	if err != nil {
		t.Fatal(err)
	}
	key1, err := p.Key(context.Background())
	if err != nil || len(key1) != 32 {
		t.Fatalf("key: %d bytes, %v", len(key1), err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("key permissions = %o, want 600", info.Mode().Perm())
	}
	p2, err := NewFileKeyProvider(path)
	if err != nil {
		t.Fatal(err)
	}
	key2, err := p2.Key(context.Background())
	if err != nil || string(key1) != string(key2) {
		t.Fatal("key was not stable across providers")
	}
}

func TestFileKeyProviderRejectsInsecureOrMalformedFile(t *testing.T) {
	if _, err := NewFileKeyProvider("relative-secret.key"); err == nil {
		t.Fatal("expected relative key path rejection")
	}
	if _, err := NewFileKeyProvider(t.TempDir()); err == nil {
		t.Fatal("expected directory key path rejection")
	}
	path := filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(path, []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileKeyProvider(path); err == nil {
		t.Fatal("expected malformed key rejection")
	}
	if err := os.WriteFile(path, bytesOf(0, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileKeyProvider(path); err == nil {
		t.Fatal("expected insecure permission rejection")
	}
}

func TestNewStoreFailsClosedWithoutDurableBackend(t *testing.T) {
	provider, _ := NewStaticKeyProvider(make([]byte, 32))
	if err := NewStore(provider).Put(context.Background(), "x", []byte("secret")); !errors.Is(err, ErrBackend) {
		t.Fatalf("default store error = %v, want durable-backend failure", err)
	}
}

func TestStoreUsesDurableBackend(t *testing.T) {
	p, _ := NewStaticKeyProvider(make([]byte, 32))
	backend := NewMemoryBackend()
	s := NewStoreWithBackend(p, backend)
	if err := s.Put(context.Background(), "x", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	s2 := NewStoreWithBackend(p, backend)
	got, err := s2.Get(context.Background(), "x")
	if err != nil || string(got) != "secret" {
		t.Fatalf("durable backend round trip: %q %v", got, err)
	}
}

func TestProductionStoreUsesSQLBackend(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	provider, _ := NewStaticKeyProvider(bytesOf(9, 32))
	store, err := NewProductionStore(provider, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "production", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM secret_values`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("production store did not persist secret: %d rows", count)
	}
}

func TestUnavailablePlatformKeyProviderFailsClosed(t *testing.T) {
	store := NewStoreWithBackend(unavailablePlatformKeyProvider{}, NewMemoryBackend())
	if err := store.Put(context.Background(), "x", []byte("secret")); !errors.Is(err, ErrKeyProviderUnavailable) {
		t.Fatalf("Put error = %v, want unavailable provider", err)
	}
}

type unavailablePlatformKeyProvider struct{}

func (unavailablePlatformKeyProvider) Key(context.Context) ([]byte, error) {
	return nil, ErrKeyProviderUnavailable
}

func (unavailablePlatformKeyProvider) Available(context.Context) error {
	return ErrKeyProviderUnavailable
}

func TestSQLBackendPersistsCiphertextOnly(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backend, err := NewSQLBackend(db)
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := NewStaticKeyProvider(bytesOf(7, 32))
	store := NewStoreWithBackend(provider, backend)
	if err := store.Put(context.Background(), "credential-1", []byte("plaintext-secret")); err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	if err := db.QueryRow(`SELECT ciphertext FROM secret_values WHERE ref=?`, "credential-1").Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if string(ciphertext) == "plaintext-secret" || len(ciphertext) <= 12 {
		t.Fatalf("invalid ciphertext persisted: %x", ciphertext)
	}
	store2 := NewStoreWithBackend(provider, backend)
	got, err := store2.Get(context.Background(), "credential-1")
	if err != nil || string(got) != "plaintext-secret" {
		t.Fatalf("durable round trip: %q %v", got, err)
	}

	all, err := store2.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}
	if len(all) != 1 || string(all["credential-1"]) != "plaintext-secret" {
		t.Fatalf("unexpected ListAll result: %v", all)
	}
}

func bytesOf(value byte, count int) []byte {
	b := make([]byte, count)
	for i := range b {
		b[i] = value
	}
	return b
}
