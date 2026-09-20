package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync"
)

var (
	ErrNotFound               = errors.New("secret not found")
	ErrKeyProviderUnavailable = errors.New("secret key provider unavailable")
	ErrInvalidKey             = errors.New("invalid secret key")
	ErrInvalidCipher          = errors.New("invalid encrypted secret")
	ErrBackend                = errors.New("secret backend unavailable")
)

type KeyProvider interface {
	Key(context.Context) ([]byte, error)
}

type StaticKeyProvider struct{ key []byte }

func NewStaticKeyProvider(key []byte) (*StaticKeyProvider, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	return &StaticKeyProvider{key: append([]byte(nil), key...)}, nil
}

func (p *StaticKeyProvider) Key(context.Context) ([]byte, error) {
	if p == nil || len(p.key) != 32 {
		return nil, ErrInvalidKey
	}
	return append([]byte(nil), p.key...), nil
}

// Backend is the persistence seam for encrypted secret envelopes. Implementations
// store ciphertext only; the Store never passes plaintext to a backend.
type Backend interface {
	Load(context.Context, string) ([]byte, error)
	Save(context.Context, string, []byte) error
	Delete(context.Context, string) error
	ListRefs(context.Context) ([]string, error)
}

// PlatformKeyProvider is the seam for native keychain providers. Secret
// operations fail closed when the platform backend reports unavailable.
type PlatformKeyProvider interface {
	KeyProvider
	Available(context.Context) error
}

type MemoryBackend struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func NewMemoryBackend() *MemoryBackend { return &MemoryBackend{values: map[string][]byte{}} }
func (b *MemoryBackend) Load(_ context.Context, ref string) ([]byte, error) {
	b.mu.RLock()
	v, ok := b.values[ref]
	b.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}
func (b *MemoryBackend) Save(_ context.Context, ref string, value []byte) error {
	b.mu.Lock()
	b.values[ref] = append([]byte(nil), value...)
	b.mu.Unlock()
	return nil
}
func (b *MemoryBackend) Delete(_ context.Context, ref string) error {
	b.mu.Lock()
	delete(b.values, ref)
	b.mu.Unlock()
	return nil
}

func (b *MemoryBackend) ListRefs(_ context.Context) ([]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	refs := make([]string, 0, len(b.values))
	for k := range b.values {
		refs = append(refs, k)
	}
	return refs, nil
}

type Store struct {
	provider KeyProvider
	backend  Backend

	// values is retained as a compatibility/testing view for the default
	// in-memory store. Durable callers should provide a Backend.
	mu     sync.RWMutex
	values map[string][]byte
}

// NewStore intentionally has no backend. Production callers must use
// NewProductionStore or NewStoreWithBackend; accidental in-memory storage
// therefore fails closed instead of losing credentials on restart.
func NewStore(p KeyProvider) *Store {
	return &Store{provider: p, values: map[string][]byte{}}
}

// NewTestStore creates the non-durable backend intended for isolated tests.
func NewTestStore(p KeyProvider) *Store {
	return NewStoreWithBackend(p, NewMemoryBackend())
}

// NewProductionStore composes encrypted storage with the durable SQL backend.
func NewProductionStore(p KeyProvider, db *sql.DB) (*Store, error) {
	backend, err := NewSQLBackend(db)
	if err != nil {
		return nil, err
	}
	return NewStoreWithBackend(p, backend), nil
}

func NewStoreWithBackend(p KeyProvider, backend Backend) *Store {
	return &Store{provider: p, backend: backend, values: map[string][]byte{}}
}

func (s *Store) Put(ctx context.Context, ref string, plaintext []byte) error {
	if ref == "" {
		return errors.New("secret reference must not be empty")
	}
	if s == nil || s.provider == nil || s.backend == nil {
		return ErrBackend
	}
	sealed, err := s.seal(ctx, ref, plaintext)
	if err != nil {
		return err
	}
	if err := s.backend.Save(ctx, ref, sealed); err != nil {
		return err
	}
	s.mu.Lock()
	s.values[ref] = append([]byte(nil), sealed...)
	s.mu.Unlock()
	return nil
}

