package identity

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// AUDIT 2026-09-24 F13: an invalid proxy degraded the egress probe to a
// direct connection, reporting (and disclosing) the operator's real IP.
func TestProbeEgressNeverDegradesToDirect(t *testing.T) {
	if got := ProbeEgress(context.Background(), "::not a proxy::"); got != "" {
		t.Fatalf("invalid proxy produced a probe result %q", got)
	}
	var sawHost string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawHost = r.URL.Host
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("203.0.113.9\n")), Header: http.Header{}}, nil
	})}
	if got := ProbeEgressWith(context.Background(), client); got != "203.0.113.9" || sawHost != "api.ipify.org" {
		t.Fatalf("ProbeEgressWith = %q via %q", got, sawHost)
	}
	if got := ProbeEgressWith(context.Background(), nil); got != "" {
		t.Fatalf("nil client produced %q", got)
	}
}
