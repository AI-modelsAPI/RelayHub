package auth

import (
	"strings"
	"testing"
)

func TestKeyLifecycleDoesNotStoreRawKey(t *testing.T) {
	s := NewLocalKeyService()
	key, err := s.Create()
	if err != nil || key == "" {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "rh_") {
		t.Fatalf("unexpected key prefix: %q", key)
	}
	if !s.Validate(key) {
		t.Fatal("new key is not valid")
	}
	if s.containsRawForTest(key) {
		t.Fatal("raw key stored in service")
	}
	if s.Validate(key + "x") {
		t.Fatal("modified key is valid")
	}
	s.Revoke(key)
	if s.Validate(key) {
		t.Fatal("revoked key is valid")
	}
}

func TestKeyServiceRejectsMalformedKeys(t *testing.T) {
	s := NewLocalKeyService()
	for _, key := range []string{"", "rh_", "not-a-key", "rh_!!!!", "Bearer rh_x"} {
		if s.Validate(key) {
			t.Fatalf("malformed key accepted: %q", key)
		}
	}
}

func TestKeyServiceUsesHashStoreBackend(t *testing.T) {
	backend := NewMemoryKeyBackend()
	s := NewLocalKeyServiceWithBackend(backend)
	key, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	s2 := NewLocalKeyServiceWithBackend(backend)
	if !s2.Validate(key) {
		t.Fatal("key not valid after service recreation")
	}
	s2.Revoke(key)
	if s.Validate(key) {
		t.Fatal("revoked key remains valid")
	}
}
