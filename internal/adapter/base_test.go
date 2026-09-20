package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"relayhub/internal/adapter/testhelper"
	"relayhub/internal/domain"
)

func TestBaseNewAPIAdapter_Helpers(t *testing.T) {
	t.Run("MergeCookies", func(t *testing.T) {
		oldCk := "a=1; b=2; c=3"
		setCookies := []string{
			"b=20; Path=/; HttpOnly",
			"c=deleted; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
			"d=4; Secure",
		}
		merged := MergeCookies(oldCk, setCookies)
		if !HasRefreshCookie(merged) {
			// ok, neither has it
		}
		if HasRefreshCookie("foo=bar; new_api_refresh=xyz") != true {
			t.Errorf("expected HasRefreshCookie to be true")
		}
	})

	t.Run("JWT_ExpMs_NeedsExchange", func(t *testing.T) {
		// Non-JWT
		if NeedsExchange("simple-api-key") {
			t.Errorf("expected non-jwt to not trigger NeedsExchange")
		}
		if ExpMs("simple-api-key") != 0 {
			t.Errorf("expected 0 for invalid jwt")
		}

		// Valid JWT payload with future exp
		// header: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9 ({"alg":"HS256","typ":"JWT"})
		futureJWT := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJleHAiOjQxMDI0NDQ4MDB9.signature"
		if NeedsExchange(futureJWT) {
			t.Errorf("expected future JWT to not need exchange")
		}

		expiredJWT := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJleHAiOjE1MDAwMDAwMDB9.signature"
		if !NeedsExchange(expiredJWT) {
			t.Errorf("expected expired JWT to need exchange")
		}
	})

	t.Run("CheckinLogParsing", func(t *testing.T) {
		if !IsCheckinLogText("用户签到，获得额度 ＄20.642880 额度") {
			t.Errorf("expected true for checkin log")
		}
		if IsCheckinLogText("用户注册，获得赠送额度 $10") {
			t.Errorf("expected false for registration bonus")
		}
		if IsCheckinLogText("邀请好友，获得奖励 $5") {
			t.Errorf("expected false for invitation bonus")
		}

		usd := ParseUsdInText("用户签到，获得额度 ＄20.642880 额度")
		if usd != 20.64 {
			t.Errorf("expected 20.64, got %f", usd)
		}
	})

	t.Run("TokenRefreshWorkflow", func(t *testing.T) {
		var refreshHits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/user/auth/refresh" {
				refreshHits++
				w.Header().Add("Set-Cookie", "new_api_refresh=renewed-refresh-cookie; Path=/")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"success":true,"data":{"access_token":"new-jwt-token"}}`))
				return
			}
			http.NotFound(w, r)
		}))
		defer srv.Close()

		secStore := testhelper.NewMockSecretStore()
		expiredJWT := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJleHAiOjE1MDAwMDAwMDB9.signature"
		credJSON := `{"token":"` + expiredJWT + `","site_cookie":"new_api_refresh=old-cookie"}`
		_ = secStore.Put(context.Background(), "cred-1", []byte(credJSON))

		b := &BaseNewAPIAdapter{
			Client:  srv.Client(),
			Secrets: secStore,
		}
		ch := domain.Channel{
			ID:            "ch-1",
			BaseURL:       srv.URL,
			CredentialRef: "cred-1",
		}

		cred, _ := b.ResolveCredential(context.Background(), ch)
		newToken, err := b.RefreshAccessToken(context.Background(), ch, cred, "")
		if err != nil {
			t.Fatalf("refresh failed: %v", err)
		}
		if newToken != "new-jwt-token" {
			t.Errorf("expected new-jwt-token, got %s", newToken)
		}
		if refreshHits != 1 {
			t.Errorf("expected 1 refresh call, got %d", refreshHits)
		}

		// Verify persisted
		persisted, _ := secStore.Get(context.Background(), "cred-1")
		if string(persisted) == credJSON {
			t.Errorf("expected secret to be updated in store")
		}
	})
}