func (s *Store) Get(ctx context.Context, ref string) ([]byte, error) {
	if ref == "" {
		return nil, ErrNotFound
	}
	if s == nil || s.provider == nil || s.backend == nil {
		return nil, ErrBackend
	}
	sealed, err := s.backend.Load(ctx, ref)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: %q", ErrNotFound, ref)
		}
		return nil, err
	}
	return s.open(ctx, ref, sealed)
}

func (s *Store) Delete(ctx context.Context, ref string) error {
	if ref == "" {
		return errors.New("secret reference must not be empty")
	}
	if s == nil || s.backend == nil {
		return ErrBackend
	}
	if err := s.checkProvider(ctx); err != nil {
		return err
	}
	if err := s.backend.Delete(ctx, ref); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.values, ref)
	s.mu.Unlock()
	return nil
}

// ListAll returns a map of all decrypted secrets in the store.
func (s *Store) ListAll(ctx context.Context) (map[string][]byte, error) {
	if s == nil || s.provider == nil || s.backend == nil {
		return nil, ErrBackend
	}
	refs, err := s.backend.ListRefs(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(refs))
	for _, ref := range refs {
		val, err := s.Get(ctx, ref)
		if err != nil {
			return nil, err
		}
		out[ref] = val
	}
	return out, nil
}

func (s *Store) seal(ctx context.Context, ref string, plaintext []byte) ([]byte, error) {
	key, err := s.loadKey(ctx)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	// Binding the reference prevents a ciphertext copied to another reference
	// from being accepted as a different credential.
	return gcm.Seal(nonce, nonce, plaintext, []byte(ref)), nil
}

func (s *Store) open(ctx context.Context, ref string, sealed []byte) ([]byte, error) {
	key, err := s.loadKey(ctx)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrInvalidCipher
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte(ref))
	if err != nil {
		return nil, fmt.Errorf("%w: authentication failed", ErrInvalidCipher)
	}
	return plain, nil
}

func (s *Store) loadKey(ctx context.Context) ([]byte, error) {
	if err := s.checkProvider(ctx); err != nil {
		return nil, err
	}
	key, err := s.provider.Key(ctx)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Store) checkProvider(ctx context.Context) error {
	if s == nil || s.provider == nil {
		return ErrKeyProviderUnavailable
	}
	if platform, ok := s.provider.(PlatformKeyProvider); ok {
		if err := platform.Available(ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrKeyProviderUnavailable, err)
		}
	}
	return nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidKey
	}
	return cipher.NewGCM(block)
}

func clear(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// SQLBackend is the durable adapter seam for SQLite. It stores only encrypted
// envelopes; callers must use Store rather than writing plaintext directly.
type SQLBackend struct{ DB *sql.DB }

func NewSQLBackend(db *sql.DB) (*SQLBackend, error) {
	if db == nil {
		return nil, ErrBackend
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS secret_values (
		ref TEXT PRIMARY KEY NOT NULL, ciphertext BLOB NOT NULL
	)`); err != nil {
		return nil, err
	}
	return &SQLBackend{DB: db}, nil
}

func (b *SQLBackend) Load(ctx context.Context, ref string) ([]byte, error) {
	if b == nil || b.DB == nil {
		return nil, ErrBackend
	}
	var value []byte
	if err := b.DB.QueryRowContext(ctx, `SELECT ciphertext FROM secret_values WHERE ref=?`, ref).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return value, nil
}

func (b *SQLBackend) Save(ctx context.Context, ref string, value []byte) error {
	if b == nil || b.DB == nil {
		return ErrBackend
	}
	_, err := b.DB.ExecContext(ctx, `INSERT INTO secret_values(ref,ciphertext) VALUES(?,?)
		ON CONFLICT(ref) DO UPDATE SET ciphertext=excluded.ciphertext`, ref, value)
	return err
}

func (b *SQLBackend) Delete(ctx context.Context, ref string) error {
	if b == nil || b.DB == nil {
		return ErrBackend
	}
	_, err := b.DB.ExecContext(ctx, `DELETE FROM secret_values WHERE ref=?`, ref)
	return err
}

func (b *SQLBackend) ListRefs(ctx context.Context) ([]string, error) {
	if b == nil || b.DB == nil {
		return nil, ErrBackend
	}
	rows, err := b.DB.QueryContext(ctx, `SELECT ref FROM secret_values ORDER BY ref ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}
