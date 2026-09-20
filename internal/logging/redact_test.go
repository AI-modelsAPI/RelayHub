package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactsSecretsAndPreservesOrdinaryValues(t *testing.T) {
	r := NewRedactor()
	input := "Authorization: Bearer *** authorization=Bearer equals-bearer authorization=Basic basic-secret Cookie: sid=cookie-secret x-api-key=api-secret password=pass-secret secret=secret-value https://x.test/cb?code=oauth-code&state=oauth-state model=claude-sonnet url=https://api.test/v1/models"
	output := r.Redact(input)
	for _, secret := range []string{"bearer-secret", "equals-bearer", "basic-secret", "cookie-secret", "api-secret", "pass-secret", "secret-value", "oauth-code", "oauth-state"} {
		if strings.Contains(output, secret) {
			t.Fatalf("secret leaked: %q in %q", secret, output)
		}
	}
	for _, ordinary := range []string{"claude-sonnet", "https://api.test/v1/models"} {
		if !strings.Contains(output, ordinary) {
			t.Fatalf("ordinary value removed: %q", ordinary)
		}
	}
}
func TestRedactsKnownTokenShapesWithoutMaskingModels(t *testing.T) {
	output := NewRedactor().Redact("keys sk-live123 ak-test456 ghp_ordinary model=gpt-5.6-sol")
	for _, secret := range []string{"sk-live123", "ak-test456", "ghp_ordinary"} {
		if strings.Contains(output, secret) {
			t.Fatalf("token leaked: %q", secret)
		}
	}
	if !strings.Contains(output, "gpt-5.6-sol") {
		t.Fatal("model was redacted")
	}
}
func TestSensitiveHeader(t *testing.T) {
	redactor := NewRedactor()
	for _, name := range []string{"Authorization", "Cookie", "Set-Cookie", "X-API-Key", "Proxy-Authorization"} {
		if got := redactor.Header(name, "sensitive-value"); got != "[REDACTED]" {
			t.Fatalf("%s = %q", name, got)
		}
	}
	if got := redactor.Header("Content-Type", "application/json"); got != "application/json" {
		t.Fatalf("ordinary header = %q", got)
	}
}
func TestRedactValueRecursesThroughStructuredFields(t *testing.T) {
	value := map[string]any{"authorization": "secret", "model": "claude-sonnet", "nested": []any{map[string]any{"password": "pw"}}}
	got := NewRedactor().RedactValue(value).(map[string]any)
	if got["authorization"] != "[REDACTED]" || got["model"] != "claude-sonnet" {
		t.Fatalf("unexpected redacted object: %#v", got)
	}
	if nested := got["nested"].([]any)[0].(map[string]any); nested["password"] != "[REDACTED]" {
		t.Fatalf("nested secret leaked: %#v", nested)
	}
}
func TestRedactValueRecursesThroughTypedMapsAndSlices(t *testing.T) {
	value := map[string]string{"authorization": "Bearer typed-bearer", "model": "claude-sonnet"}
	got := NewRedactor().RedactValue(value).(map[string]any)
	if got["authorization"] != "[REDACTED]" || got["model"] != "claude-sonnet" {
		t.Fatalf("unexpected typed map: %#v", got)
	}
	values := []string{"Basic typed-basic", "gpt-5.6-sol"}
	redacted := NewRedactor().RedactValue(values).([]any)
	if redacted[0] == "Basic typed-basic" || redacted[1] != "gpt-5.6-sol" {
		t.Fatalf("unexpected typed slice: %#v", redacted)
	}
}
func TestRedactValueHandlesNilInterfaces(t *testing.T) {
	value := []any{nil, map[string]any{"nested": nil}}
	got := NewRedactor().RedactValue(value).([]any)
	if got[0] != nil || got[1].(map[string]any)["nested"] != nil {
		t.Fatalf("unexpected nil handling: %#v", got)
	}
}
func TestLoggerNilWriterDoesNotPanic(t *testing.T) {
	logger := New(nil)
	if err := logger.Event("info", "request", "req-1", map[string]any{"key": "val"}); err != nil {
		t.Fatal(err)
	}
}

func TestLoggerRedactsBeforeJSONSink(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out)
	if err := logger.Event("info", "request", "req-1", map[string]any{"authorization": "Bearer top-secret", "model": "gpt-5.6-sol"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "top-secret") {
		t.Fatal("secret reached sink")
	}
	var event map[string]any
	if err := json.Unmarshal(out.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["request_id"] != "req-1" || event["model"] != "gpt-5.6-sol" {
		t.Fatalf("bad structured event: %#v", event)
	}
}
