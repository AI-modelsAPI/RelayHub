package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"sync"
)

var (
	ErrInvalidKey = errors.New("invalid local api key")
	errKeyExists  = errors.New("local api key id already exists")
)

type KeyBackend interface {
	Save(string, [32]byte) error
	Delete(string) error
	Lookup(string) ([32]byte, bool, error)
	List() ([]string, error)
}

type MemoryKeyBackend struct {
	mu   sync.RWMutex
	keys map[string][32]byte
}

func NewMemoryKeyBackend() *MemoryKeyBackend { return &MemoryKeyBackend{keys: map[string][32]byte{}} }
func (b *MemoryKeyBackend) Save(id string, hash [32]byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.keys[id]; exists {
		return errKeyExists
	}
	b.keys[id] = hash
	return nil
}
func (b *MemoryKeyBackend) Delete(id string) error {
	b.mu.Lock()
	delete(b.keys, id)
	b.mu.Unlock()
	return nil
}
func (b *MemoryKeyBackend) Lookup(id string) ([32]byte, bool, error) {
	b.mu.RLock()
	hash, ok := b.keys[id]
	b.mu.RUnlock()
	return hash, ok, nil
}
func (b *MemoryKeyBackend) List() ([]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ids := make([]string, 0, len(b.keys))
	for id := range b.keys {
		ids = append(ids, id)
	}
	return ids, nil
}

type LocalKeyService struct {
	mu      sync.RWMutex
	backend KeyBackend
}

func NewLocalKeyService() *LocalKeyService {
	return NewLocalKeyServiceWithBackend(NewMemoryKeyBackend())
}
func NewLocalKeyServiceWithBackend(backend KeyBackend) *LocalKeyService {
	return &LocalKeyService{backend: backend}
}

func (s *LocalKeyService) Create() (string, error) {
	if s == nil || s.backend == nil {
		return "", ErrInvalidKey
	}
	for range 3 {
		rawBytes := make([]byte, 32)
		if _, err := rand.Read(rawBytes); err != nil {
			return "", err
		}
		raw := "rh_" + base64.RawURLEncoding.EncodeToString(rawBytes)
		id := raw[:11]
		if err := s.backend.Save(id, sha256.Sum256([]byte(raw))); err != nil {
			if errors.Is(err, errKeyExists) {
				continue
			}
			return "", err
		}
		return raw, nil
	}
	return "", errors.New("could not allocate unique local api key")
}

// Revoke deletes a key by raw value or by rh_ id. Persistence failures are
// returned so callers cannot report "revoked" for a key that still validates
// (AUDIT RH-30).
func (s *LocalKeyService) Revoke(key string) error {
	if s == nil || s.backend == nil {
		return ErrInvalidKey
	}
	if id, ok := keyID(key); ok {
		return s.backend.Delete(id)
	}
	// Also allow revoking by key ID directly (e.g. from management API)
	if len(key) == 11 && key[:3] == "rh_" {
		return s.backend.Delete(key)
	}
	return ErrInvalidKey
}

func (s *LocalKeyService) List() ([]string, error) {
	if s == nil || s.backend == nil {
		return nil, ErrInvalidKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backend.List()
}

func (s *LocalKeyService) Validate(key string) bool {
	if s == nil || s.backend == nil {
		return false
	}
	id, ok := keyID(key)
	if !ok {
		return false
	}
	stored, found, err := s.backend.Lookup(id)
	if err != nil || !found {
		return false
	}
	computed := sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare(stored[:], computed[:]) == 1
}

func keyID(key string) (string, bool) {
	if len(key) != 46 || key[:3] != "rh_" {
		return "", false
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(key[3:]); err != nil || len(decoded) != 32 {
		return "", false
	}
	return key[:11], true
}

// containsRawForTest exists only to make the no-plaintext-storage invariant
// directly testable without exposing internal backend representation publicly.
func (s *LocalKeyService) containsRawForTest(key string) bool {
	id, ok := keyID(key)
	if !ok {
		return false
	}
	if b, ok := s.backend.(*MemoryKeyBackend); ok {
		b.mu.RLock()
		defer b.mu.RUnlock()
		_, rawStored := b.keys[key]
		_, idStored := b.keys[id]
		return rawStored || !idStored
	}
	return false
}

// SQLKeyBackend persists only a key identifier and SHA-256 digest. The raw
// bearer key is returned once by Create and is never written to SQLite.
type SQLKeyBackend struct{ DB *sql.DB }

func NewSQLKeyBackend(db *sql.DB) (*SQLKeyBackend, error) {
	if db == nil {
		return nil, ErrInvalidKey
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS local_api_keys (
		id TEXT PRIMARY KEY NOT NULL, key_hash BLOB NOT NULL
	)`); err != nil {
		return nil, err
	}
	return &SQLKeyBackend{DB: db}, nil
}

func (b *SQLKeyBackend) Save(id string, hash [32]byte) error {
	if b == nil || b.DB == nil {
		return ErrInvalidKey
	}
	_, err := b.DB.Exec(`INSERT INTO local_api_keys(id,key_hash) VALUES(?,?)`, id, hash[:])
	return err
}
func (b *SQLKeyBackend) Delete(id string) error {
	if b == nil || b.DB == nil {
		return ErrInvalidKey
	}
	_, err := b.DB.Exec(`DELETE FROM local_api_keys WHERE id=?`, id)
	return err
}
func (b *SQLKeyBackend) Lookup(id string) ([32]byte, bool, error) {
	if b == nil || b.DB == nil {
		return [32]byte{}, false, ErrInvalidKey
	}
	var bytes []byte
	err := b.DB.QueryRow(`SELECT key_hash FROM local_api_keys WHERE id=?`, id).Scan(&bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return [32]byte{}, false, nil
	}
	if err != nil {
		return [32]byte{}, false, err
	}
	if len(bytes) != 32 {
		return [32]byte{}, false, ErrInvalidKey
	}
	var hash [32]byte
	copy(hash[:], bytes)
	return hash, true, nil
}

func (b *SQLKeyBackend) List() ([]string, error) {
	if b == nil || b.DB == nil {
		return nil, ErrInvalidKey
	}
	rows, err := b.DB.Query(`SELECT id FROM local_api_keys WHERE revoked_at IS NULL ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
