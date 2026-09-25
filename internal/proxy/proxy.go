package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server interface {
	Start(context.Context) error
	Shutdown(context.Context) error
	Addr() net.Addr
}

// TargetPolicy is deliberately explicit. A zero policy permits only loopback
// and local-link targets, which is safe for a local development proxy and does
// not turn an accidentally exposed listener into an open internet proxy.
type TargetPolicy struct {
	AllowLocal   bool
	AllowPrivate bool
	AllowPublic  bool
}

func LocalOnlyPolicy() TargetPolicy { return TargetPolicy{AllowLocal: true} }
func OpenPolicy() TargetPolicy {
	return TargetPolicy{AllowLocal: true, AllowPrivate: true, AllowPublic: true}
}

func (p TargetPolicy) allows(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// Cloud instance-metadata endpoints are never a legitimate egress target
	// for an AI traffic proxy, whatever the policy says (AUDIT 2026-09-24 F2).
	if isMetadataIP(ip) {
		return false
	}
	if isLocalIP(ip) {
		return p.AllowLocal
	}
	if ip.IsPrivate() || isSharedAddressSpace(ip) {
		return p.AllowPrivate
	}
	return p.AllowPublic
}

// cgnat is RFC 6598 shared address space (carrier NAT, Tailscale). It is not
// publicly routable, so it belongs with the private ranges rather than being
// reachable under a public-only policy (AUDIT 2026-09-24 F2).
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isSharedAddressSpace(ip net.IP) bool { return cgnat.Contains(ip) }

func isLocalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// metadataIPs are well-known cloud instance-metadata service addresses (AWS,
// GCP, Azure, OpenStack; AWS ECS task metadata; Alibaba Cloud; AWS IPv6).
var metadataIPs = []net.IP{
	net.ParseIP("169.254.169.254"),
	net.ParseIP("169.254.170.2"),
	net.ParseIP("100.100.100.200"),
	net.ParseIP("fd00:ec2::254"),
}

func isMetadataIP(ip net.IP) bool {
	for _, m := range metadataIPs {
		if m.Equal(ip) {
			return true
		}
	}
	return false
}

// SelfGuard records the TCP ports of RelayHub's own listeners (proxies,
// gateway, management API). A proxy must never relay to one of those ports on
// an address of this host: doing so let any proxy client reach the loopback
// management API without passing its browser boundary (AUDIT 2026-09-24 F3)
// and let an origin-form request make the HTTP proxy dial itself until file
// descriptors ran out (F1). The guard is independent of TargetPolicy so even
// an "open" policy cannot re-enable those paths.
type SelfGuard struct {
	mu    sync.RWMutex
	ports map[int]struct{}
	// localAddrs is swappable in tests; defaults to net.InterfaceAddrs.
	localAddrs func() ([]net.Addr, error)
}

// NewSelfGuard returns a guard protecting the given ports.
func NewSelfGuard(ports ...int) *SelfGuard {
	g := &SelfGuard{ports: map[int]struct{}{}, localAddrs: net.InterfaceAddrs}
	for _, p := range ports {
		g.ProtectPort(p)
	}
	return g
}

// ProtectPort adds a port to the guard. Non-positive ports are ignored.
func (g *SelfGuard) ProtectPort(port int) {
	if g == nil || port <= 0 {
		return
	}
	g.mu.Lock()
	g.ports[port] = struct{}{}
	g.mu.Unlock()
}

// ProtectAddr adds the port of a listener address such as "127.0.0.1:8790"
// or a net.Addr's String(). Unparseable addresses are ignored.
func (g *SelfGuard) ProtectAddr(addr string) {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}
	if p, err := strconv.Atoi(portStr); err == nil {
		g.ProtectPort(p)
	}
}

