package secrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// CommandRunner runs name with args, feeding stdin, and returns stdout and
// the exit code. err is reserved for failures to run the command at all.
type CommandRunner func(ctx context.Context, stdin []byte, name string, args ...string) (stdout []byte, exitCode int, err error)

// KeychainOptions configure a KeychainKeyProvider.
type KeychainOptions struct {
	// FilePath is the legacy <data-dir>/master.key. An existing file is
	// migrated into the keychain and removed once the keychain copy has been
	// read back successfully.
	FilePath string
	// SecurityPath defaults to /usr/bin/security.
	SecurityPath string
	// Run defaults to exec; tests inject a fake keychain.
	Run CommandRunner
	// Keychain is a keychain file to use instead of the default search list
	// (the login keychain). Tests point it at a throwaway keychain; it must
	// not contain whitespace or quotes, since it travels through security -i.
	Keychain string
}

const (
	keychainService = "relayhub.master-key"
	// security(1) exits with 44 when the item does not exist.
	securityItemNotFound = 44
)

// KeychainKeyProvider keeps the installation key in the macOS login keychain
// instead of next to the database (AUDIT 2026-09-24 F18). Backups and sync
// folders then carry the key only inside the keychain, which is encrypted
// with the user's login password, rather than as a plaintext file beside the
// ciphertext it protects.
//
// The secret is written through `security -i` so it never appears in a
// process argument list (argv is world-readable on macOS).
type KeychainKeyProvider struct {
	account string
	path    string
	secPath string
	run     CommandRunner
	// keychain is an explicit keychain file; empty means the search list.
	keychain string

	mu  sync.Mutex
	key []byte
}

// NewKeychainKeyProvider loads the key from the keychain, migrating
// FilePath into it or creating a fresh key when neither exists.
func NewKeychainKeyProvider(ctx context.Context, opts KeychainOptions) (*KeychainKeyProvider, error) {
	path := filepath.Clean(opts.FilePath)
	if opts.FilePath == "" || !filepath.IsAbs(path) {
		return nil, errors.New("keychain key provider: master key path must be absolute")
	}
	if strings.ContainsAny(opts.Keychain, " \t\r\n\"'\\") {
		return nil, errors.New("keychain key provider: keychain path must not contain whitespace, quotes or backslashes")
	}
	p := &KeychainKeyProvider{
		account:  keychainAccount(path),
		path:     path,
		secPath:  opts.SecurityPath,
		run:      opts.Run,
		keychain: opts.Keychain,
	}
	if p.secPath == "" {
		p.secPath = "/usr/bin/security"
	}
	if p.run == nil {
		p.run = execRunner
	}
	if err := p.init(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// keychainAccount derives a stable, whitespace-free account name per data
// directory so several installations can coexist.
func keychainAccount(path string) string {
	sum := sha256.Sum256([]byte(filepath.Dir(path)))
	return "datadir-" + hex.EncodeToString(sum[:8])
}

func (p *KeychainKeyProvider) init(ctx context.Context) error {
	stored, found, err := p.read(ctx)
	if err != nil {
		return err
	}
	fileKey, fileErr := readKeyFile(p.path)
	fileExists := fileErr == nil
	if fileErr != nil && !errors.Is(fileErr, fs.ErrNotExist) {
		return fmt.Errorf("keychain key provider: legacy key file: %w", fileErr)
	}

	switch {
	case found && fileExists:
		if subtle.ConstantTimeCompare(stored, fileKey) != 1 {
			return fmt.Errorf("keychain key provider: %s and the keychain item %q differ; refusing to guess which one encrypts the database", p.path, p.account)
		}
		p.key = stored
		return removeKeyFile(p.path)
	case found:
		p.key = stored
		return nil
	}

	key := fileKey
	if !fileExists {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return err
		}
	}
	if err := p.write(ctx, key); err != nil {
		return err
	}
	back, found, err := p.read(ctx)
	if err != nil {
		return err
	}
	if !found || subtle.ConstantTimeCompare(back, key) != 1 {
		return errors.New("keychain key provider: the key written to the keychain could not be read back; the key file was left in place")
	}
	p.key = key
	if fileExists {
		return removeKeyFile(p.path)
	}
	return nil
}

func (p *KeychainKeyProvider) Key(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.key) != 32 {
		return nil, ErrInvalidKey
	}
	return append([]byte(nil), p.key...), nil
}

// Available reports whether the provider holds a usable key.
func (p *KeychainKeyProvider) Available(ctx context.Context) error {
	_, err := p.Key(ctx)
	return err
}

func (p *KeychainKeyProvider) read(ctx context.Context) ([]byte, bool, error) {
	args := []string{"find-generic-password", "-s", keychainService, "-a", p.account, "-w"}
	if p.keychain != "" {
		args = append(args, p.keychain)
	}
	out, code, err := p.run(ctx, nil, p.secPath, args...)
	if err != nil {
		return nil, false, fmt.Errorf("keychain key provider: %w", err)
	}
	if code == securityItemNotFound {
		return nil, false, nil
	}
	if code != 0 {
		return nil, false, fmt.Errorf("keychain key provider: security find-generic-password exited with %d (is the login keychain locked?)", code)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(out)))
	if err != nil || len(key) != 32 {
		return nil, false, fmt.Errorf("%w: keychain item %q does not hold a 256-bit key", ErrInvalidKey, p.account)
	}
	return key, true, nil
}

func (p *KeychainKeyProvider) write(ctx context.Context, key []byte) error {
	cmd := fmt.Sprintf("add-generic-password -U -s %s -a %s -l RelayHub -w %s", keychainService, p.account, hex.EncodeToString(key))
	if p.keychain != "" {
		cmd += " " + p.keychain
	}
	cmd += "\n"
	_, code, err := p.run(ctx, []byte(cmd), p.secPath, "-i")
	if err != nil {
		return fmt.Errorf("keychain key provider: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("keychain key provider: security add-generic-password exited with %d", code)
	}
	return nil
}

func readKeyFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := validateKeyFile(info); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: key file must contain 32 bytes", ErrInvalidKey)
	}
	return key, nil
}

func removeKeyFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("keychain key provider: key migrated but %s could not be removed: %w", path, err)
	}
	return nil
}

func execRunner(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.Bytes(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return nil, -1, err
	}
	return stdout.Bytes(), 0, nil
}
