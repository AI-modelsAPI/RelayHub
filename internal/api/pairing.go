package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Pairing hands the management token to a browser without putting the
// long-lived token into a URL (AUDIT 2026-09-24 F4). A one-time code, valid
// for a few minutes, travels in the URL fragment — which browsers never send
// to a server and which therefore never reaches an access log — and the
// console redeems it exactly once for the token, which it keeps in its own
// origin-scoped storage. Codes are minted at startup (printed to the log), by
// `relayhub pair`, and by the desktop shell; the last two authenticate with
// the token they read from the private data directory.

const (
	pairingCodeTTL  = 10 * time.Minute
	maxPairingCodes = 16
)

type pairingStore struct {
	mu    sync.Mutex
	codes map[string]time.Time // sha256(code) -> expiry
}

func pairingKey(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func (p *pairingStore) mint(now time.Time) (string, time.Time, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	exp := now.Add(pairingCodeTTL)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.codes == nil {
		p.codes = map[string]time.Time{}
	}
	for k, e := range p.codes {
		if !now.Before(e) {
			delete(p.codes, k)
		}
	}
	for len(p.codes) >= maxPairingCodes {
		// Drop the code closest to expiry.
		var oldest string
		var oldestExp time.Time
		for k, e := range p.codes {
			if oldest == "" || e.Before(oldestExp) {
				oldest, oldestExp = k, e
			}
		}
		delete(p.codes, oldest)
	}
	p.codes[pairingKey(code)] = exp
	return code, exp, nil
}

// redeem consumes a code; every code works at most once.
func (p *pairingStore) redeem(code string, now time.Time) bool {
	if code == "" || len(code) > 128 {
		return false
	}
	key := pairingKey(code)
	p.mu.Lock()
	defer p.mu.Unlock()
	exp, ok := p.codes[key]
	if !ok {
		return false
	}
	delete(p.codes, key)
	return now.Before(exp)
}

// NewPairingCode mints a one-time console pairing code. It fails when
// management authentication is disabled, since there is nothing to hand out.
func (s *Server) NewPairingCode() (string, time.Time, error) {
	if s.Token == "" {
		return "", time.Time{}, errAuthDisabled
	}
	return s.pairing.mint(time.Now())
}

// PairingURL is the console link that redeems code on load.
func PairingURL(host, code string) string {
	return "http://" + host + "/#pair=" + code
}

var errAuthDisabled = fault{status: http.StatusConflict, code: "auth_disabled", message: "management authentication is disabled"}

// authPair redeems a pairing code for the management token.
// POST /api/v1/auth/pair {"code": "..."} -> 200 {"token": "..."}
func (s *Server) authPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.fail(w, r, badRequest("method_not_allowed", "POST is required"))
		return
	}
	if s.Token == "" {
		s.fail(w, r, errAuthDisabled)
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.pairing.redeem(strings.TrimSpace(input.Code), time.Now()) {
		s.fail(w, r, unauthorized())
		return
	}
	s.auditEvent(r.Context(), "management_paired", r, nil)
	s.write(w, r, http.StatusOK, map[string]any{"token": s.Token})
}

// authPairCodes mints a pairing code for an already authenticated client
// (relayhub pair, the desktop shell).
// POST /api/v1/auth/pair-codes -> 201 {"code", "url", "expires_at"}
func (s *Server) authPairCodes(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		s.fail(w, r, badRequest("method_not_allowed", "POST is required"))
		return
	}
	code, exp, err := s.NewPairingCode()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	host := r.Host
	if host == "" {
		host = s.Management
	}
	s.auditEvent(r.Context(), "management_pair_code", r, nil)
	s.write(w, r, http.StatusCreated, map[string]any{
		"code":       code,
		"url":        PairingURL(host, code),
		"expires_at": exp.UTC().Format(time.RFC3339),
	})
}

// credentialID names the credential behind a request in audit records
// without revealing it.
func (s *Server) credentialID(r *http.Request) string {
	if s.Token == "" || !subtleEqual(r.Header.Get("Authorization"), "Bearer "+s.Token) {
		return ""
	}
	sum := sha256.Sum256([]byte(s.Token))
	return "token:" + hex.EncodeToString(sum[:4])
}
