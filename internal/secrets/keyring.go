package secrets

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// FileKeyProvider loads or creates a 256-bit installation key. The file is
// created with mode 0600 and existing files must remain regular, private files.
type FileKeyProvider struct {
	Path string
}

func NewFileKeyProvider(path string) (*FileKeyProvider, error) {
	if path == "" {
		return nil, errors.New("key path must not be empty")
	}
	p := &FileKeyProvider{Path: filepath.Clean(path)}
	if filepath.IsAbs(p.Path) == false {
		return nil, errors.New("key path must be absolute")
	}
	if p.Path == string(filepath.Separator) || filepath.Base(p.Path) == "." || filepath.Base(p.Path) == ".." {
		return nil, errors.New("key path must name a file")
	}
	if err := p.ensure(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *FileKeyProvider) Key(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil || p.Path == "" {
		return nil, ErrInvalidKey
	}
	return p.load()
}

func (p *FileKeyProvider) ensure() error {
	if info, err := os.Lstat(p.Path); err == nil {
		if err := validateKeyFile(info); err != nil {
			return err
		}
		return p.validateContents()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0700); err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	f, err := os.OpenFile(p.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return p.ensure()
		}
		return err
	}
	defer f.Close()
	if _, err = f.Write(key); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return nil
}

func (p *FileKeyProvider) load() ([]byte, error) {
	info, err := os.Lstat(p.Path)
	if err != nil {
		return nil, err
	}
	if err := validateKeyFile(info); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: key file must contain 32 bytes", ErrInvalidKey)
	}
	return key, nil
}

func (p *FileKeyProvider) validateContents() error {
	_, err := p.load()
	return err
}

func validateKeyFile(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("secret key file must be a regular file")
	}
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("secret key file permissions are too broad")
	}
	return nil
}
