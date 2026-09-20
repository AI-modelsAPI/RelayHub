package auth

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSQLKeyBackendNeverStoresRawKey(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backend, err := NewSQLKeyBackend(db)
	if err != nil {
		t.Fatal(err)
	}
	service := NewLocalKeyServiceWithBackend(backend)
	key, err := service.Create()
	if err != nil || !service.Validate(key) {
		t.Fatalf("create/validate: %v", err)
	}
	var id string
	var hash []byte
	if err := db.QueryRow(`SELECT id,key_hash FROM local_api_keys`).Scan(&id, &hash); err != nil {
		t.Fatal(err)
	}
	if id == key || string(hash) == key {
		t.Fatal("raw key stored")
	}
	service.Revoke(key)
	if service.Validate(key) {
		t.Fatal("revoked key accepted")
	}
}
