package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"

	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"relayhub/internal/logging"
)

func startHTTP(t *testing.T, cfg HTTPConfig) (*HTTPServer, context.CancelFunc) {
	t.Helper()
	s, err := NewHTTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan error, 1)
	go func() { started <- s.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = s.Shutdown(context.Background())
		select {
		case err := <-started:
			if err != nil {
				t.Errorf("proxy stopped with error: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("proxy did not stop")
		}
	})
	return s, cancel
}

func startSOCKS(t *testing.T, cfg SOCKS5Config) (*SOCKS5Server, context.CancelFunc) {
	t.Helper()
	s, err := NewSOCKS5(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan error, 1)
	go func() { started <- s.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = s.Shutdown(context.Background())
		select {
		case err := <-started:
			if err != nil {
				t.Errorf("proxy stopped with error: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("proxy did not stop")
		}
	})
	return s, cancel
}

func TestHTTPProxyOrdinaryForwardingLargeBodyAndRedactedLogs(t *testing.T) {
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("X-Upstream", "yes")
		_, _ = w.Write([]byte(fmt.Sprintf("received:%d", len(gotBody))))
	}))
	defer upstream.Close()

	var logs bytes.Buffer
	proxy, _ := startHTTP(t, HTTPConfig{Addr: "127.0.0.1:0", MaxBodyBytes: 2 << 20, Logger: newTestLogger(&logs)})
	transport := &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: proxy.Addr().String()})}
	client := &http.Client{Transport: transport}
	body := bytes.Repeat([]byte("sensitive-body-"), 100000)
	req, err := http.NewRequest(http.MethodPost, upstream.URL+"/upload?token=secret", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(response) != fmt.Sprintf("received:%d", len(body)) || !bytes.Equal(gotBody, body) {
		t.Fatalf("unexpected forwarded response: status=%d body=%q upstream_body=%d", resp.StatusCode, response, len(gotBody))
	}
	// The proxy logs asynchronously; close its listener before reading the
	// test buffer so the assertion does not race with the logging goroutine.
	if err := proxy.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), string(body)) || strings.Contains(logs.String(), "secret") {
		t.Fatalf("proxy log leaked request content: %s", logs.String())
	}
}

func TestHTTPProxyConnectHTTPS(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secure:" + r.URL.Path))
	}))
	defer upstream.Close()
	proxy, _ := startHTTP(t, HTTPConfig{Addr: "127.0.0.1:0"})
	transport := upstream.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: proxy.Addr().String()})
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test certificate is intentionally self-signed.
	client := &http.Client{Transport: transport}
	resp, err := client.Get(upstream.URL + "/through-connect")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != "secure:/through-connect" {
		t.Fatalf("unexpected CONNECT response %q", got)
	}
}

func TestHTTPProxyConnectPreservesCoalescedPayload(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	payload := []byte("coalesced-tls-or-application-payload")
	received := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		got, _ := io.ReadAll(io.LimitReader(conn, int64(len(payload))))
		received <- got
	}()

	proxy, _ := startHTTP(t, HTTPConfig{Addr: "127.0.0.1:0"})
	conn, err := net.Dial("tcp", proxy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", listener.Addr(), listener.Addr())
	if _, err := conn.Write(append([]byte(request), payload...)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.Contains(line, "200") {
		t.Fatalf("CONNECT failed: line=%q err=%v", line, err)
	}
	select {
	case got := <-received:
		if !bytes.Equal(got, payload) {
			t.Fatalf("upstream received %q, want %q", got, payload)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive coalesced payload")
	}
}

func TestHTTPProxyAuthenticationAndTargetPolicy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer upstream.Close()
	proxy, _ := startHTTP(t, HTTPConfig{Addr: "127.0.0.1:0", Username: "alice", Password: "correct"})
	target, _ := url.Parse(upstream.URL)
	unauthenticated := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: proxy.Addr().String()})}}
	resp, err := unauthenticated.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("unauthenticated status=%d", resp.StatusCode)
	}
	conn, err := net.Dial("tcp", proxy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic YWxpY2U6Y29ycmVjdA==\r\nConnection: close\r\n\r\n", target.String(), target.Host)
	line, _ := bufio.NewReader(conn).ReadString('\n')
	_ = conn.Close()
	if !strings.Contains(line, "200") {
		t.Fatalf("authenticated status line=%q", line)
	}

	conn, err = net.Dial("tcp", proxy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(conn, "CONNECT 1.1.1.1:443 HTTP/1.1\r\nHost: 1.1.1.1:443\r\nProxy-Authorization: Basic YWxpY2U6Y29ycmVjdA==\r\n\r\n")
	line, _ = bufio.NewReader(conn).ReadString('\n')
	_ = conn.Close()
	if !strings.Contains(line, "403") {
		t.Fatalf("public target was not denied: %q", line)
	}
}

func TestHTTPProxyRejectsOversizedHeadersAndBodies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("unexpected")) }))
	defer upstream.Close()
	proxy, _ := startHTTP(t, HTTPConfig{Addr: "127.0.0.1:0", MaxHeaderBytes: 512, MaxBodyBytes: 4})
	conn, err := net.Dial("tcp", proxy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(conn, "GET http://127.0.0.1:%s/ HTTP/1.1\r\nHost: 127.0.0.1:%s\r\nX-Large: %s\r\n\r\n", portOf(upstream.URL), portOf(upstream.URL), strings.Repeat("x", 1000))
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if data, err := io.ReadAll(conn); err != nil || len(data) != 0 {
		t.Fatalf("oversized header connection returned data=%q err=%v", data, err)
	}
	_ = conn.Close()

	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: proxy.Addr().String()})}}
	resp, err := client.Post(upstream.URL, "text/plain", strings.NewReader("12345"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status=%d", resp.StatusCode)
	}
}