// Blocks reports whether dialing ip:port would reach one of this host's own
// protected listeners.
func (g *SelfGuard) Blocks(ip net.IP, port int) bool {
	if g == nil || ip == nil {
		return false
	}
	g.mu.RLock()
	_, protected := g.ports[port]
	g.mu.RUnlock()
	if !protected {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	addrs, err := g.localAddrs()
	if err != nil {
		// Fail closed: without the interface list we cannot prove the target
		// is not this host.
		return true
	}
	for _, a := range addrs {
		var local net.IP
		switch v := a.(type) {
		case *net.IPNet:
			local = v.IP
		case *net.IPAddr:
			local = v.IP
		}
		if local != nil && local.Equal(ip) {
			return true
		}
	}
	return false
}

type baseServer struct {
	listener                 net.Listener
	mu                       sync.RWMutex
	addr                     net.Addr
	active                   map[net.Conn]struct{}
	closeOnce                sync.Once
	startOnce                sync.Once
	done                     chan struct{}
	connectionsDone          chan struct{}
	lifecycleDone            chan struct{}
	closed                   bool
	ctx                      context.Context
	cancel                   context.CancelFunc
	dialTimeout, idleTimeout time.Duration
	// guard always protects this server's own listener port (see SelfGuard).
	guard *SelfGuard
}

// useGuard installs g (or a fresh guard when nil) and protects the server's
// own listener port with it.
func (b *baseServer) useGuard(g *SelfGuard) {
	if g == nil {
		g = NewSelfGuard()
	}
	g.ProtectAddr(b.addr.String())
	b.guard = g
}

func newBase(addr string, supplied net.Listener) (*baseServer, error) {
	// Listener ownership is intentionally constructor-time: a successfully
	// constructed server owns the listener even if Start is never called.
	// Shutdown is safe before Start and always releases it.
	l := supplied
	var err error
	if l == nil {
		l, err = net.Listen("tcp", addr)
		if err != nil {
			return nil, err
		}
	}
	return &baseServer{
		listener: l, addr: l.Addr(), active: make(map[net.Conn]struct{}),
		done: make(chan struct{}), connectionsDone: closedChan(), lifecycleDone: closedChan(),
		dialTimeout: 15 * time.Second, idleTimeout: 2 * time.Minute,
	}, nil
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

func (b *baseServer) Addr() net.Addr {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.addr
}

func (b *baseServer) begin(ctx context.Context) bool {
	started := false
	b.startOnce.Do(func() {
		started = true
		b.mu.Lock()
		b.ctx, b.cancel = context.WithCancel(ctx)
		b.lifecycleDone = make(chan struct{})
		b.mu.Unlock()
		go func() {
			defer close(b.lifecycleDone)
			select {
			case <-ctx.Done():
				b.closeListener()
				b.closeActive()
			case <-b.context().Done():
			}
		}()
	})
	return started
}

func (b *baseServer) context() context.Context {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.ctx == nil {
		return context.Background()
	}
	return b.ctx
}

func (b *baseServer) register(c net.Conn) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false
	}
	if len(b.active) == 0 {
		b.connectionsDone = make(chan struct{})
	}
	b.active[c] = struct{}{}
	return true
}

func (b *baseServer) unregister(c net.Conn) {
	b.mu.Lock()
	delete(b.active, c)
	if len(b.active) == 0 {
		select {
		case <-b.connectionsDone:
		default:
			close(b.connectionsDone)
		}
	}
	b.mu.Unlock()
}

func (b *baseServer) closeActive() {
	b.mu.RLock()
	connections := make([]net.Conn, 0, len(b.active))
	for c := range b.active {
		connections = append(connections, c)
	}
	b.mu.RUnlock()
	for _, c := range connections {
		_ = c.Close()
	}
}

func (b *baseServer) closeListener() {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		cancel := b.cancel
		b.mu.Unlock()
		close(b.done)
		if cancel != nil {
			cancel()
		}
		_ = b.listener.Close()
	})
}

