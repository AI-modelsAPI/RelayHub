package testhelper

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type MockSecretStore struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func NewMockSecretStore() *MockSecretStore {
	return &MockSecretStore{
		values: make(map[string][]byte),
	}
}

func (m *MockSecretStore) Put(ctx context.Context, ref string, plaintext []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[ref] = append([]byte(nil), plaintext...)
	return nil
}

func (m *MockSecretStore) Get(ctx context.Context, ref string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.values[ref]
	if !ok {
		return nil, fmt.Errorf("secret not found: %s", ref)
	}
	return append([]byte(nil), v...), nil
}

// cstZone mirrors internal/adapter's Beijing zone. Check-in judgements are made
// against Beijing "today", so fixtures must be dated relative to the same clock.
var cstZone = time.FixedZone("CST", 8*3600)

// Date placeholders resolved at fixture load time. Hard-coding a literal date in
// a fixture makes the test pass only on the day it was written and silently turn
// red every day after, so every check-in date must be written as a placeholder.
var fixtureDatePlaceholders = []struct {
	token     string
	dayOffset int
}{
	{"{{TODAY}}", 0},
	{"{{YESTERDAY}}", -1},
	{"{{TOMORROW}}", 1},
}

// LoadFixture reads a fixture file and substitutes relative-date placeholders
// with concrete Beijing-time dates, so date-sensitive assertions stay stable on
// any calendar day.
func LoadFixture(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	now := time.Now().In(cstZone)
	for _, p := range fixtureDatePlaceholders {
		text = strings.ReplaceAll(text, p.token, now.AddDate(0, 0, p.dayOffset).Format("2006-01-02"))
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, err
	}
	return out, nil
}