func TestHTTPProxyRejectsOversizedUnknownLengthBodyBeforeUpstreamDial(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream was contacted for an oversized unknown-length body")
	}))
	defer upstream.Close()
	proxy, _ := startHTTP(t, HTTPConfig{Addr: "127.0.0.1:0", MaxBodyBytes: 4})
	conn, err := net.Dial("tcp", proxy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	port := portOf(upstream.URL)
	request := fmt.Sprintf("POST http://127.0.0.1:%s/ HTTP/1.1\r\nHost: 127.0.0.1:%s\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n5\r\n12345\r\n0\r\n\r\n", port, port)
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response, []byte("413 Request Entity Too Large")) {
		t.Fatalf("unexpected response %q", response)
	}
}

func TestProxyShutdownHonorsDeadlineWithBlockedConnection(t *testing.T) {
	s, err := NewHTTP(HTTPConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	blocked := &nonClosingConn{}
	if !s.register(blocked) {
		t.Fatal("failed to register blocked connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := s.Shutdown(ctx); err == nil {
		t.Fatal("Shutdown ignored blocked connection")
	} else if time.Since(started) > time.Second {
		t.Fatalf("Shutdown exceeded deadline by too much: %v", time.Since(started))
	}
}

type nonClosingConn struct{}

func (*nonClosingConn) Read([]byte) (int, error)         { return 0, io.ErrClosedPipe }
func (*nonClosingConn) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (*nonClosingConn) Close() error                     { return nil }
func (*nonClosingConn) LocalAddr() net.Addr              { return testAddr("local") }
func (*nonClosingConn) RemoteAddr() net.Addr             { return testAddr("remote") }
func (*nonClosingConn) SetDeadline(time.Time) error      { return nil }
func (*nonClosingConn) SetReadDeadline(time.Time) error  { return nil }
func (*nonClosingConn) SetWriteDeadline(time.Time) error { return nil }

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }

func TestSOCKS5HTTPHTTPSIPv4AndDomain(t *testing.T) {
	httpUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("socks-http")) }))
	defer httpUpstream.Close()
	tlsUpstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("socks-https")) }))
	defer tlsUpstream.Close()
	proxy, _ := startSOCKS(t, SOCKS5Config{Addr: "127.0.0.1:0"})

	for _, tc := range []struct {
		name string
		host string
		url  string
		tls  bool
	}{
		{"ipv4-http", "127.0.0.1", httpUpstream.URL, false},
		{"domain-http", "localhost", httpUpstream.URL, false},
		{"ipv4-https", "127.0.0.1", tlsUpstream.URL, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, _ := url.Parse(tc.url)
			conn := socksConnect(t, proxy.Addr().String(), tc.host, u.Port(), false, "", "")
			defer conn.Close()
			var rw io.ReadWriter = conn
			if tc.tls {
				tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: tc.host})
				if err := tlsConn.Handshake(); err != nil {
					t.Fatal(err)
				}
				rw = tlsConn
			}
			_, _ = fmt.Fprintf(rw, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", u.Host)
			got, err := io.ReadAll(rw)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(got, []byte("200 OK")) {
				t.Fatalf("unexpected response %q", got)
			}
		})
	}
}

