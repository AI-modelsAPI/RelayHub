package notify

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"relayhub/internal/logging"
)

// AUDIT 2026-09-24 F20: a failed Telegram/Bark/webhook send logged the full
// URL, including the bot token or device key.
func TestSendErrorsDoNotLeakURLSecrets(t *testing.T) {
	const token = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	client := &http.Client{Timeout: 2 * time.Second}
	tg := NewTelegram(token, "42", client)
	tg.apiBase = "http://127.0.0.1:1"
	sinks := []Sink{
		tg,
		&Bark{URL: "http://127.0.0.1:1/SECRETDEVICEKEY123/", client: client},
		&Webhook{URL: "http://127.0.0.1:1/robot/send?access_token=hook-secret-xyz", client: client},
	}
	for _, s := range sinks {
		err := s.Send(context.Background(), Event{Title: "t", Body: "b", At: time.Now()})
		if err == nil {
			t.Fatalf("%s: expected a connection error", s.Name())
		}
		for _, secret := range []string{token, "SECRETDEVICEKEY123", "hook-secret-xyz"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("%s error leaks %q: %v", s.Name(), secret, err)
			}
		}
		if !strings.Contains(err.Error(), "127.0.0.1:1") {
			t.Fatalf("%s error should still name the host: %v", s.Name(), err)
		}
	}
	if got := logging.NewRedactor().Redact("post https://api.telegram.org/bot" + token + "/sendMessage"); strings.Contains(got, token) {
		t.Fatalf("redactor missed telegram token: %s", got)
	}
}
