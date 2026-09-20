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
	if isLocalIP(ip) {
		return p.AllowLocal
	}
	if ip.IsPrivate() {
		return p.AllowPrivate
	}
	return p.AllowPublic
}

func isLocalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
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

func resolveTarget(ctx context.Context, authority, defaultPort string, requirePort bool, policy TargetPolicy) (string, string, int, error) {
	host, port, err := parseAuthority(authority, defaultPort, requirePort)
	if err != nil {
		return "", "", 0, err
	}
	ips := net.ParseIP(host)
	if ips != nil {
		if !policy.allows(ips) {
			return "", "", 0, fmt.Errorf("target blocked by policy: %w", errInvalidTarget)
		}
		return net.JoinHostPort(ips.String(), strconv.Itoa(port)), host, port, nil
	}
	if strings.EqualFold(host, "localhost") {
		if !policy.AllowLocal {
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
		if policy.allows(ip) {
			return net.JoinHostPort(ip.String(), strconv.Itoa(port)), host, port, nil
		}
	}
	return "", "", 0, fmt.Errorf("target blocked by policy: %w", errInvalidTarget)
}

func (b *baseServer) dialTarget(ctx context.Context, authority, defaultPort string, requirePort bool, policy TargetPolicy) (net.Conn, string, int, error) {
	address, host, port, err := resolveTarget(ctx, authority, defaultPort, requirePort, policy)
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
