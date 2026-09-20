package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/router"
)

func TestSSEValidationRejectsMalformedEvent(t *testing.T) {
	w := httptest.NewRecorder()
	_, err := writeSSE(w, strings.NewReader("data: not-json\n\n"), "openai")
	if err == nil || w.Code != http.StatusOK {
		t.Fatalf("err=%v code=%d", err, w.Code)
	}
}
func TestHTTPUpstreamPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	up := HTTPUpstream{Client: http.DefaultClient}
	_, err := up.Do(ctx, Request{Decision: router.Decision{Channel: domain.Channel{BaseURL: "http://127.0.0.1"}}, Body: []byte(`{}`), Path: "/v1/messages"})
	if err == nil {
		t.Fatal("expected cancellation")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err=%v", err)
	}
}
func TestResponseBodyCanBeClosed(t *testing.T) {
	body := io.NopCloser(strings.NewReader("x"))
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
}
