package egress

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"relayhub/internal/domain"
)

// recordingProxy is a minimal HTTP proxy (absolute-URI GET forwarding and
// CONNECT tunnelling) that remembers what went through it.
type recordingProxy struct {
	mu   sync.Mutex
	seen []string
	srv  *httptest.Server
}

func newRecordingProxy(t *testing.T) *recordingProxy {
	t.Helper()
	p := &recordingProxy{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.Method+" "+r.Host)
		p.mu.Unlock()
		if r.Method == http.MethodConnect {
			upstream, err := net.Dial("tcp", r.Host)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "no hijack", http.StatusInternalServerError)
				return
			}
			client, _, err := hj.Hijack()
			if err != nil {
				return
			}
			_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
			go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
			_, _ = io.Copy(client, upstream)
			_ = client.Close()
			return
		}
		// Plain HTTP proxying: forward the absolute-URI request.
		req, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *recordingProxy) count() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.seen) }

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"  direct ":                     Direct,
		"NONE":                          Direct,
		"http://127.0.0.1:7890":         "http://127.0.0.1:7890",
		"socks5://user:pw@host:1080":    "socks5://user:pw@host:1080",
		"socks5h://host:1080":           "socks5://host:1080",
		"https://proxy.example.com:443": "https://proxy.example.com:443",
	}
	for in, want := range cases {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"ftp://x:1", "127.0.0.1:7890", "http://"} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) must fail", bad)
		}
	}
}

// The whole point of per-channel egress: a channel with ProxyURL set must have
// its bytes go through that proxy, and a channel without it must not.
func TestClientForRoutesThroughChannelProxy(t *testing.T) {
	proxy := newRecordingProxy(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	sel := New("")
	viaProxy := domain.Channel{ID: "a", ProxyURL: proxy.srv.URL}
	direct := domain.Channel{ID: "b"}

	resp, err := sel.ClientFor(viaProxy, 5*time.Second).Get(upstream.URL)
	if err != nil {
		t.Fatalf("request via proxy failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "ok" || proxy.count() != 1 {
		t.Fatalf("expected one proxied request, got body=%q proxied=%d", body, proxy.count())
	}

	resp, err = sel.ClientFor(direct, 5*time.Second).Get(upstream.URL)
	if err != nil {
		t.Fatalf("direct request failed: %v", err)
	}
	_ = resp.Body.Close()
	if proxy.count() != 1 {
		t.Fatalf("channel without proxy must not use the proxy, proxied=%d", proxy.count())
	}

	// Same proxy + timeout -> same cached client (shared pool).
	if sel.ClientFor(viaProxy, 5*time.Second) != sel.ClientFor(domain.Channel{ID: "c", ProxyURL: proxy.srv.URL}, 5*time.Second) {
		t.Fatal("clients for the same proxy must be shared")
	}
	if sel.ClientFor(viaProxy, 5*time.Second) == sel.ClientFor(direct, 5*time.Second) {
		t.Fatal("proxy and direct clients must differ")
	}
}

// The global default applies to channels without their own proxy, and a
// per-channel "direct" opts out of it.
func TestGlobalDefaultAndDirectOverride(t *testing.T) {
	proxy := newRecordingProxy(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer upstream.Close()

	sel := New(proxy.srv.URL)
	if got := sel.ProxyFor(domain.Channel{ID: "x"}); got != proxy.srv.URL {
		t.Fatalf("default not applied: %q", got)
	}
	if got := sel.ProxyFor(domain.Channel{ID: "y", ProxyURL: "direct"}); got != Direct {
		t.Fatalf("direct override lost: %q", got)
	}
	resp, err := sel.ClientFor(domain.Channel{ID: "x"}, 5*time.Second).Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	resp, err = sel.ClientFor(domain.Channel{ID: "y", ProxyURL: "direct"}, 5*time.Second).Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if proxy.count() != 1 {
		t.Fatalf("expected exactly the default-routed request through the proxy, got %d", proxy.count())
	}
}

// A typo in a proxy URL must fail the request, never silently fall back to a
// different exit (that would defeat the reason the user configured a proxy).
func TestInvalidProxyFailsClosed(t *testing.T) {
	sel := New("")
	c := sel.ClientFor(domain.Channel{ID: "z", ProxyURL: "ftp://nope:1"}, time.Second)
	if _, err := c.Get("http://127.0.0.1:9/"); err == nil || !strings.Contains(err.Error(), "unsupported proxy scheme") {
		t.Fatalf("expected unsupported scheme error, got %v", err)
	}
}

func TestDescribeHidesCredentials(t *testing.T) {
	if got := Describe("socks5://alice:secret@host:1080"); strings.Contains(got, "secret") || !strings.Contains(got, "alice") {
		t.Fatalf("Describe leaked or dropped too much: %q", got)
	}
	if Describe("") != "environment/direct" || Describe("direct") != Direct || Describe("ftp://x:1") != "invalid" {
		t.Fatalf("Describe basics wrong: %q %q %q", Describe(""), Describe("direct"), Describe("ftp://x:1"))
	}
}

// Guard against regressions in the CONNECT path used for https upstreams.
func TestConnectTunnelThroughProxy(t *testing.T) {
	proxy := newRecordingProxy(t)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("tls-ok")) }))
	defer upstream.Close()

	sel := New("")
	client := sel.ClientFor(domain.Channel{ID: "t", ProxyURL: proxy.srv.URL}, 5*time.Second)
	// Trust the test server certificate on this client's transport only.
	tr := client.Transport.(*http.Transport).Clone()
	tr.TLSClientConfig = upstream.Client().Transport.(*http.Transport).TLSClientConfig
	client = &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("https via proxy failed: %v", err)
	}
	b, _ := io.ReadAll(bufio.NewReader(resp.Body))
	_ = resp.Body.Close()
	if string(b) != "tls-ok" || proxy.count() != 1 || !strings.HasPrefix(proxy.seen[0], "CONNECT ") {
		t.Fatalf("expected CONNECT through proxy, got body=%q seen=%v", b, proxy.seen)
	}
}