func (b *baseServer) Shutdown(ctx context.Context) error {
	b.closeListener()
	b.closeActive()
	b.mu.RLock()
	connectionsDone, lifecycleDone := b.connectionsDone, b.lifecycleDone
	b.mu.RUnlock()
	wait := func(done <-chan struct{}) bool {
		select {
		case <-done:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if !wait(connectionsDone) || !wait(lifecycleDone) {
		b.closeActive()
		return ctx.Err()
	}
	return nil
}

func dial(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{Timeout: timeout}
	return d.DialContext(ctx, network, address)
}

func parseAuthority(authority, defaultPort string, requirePort bool) (string, int, error) {
	authority = strings.TrimSpace(authority)
	if authority == "" || strings.ContainsAny(authority, "\r\n") || strings.Contains(authority, "@") {
		return "", 0, errInvalidTarget
	}
	if strings.Contains(authority, "/") || strings.Contains(authority, "?") || strings.Contains(authority, "#") {
		return "", 0, errInvalidTarget
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		if requirePort || !strings.Contains(err.Error(), "missing port") {
			return "", 0, errInvalidTarget
		}
		host, port = authority, defaultPort
	}
	if host == "" || strings.ContainsAny(host, " \t") {
		return "", 0, errInvalidTarget
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", 0, errInvalidTarget
	}
	return strings.Trim(host, "[]"), portNumber, nil
}

func resolveTarget(ctx context.Context, authority, defaultPort string, requirePort bool, policy TargetPolicy, guard *SelfGuard) (string, string, int, error) {
	host, port, err := parseAuthority(authority, defaultPort, requirePort)
	if err != nil {
		return "", "", 0, err
	}
	ips := net.ParseIP(host)
	if ips != nil {
		if !policy.allows(ips) || guard.Blocks(ips, port) {
			return "", "", 0, fmt.Errorf("target blocked by policy: %w", errInvalidTarget)
		}
		return net.JoinHostPort(ips.String(), strconv.Itoa(port)), host, port, nil
	}
	if strings.EqualFold(host, "localhost") {
		loopback := net.IPv4(127, 0, 0, 1)
		if !policy.AllowLocal || guard.Blocks(loopback, port) {
			return "", "", 0, fmt.Errorf("target blocked by policy: %w", errInvalidTarget)
		}
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), host, port, nil
	}
	resolved, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(resolved) == 0 {
		if err == nil {
			err = errInvalidTarget
		}
		return "", "", 0, err
	}
	for _, ip := range resolved {
		if policy.allows(ip) && !guard.Blocks(ip, port) {
			return net.JoinHostPort(ip.String(), strconv.Itoa(port)), host, port, nil
		}
	}
	return "", "", 0, fmt.Errorf("target blocked by policy: %w", errInvalidTarget)
}

func (b *baseServer) dialTarget(ctx context.Context, authority, defaultPort string, requirePort bool, policy TargetPolicy) (net.Conn, string, int, error) {
	address, host, port, err := resolveTarget(ctx, authority, defaultPort, requirePort, policy, b.guard)
	if err != nil {
		return nil, "", 0, err
	}
	c, err := dial(ctx, "tcp", address, b.dialTimeout)
	return c, host, port, err
}

func (b *baseServer) setIdle(c net.Conn) {
	if b.idleTimeout > 0 {
		_ = c.SetDeadline(time.Now().Add(b.idleTimeout))
	}
}

type idleConn struct {
	net.Conn
	timeout time.Duration
}

func (c idleConn) Read(p []byte) (int, error) {
	if c.timeout > 0 {
		_ = c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	}
	return c.Conn.Read(p)
}
func (c idleConn) Write(p []byte) (int, error) {
	if c.timeout > 0 {
		_ = c.Conn.SetWriteDeadline(time.Now().Add(c.timeout))
	}
	return c.Conn.Write(p)
}

func halfClose(c net.Conn, write bool) {
	if tcp, ok := c.(*net.TCPConn); ok {
		if write {
			_ = tcp.CloseWrite()
		} else {
			_ = tcp.CloseRead()
		}
	}
}

func tunnel(ctx context.Context, client, upstream net.Conn, idle time.Duration) error {
	cc, uc := idleConn{Conn: client, timeout: idle}, idleConn{Conn: upstream, timeout: idle}
	errCh := make(chan error, 2)
	go func() {
		_, err := ioCopy(uc, cc)
		halfClose(upstream, true)
		errCh <- err
	}()
	go func() {
		_, err := ioCopy(cc, uc)
		halfClose(client, true)
		errCh <- err
	}()
	select {
	case err := <-errCh:
		// A FIN in one direction is a half-close, not permission to discard
		// the reverse stream. Let it drain until the peer closes or the context
		// is canceled.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err2 := <-errCh:
			if err != nil && !errors.Is(err, net.ErrClosed) {
				return err
			}
			return err2
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Kept as a variable to make copy behavior easy to exercise with race tests.
var ioCopy = func(dst io.Writer, src io.Reader) (int64, error) { return io.Copy(dst, src) }

var errInvalidTarget = errors.New("invalid proxy target")