func TestSOCKS5AuthenticationFailureAndSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("auth-ok")) }))
	defer upstream.Close()
	proxy, _ := startSOCKS(t, SOCKS5Config{Addr: "127.0.0.1:0", Username: "alice", Password: "correct"})
	u, _ := url.Parse(upstream.URL)
	conn, err := net.Dial("tcp", proxy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte{5, 1, 2})
	result := make([]byte, 2)
	if _, err = io.ReadFull(conn, result); err != nil || result[1] != 2 {
		t.Fatalf("unexpected method selection %v: %v", result, err)
	}
	_, _ = conn.Write([]byte{1, 5, 'a', 'l', 'i', 'c', 'e', 5, 'w', 'r', 'o', 'n', 'g'})
	if _, err = io.ReadFull(conn, result); err != nil || result[1] != 1 {
		t.Fatalf("unexpected failed-auth result %v: %v", result, err)
	}

	conn = socksConnect(t, proxy.Addr().String(), "127.0.0.1", u.Port(), true, "alice", "correct")
	defer conn.Close()
	_, _ = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", u.Host)
	got, _ := io.ReadAll(conn)
	if !bytes.Contains(got, []byte("auth-ok")) {
		t.Fatalf("authenticated SOCKS request failed: %q", got)
	}
}

func TestSOCKS5CancellationAndIPv6WhenAvailable(t *testing.T) {
	listener, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 loopback unavailable")
	}
	_ = listener.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	proxy, cancel := startSOCKS(t, SOCKS5Config{Addr: "127.0.0.1:0", IdleTimeout: 5 * time.Second})
	upstreamURL, _ := url.Parse(upstream.URL)
	conn := socksConnect(t, proxy.Addr().String(), "127.0.0.1", upstreamURL.Port(), false, "", "")
	_, _ = fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n")
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 128)
	_, _ = conn.Read(buf)
	cancel()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = conn.Read(buf); err == nil {
		t.Fatal("canceled SOCKS connection remained open")
	}
	_ = conn.Close()
}

func socksConnect(t *testing.T, proxyAddr, host, port string, auth bool, user, password string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	methods := []byte{0}
	if auth {
		methods = []byte{2}
	}
	_, _ = conn.Write(append([]byte{5, byte(len(methods))}, methods...))
	selection := make([]byte, 2)
	if _, err = io.ReadFull(conn, selection); err != nil || selection[1] == 0xff {
		conn.Close()
		t.Fatalf("SOCKS method selection failed: %v", err)
	}
	if auth {
		payload := append([]byte{1, byte(len(user))}, []byte(user)...)
		payload = append(payload, byte(len(password)))
		payload = append(payload, []byte(password)...)
		_, _ = conn.Write(payload)
		result := make([]byte, 2)
		if _, err = io.ReadFull(conn, result); err != nil || result[1] != 0 {
			conn.Close()
			t.Fatalf("SOCKS auth failed: %v", err)
		}
	}
	ip := net.ParseIP(host)
	request := []byte{5, 1, 0}
	if ip4 := ip.To4(); ip4 != nil {
		request = append(request, 1)
		request = append(request, ip4...)
	} else if ip16 := ip.To16(); ip16 != nil {
		request = append(request, 4)
		request = append(request, ip16...)
	} else {
		request = append(request, 3, byte(len(host)))
		request = append(request, host...)
	}
	portNum := 0
	_, _ = fmt.Sscan(port, &portNum)
	request = append(request, byte(portNum>>8), byte(portNum))
	_, _ = conn.Write(request)
	reply := make([]byte, 4)
	if _, err = io.ReadFull(conn, reply); err != nil || reply[1] != 0 {
		conn.Close()
		t.Fatalf("SOCKS connect failed: reply=%v err=%v", reply, err)
	}
	var n int
	switch reply[3] {
	case 1:
		n = 4
	case 4:
		n = 16
	case 3:
		length := make([]byte, 1)
		_, _ = io.ReadFull(conn, length)
		n = int(length[0])
	default:
		conn.Close()
		t.Fatal("invalid SOCKS reply address")
	}
	if _, err = io.CopyN(io.Discard, conn, int64(n+2)); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn
}

func newTestLogger(w io.Writer) *logging.Logger { return logging.New(w) }

func portOf(raw string) string {
	u, _ := url.Parse(raw)
	return u.Port()
}
