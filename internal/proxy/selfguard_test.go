package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rawProxyRequest writes a raw request to the proxy and returns the status line.
func rawProxyRequest(t *testing.T, proxyAddr, request string) string {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("no response from proxy: %v", err)
	}
	return strings.TrimSpace(line)
}

// AUDIT 2026-09-24 F1: an origin-form request ("GET /") used to fall back to
// the Host header — the proxy itself — and recurse until file descriptors
// ran out. It must now be refused outright.
func TestHTTPProxyRejectsOriginFormInsteadOfDialingItself(t *testing.T) {
	p, _ := startHTTP(t, HTTPConfig{Addr: "127.0.0.1:0", TargetPolicy: OpenPolicy()})
	addr := p.Addr().String()
	for i := 0; i < 3; i++ {
		got := rawProxyRequest(t, addr, fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", addr))
		if !strings.Contains(got, " 400 ") {
			t.Fatalf("origin-form request: got %q, want 400", got)
		}
	}
	// Absolute-form to the proxy's own address must be refused by the guard,
	// not relayed back into the proxy.
	got := rawProxyRequest(t, addr, fmt.Sprintf("GET http://%s/ HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", addr, addr))
	if !strings.Contains(got, " 403 ") {
		t.Fatalf("self-targeted absolute-form request: got %q, want 403", got)
	}
	got = rawProxyRequest(t, addr, fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr))
	if !strings.Contains(got, " 403 ") {
		t.Fatalf("self-targeted CONNECT: got %q, want 403", got)
	}
}

// AUDIT 2026-09-24 F3: proxy clients reached the loopback management API
// (reading settings, minting gateway keys) because proxied requests carry no
// browser headers. Protected ports must be refused by every policy.
func TestProxiesRefuseProtectedLocalPorts(t *testing.T) {
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("management-secret"))
	}))
	defer mgmt.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ordinary-upstream"))
	}))
	defer other.Close()

	guard := NewSelfGuard()
	guard.ProtectAddr(mgmt.Listener.Addr().String())
	httpP, _ := startHTTP(t, HTTPConfig{Addr: "127.0.0.1:0", TargetPolicy: OpenPolicy(), Guard: guard})
	socksP, _ := startSOCKS(t, SOCKS5Config{Addr: "127.0.0.1:0", TargetPolicy: OpenPolicy(), Guard: guard})
	mgmtPort, otherPort := portOf(mgmt.URL), portOf(other.URL)

	for _, host := range []string{"127.0.0.1", "localhost"} {
		got := rawProxyRequest(t, httpP.Addr().String(), fmt.Sprintf("GET http://%s:%s/api/v1/settings HTTP/1.1\r\nHost: %s:%s\r\nConnection: close\r\n\r\n", host, mgmtPort, host, mgmtPort))
		if !strings.Contains(got, " 403 ") {
			t.Fatalf("HTTP proxy to protected %s port: got %q, want 403", host, got)
		}
		got = rawProxyRequest(t, httpP.Addr().String(), fmt.Sprintf("CONNECT %s:%s HTTP/1.1\r\nHost: %s:%s\r\n\r\n", host, mgmtPort, host, mgmtPort))
		if !strings.Contains(got, " 403 ") {
			t.Fatalf("HTTP CONNECT to protected %s port: got %q, want 403", host, got)
		}
	}
	// Ordinary local upstreams keep working under the open policy.
	got := rawProxyRequest(t, httpP.Addr().String(), fmt.Sprintf("GET http://127.0.0.1:%s/ HTTP/1.1\r\nHost: 127.0.0.1:%s\r\nConnection: close\r\n\r\n", otherPort, otherPort))
	if !strings.Contains(got, " 200 ") {
		t.Fatalf("HTTP proxy to unprotected port: got %q, want 200", got)
	}

	// SOCKS5: protected port → non-zero reply; each proxy also protects the
	// other's port because the guard is shared.
	for _, target := range []string{mgmtPort, portOf("http://" + httpP.Addr().String()), portOf("http://" + socksP.Addr().String())} {
		if rep := socksReply(t, socksP.Addr().String(), "127.0.0.1", target); rep == 0 {
			t.Fatalf("SOCKS5 CONNECT to protected port %s succeeded", target)
		}
	}
	if rep := socksReply(t, socksP.Addr().String(), "127.0.0.1", otherPort); rep != 0 {
		t.Fatalf("SOCKS5 CONNECT to unprotected port failed with reply %d", rep)
	}
}

// socksReply performs an unauthenticated SOCKS5 CONNECT and returns REP.
func socksReply(t *testing.T, proxyAddr, host, port string) byte {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write([]byte{5, 1, 0})
	sel := make([]byte, 2)
	if _, err := io.ReadFull(conn, sel); err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	req := append([]byte{5, 1, 0, 1}, net.ParseIP(host).To4()...)
	req = append(req, byte(p>>8), byte(p))
	_, _ = conn.Write(req)
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return 0xff
	}
	return reply[1]
}

func TestMetadataAddressesDeniedByEveryPolicy(t *testing.T) {
	for _, raw := range []string{"169.254.169.254", "169.254.170.2", "100.100.100.200", "fd00:ec2::254"} {
		ip := net.ParseIP(raw)
		for name, p := range map[string]TargetPolicy{"open": OpenPolicy(), "local": LocalOnlyPolicy(), "public": {AllowPublic: true}} {
			if p.allows(ip) {
				t.Fatalf("%s policy allows metadata address %s", name, raw)
			}
		}
	}
	if !OpenPolicy().allows(net.ParseIP("169.254.10.10")) {
		t.Fatal("ordinary link-local address should still follow the policy")
	}
}

func TestSelfGuardMatchesLocalInterfaceAddresses(t *testing.T) {
	g := NewSelfGuard(8790)
	g.localAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("10.1.2.3"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	cases := []struct {
		ip   string
		port int
		want bool
	}{
		{"127.0.0.1", 8790, true},
		{"::1", 8790, true},
		{"0.0.0.0", 8790, true},
		{"10.1.2.3", 8790, true},
		{"10.1.2.4", 8790, false},
		{"127.0.0.1", 8791, false},
	}
	for _, c := range cases {
		if got := g.Blocks(net.ParseIP(c.ip), c.port); got != c.want {
			t.Fatalf("Blocks(%s, %d) = %v, want %v", c.ip, c.port, got, c.want)
		}
	}
	g.localAddrs = func() ([]net.Addr, error) { return nil, fmt.Errorf("boom") }
	if !g.Blocks(net.ParseIP("192.0.2.1"), 8790) {
		t.Fatal("guard must fail closed when interface addresses are unavailable")
	}
	var nilGuard *SelfGuard
	if nilGuard.Blocks(net.ParseIP("127.0.0.1"), 8790) {
		t.Fatal("nil guard must not block")
	}
}
