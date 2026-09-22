package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relayhub/internal/domain"
)

type countingProvider struct {
	calls  int
	client *http.Client
}

func (c *countingProvider) ClientFor(ch domain.Channel, timeout time.Duration) *http.Client {
	c.calls++
	return c.client
}

// Every HTTP call an adapter makes for a channel must go through the
// per-channel client when a provider is installed, so check-in traffic exits
// through the same proxy as gateway traffic.
func TestBaseAdapterUsesClientProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"quota_per_unit":500000}}`))
	}))
	defer srv.Close()
	prov := &countingProvider{client: srv.Client()}
	b := &BaseNewAPIAdapter{Client: &http.Client{Transport: failingTransport{}}}
	b.SetClientProvider(prov)
	ch := domain.Channel{ID: "c", BaseURL: srv.URL, ProxyURL: "http://127.0.0.1:7890"}
	if unit := b.FetchStatusQuotaPerUnit(context.Background(), ch); unit != 500000 {
		t.Fatalf("expected request via provider client, got unit=%d", unit)
	}
	if prov.calls != 1 {
		t.Fatalf("provider must be consulted once, got %d", prov.calls)
	}
	// Registry-level installation reaches embedded bases and generic adapters.
	reg := NewRegistry()
	g := NewGenericAdapter(nil)
	_ = reg.Register("generic", g)
	_ = reg.Register("base", &struct{ *BaseNewAPIAdapter }{&BaseNewAPIAdapter{}})
	reg.SetClientProvider(prov)
	if g.Clients == nil {
		t.Fatal("generic adapter did not receive the client provider")
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, http.ErrHandlerTimeout
}
