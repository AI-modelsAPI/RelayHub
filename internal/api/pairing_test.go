package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func tokenServer(t *testing.T, token string) http.Handler {
	t.Helper()
	s, err := NewConfiguredServer(Config{LocalOnly: true, Management: "127.0.0.1:8790", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler()
}

// AUDIT 2026-09-24 F4: with a token configured, reads are protected too;
// only liveness and pairing stay public.
func TestManagementTokenProtectsReads(t *testing.T) {
	h := tokenServer(t, "t0ken")
	for _, path := range []string{"/api/v1/channels", "/api/v1/settings", "/api/v1/lab", "/api/v1/overview"} {
		if w := request(t, h, http.MethodGet, path, "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s without token: %d, want 401", path, w.Code)
		}
		if w := request(t, h, http.MethodGet, path, "t0ken", ""); w.Code == http.StatusUnauthorized {
			t.Fatalf("GET %s with token: still 401", path)
		}
	}
	if w := request(t, h, http.MethodGet, "/api/v1/health", "", ""); w.Code != http.StatusOK {
		t.Fatalf("health must stay public: %d", w.Code)
	}
	if w := request(t, h, http.MethodGet, "/healthz", "", ""); w.Code != http.StatusOK {
		t.Fatalf("healthz must stay public: %d", w.Code)
	}
}

func TestConsolePairingFlow(t *testing.T) {
	h := tokenServer(t, "t0ken")
	if w := request(t, h, http.MethodPost, "/api/v1/auth/pair-codes", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("minting without the token: %d, want 401", w.Code)
	}
	w := request(t, h, http.MethodPost, "/api/v1/auth/pair-codes", "t0ken", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", w.Code, w.Body.String())
	}
	var minted map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}
	code := minted["code"]
	if len(code) < 30 || !strings.HasSuffix(minted["url"], "/#pair="+code) || strings.Contains(minted["url"], "t0ken") || minted["expires_at"] == "" {
		t.Fatalf("unexpected pairing payload: %v", minted)
	}

	redeem := func(code string) *httptest.ResponseRecorder {
		return request(t, h, http.MethodPost, "/api/v1/auth/pair", "", `{"code":"`+code+`"}`)
	}
	if w := redeem("not-a-code"); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad code: %d", w.Code)
	}
	w = redeem(code)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"token":"t0ken"`) {
		t.Fatalf("redeem: %d %s", w.Code, w.Body.String())
	}
	if w := redeem(code); w.Code != http.StatusUnauthorized {
		t.Fatalf("a pairing code must work only once: %d", w.Code)
	}
}

func TestPairingCodesExpireAndAreBounded(t *testing.T) {
	var p pairingStore
	now := time.Now()
	code, _, err := p.mint(now)
	if err != nil {
		t.Fatal(err)
	}
	if p.redeem(code, now.Add(pairingCodeTTL+time.Second)) {
		t.Fatal("expired code accepted")
	}
	first, _, _ := p.mint(now)
	for i := 0; i < maxPairingCodes+4; i++ {
		if _, _, err := p.mint(now.Add(time.Duration(i+1) * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.codes) > maxPairingCodes {
		t.Fatalf("%d outstanding codes, cap %d", len(p.codes), maxPairingCodes)
	}
	if p.redeem(first, now.Add(time.Second)) {
		t.Fatal("the oldest code should have been evicted")
	}
}

func TestPairingWithoutAuthIsRefused(t *testing.T) {
	h := tokenServer(t, "")
	if w := request(t, h, http.MethodPost, "/api/v1/auth/pair", "", `{"code":"x"}`); w.Code != http.StatusConflict {
		t.Fatalf("pair with auth off: %d, want 409", w.Code)
	}
	if w := request(t, h, http.MethodPost, "/api/v1/auth/pair-codes", "", ""); w.Code != http.StatusConflict {
		t.Fatalf("mint with auth off: %d, want 409", w.Code)
	}
}

func TestCredentialIDNeverRevealsToken(t *testing.T) {
	s, err := NewConfiguredServer(Config{LocalOnly: true, Management: "127.0.0.1:8790", Token: "t0ken-value"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/channels", nil)
	if id := s.credentialID(r); id != "" {
		t.Fatalf("anonymous request got credential id %q", id)
	}
	r.Header.Set("Authorization", "Bearer t0ken-value")
	id := s.credentialID(r)
	if !strings.HasPrefix(id, "token:") || len(id) != len("token:")+8 || strings.Contains(id, "t0ken") {
		t.Fatalf("credential id %q", id)
	}
}
