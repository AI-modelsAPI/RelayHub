package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/router"
)

type taggingTransport struct {
	tag  string
	seen *[]string
}

func (t taggingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	*t.seen = append(*t.seen, t.tag)
	return http.DefaultTransport.RoundTrip(r)
}

// Channel.ProxyURL was persisted but never consumed: the upstream used
// http.DefaultClient for every channel. The upstream must ask for a
// per-channel client.
func TestHTTPUpstreamUsesPerChannelClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	var seen []string
	up := HTTPUpstream{
		Client: &http.Client{Transport: taggingTransport{tag: "default", seen: &seen}},
		ClientFor: func(ch domain.Channel) *http.Client {
			if ch.ProxyURL == "" {
				return nil
			}
			return &http.Client{Transport: taggingTransport{tag: "proxy:" + ch.ProxyURL, seen: &seen}}
		},
	}
	do := func(ch domain.Channel) {
		resp, err := up.Do(context.Background(), Request{Protocol: "openai-chat", Path: "/v1/chat/completions", Body: []byte(`{}`), Decision: router.Decision{Channel: ch}})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	do(domain.Channel{ID: "a", BaseURL: srv.URL, ProxyURL: "socks5://127.0.0.1:1080"})
	do(domain.Channel{ID: "b", BaseURL: srv.URL})
	if len(seen) != 2 || seen[0] != "proxy:socks5://127.0.0.1:1080" || seen[1] != "default" {
		t.Fatalf("per-channel client selection wrong: %v", seen)
	}
}
