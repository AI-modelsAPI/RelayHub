package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"relayhub/internal/logging"
)

type HTTPConfig struct {
	Addr           string
	Listener       net.Listener
	Username       string
	Password       string
	MaxHeaderBytes int
	MaxBodyBytes   int64
	DialTimeout    time.Duration
	IdleTimeout    time.Duration
	TargetPolicy   TargetPolicy
	// Guard lists RelayHub's own listener ports that must never be proxied
	// to. The server's own port is always added (AUDIT 2026-09-24 F1/F3).
	Guard  *SelfGuard
	Logger *logging.Logger
}

type HTTPServer struct {
	*baseServer
	cfg HTTPConfig
}

func NewHTTP(cfg HTTPConfig) (*HTTPServer, error) {
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, fmt.Errorf("http proxy username and password must be configured together")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8787"
	}
	if cfg.MaxHeaderBytes <= 0 {
		cfg.MaxHeaderBytes = 1 << 20
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 64 << 20
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 15 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 2 * time.Minute
	}
	if cfg.TargetPolicy == (TargetPolicy{}) {
		cfg.TargetPolicy = LocalOnlyPolicy()
	}
	b, err := newBase(cfg.Addr, cfg.Listener)
	if err != nil {
		return nil, err
	}
	b.dialTimeout, b.idleTimeout = cfg.DialTimeout, cfg.IdleTimeout
	b.useGuard(cfg.Guard)
	return &HTTPServer{baseServer: b, cfg: cfg}, nil
}

func (s *HTTPServer) Start(ctx context.Context) error {
	if !s.begin(ctx) {
		return fmt.Errorf("proxy already started")
	}
	for {
		c, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			default:
				return err
			}
		}
		if !s.register(c) {
			_ = c.Close()
			continue
		}
		go func() {
			defer s.unregister(c)
			s.handle(s.context(), c)
		}()
	}
}

func (s *HTTPServer) auth(r *http.Request) bool {
	if s.cfg.Username == "" && s.cfg.Password == "" {
		return true
	}
	value := r.Header.Get("Proxy-Authorization")
	if value == "" {
		return false
	}
	parts := strings.SplitN(value, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	credentials := strings.SplitN(string(decoded), ":", 2)
	if len(credentials) != 2 {
		return false
	}
	u, p, ok := credentials[0], credentials[1], true
	return ok && u == s.cfg.Username && p == s.cfg.Password
}

func (s *HTTPServer) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	s.setIdle(c)
	r, br, err := readBoundedRequest(c, s.cfg.MaxHeaderBytes)
	if err != nil {
		return
	}
	if !s.auth(r) {
		_, _ = fmt.Fprint(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=relayhub\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	requestID := strconv.FormatInt(time.Now().UnixNano(), 10)
	if r.Method == http.MethodConnect {
		s.handleConnect(ctx, c, br, r, requestID)
		return
	}
	s.handleHTTP(ctx, c, r, requestID)
}

func (s *HTTPServer) handleConnect(ctx context.Context, c net.Conn, br *bufio.Reader, r *http.Request, requestID string) {
	if r.URL == nil || r.URL.Path != "" || r.URL.RawQuery != "" || r.Host == "" {
		return
	}
	up, host, port, err := s.dialTarget(ctx, r.Host, "", true, s.cfg.TargetPolicy)
	if err != nil {
		s.rejectDial(c, requestID, "proxy_connect", host, port, err)
		return
	}
	defer up.Close()
	_, _ = fmt.Fprint(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
	s.log("info", "proxy_connect", requestID, map[string]any{"host": host, "port": port})
	// http.ReadRequest may have read the CONNECT payload into its buffered
	// reader. Tunnel through that reader so a coalesced headers+payload write is
	// delivered byte-for-byte to the upstream.
	_ = tunnel(ctx, &bufferedConn{Conn: c, reader: br}, &deadlineConn{Conn: up, timeout: s.idleTimeout}, s.idleTimeout)
}

func readBoundedRequest(c net.Conn, max int) (*http.Request, *bufio.Reader, error) {
	br := bufio.NewReaderSize(c, minInt(max, 32<<10))
	var header bytes.Buffer
	for header.Len() <= max {
		line, err := br.ReadBytes('\n')
		header.Write(line)
		if header.Len() > max {
			return nil, nil, fmt.Errorf("proxy header exceeds limit")
		}
		if err != nil {
			return nil, nil, err
		}
		if bytes.HasSuffix(header.Bytes(), []byte("\r\n\r\n")) || bytes.HasSuffix(header.Bytes(), []byte("\n\n")) {
			reqReader := bufio.NewReaderSize(io.MultiReader(bytes.NewReader(header.Bytes()), br), minInt(max, 32<<10))
			r, err := http.ReadRequest(reqReader)
			return r, reqReader, err
		}
	}
	return nil, nil, fmt.Errorf("proxy header exceeds limit")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *HTTPServer) handleHTTP(ctx context.Context, c net.Conn, r *http.Request, requestID string) {
	_ = ctx
	// A forward proxy only accepts absolute-form request targets
	// ("GET http://host/path"). Origin-form requests ("GET /") used to fall
	// back to the Host header, which for a direct hit is the proxy itself:
	// the proxy dialed itself recursively until file descriptors ran out and
	// every RelayHub plane stopped accepting connections (AUDIT 2026-09-24
	// F1). A web page could trigger that with <img src=http://127.0.0.1:8787/>.
	if !r.URL.IsAbs() || r.URL.Host == "" {
		s.log("warn", "proxy_http", requestID, map[string]any{"reason": "origin_form_rejected"})
		_, _ = fmt.Fprint(c, "HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain\r\nContent-Length: 50\r\nConnection: close\r\n\r\nRelayHub proxy requires absolute-form request URIs")
		return
	}
	authority := r.URL.Host
	if !strings.EqualFold(r.URL.Scheme, "http") {
		return
	}
	// Do not forward proxy-only hop-by-hop headers or absolute-form URLs.
	r.RequestURI = ""
	r.URL.Scheme = ""
	r.URL.Host = ""
	r.Header.Del("Proxy-Authorization")
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Connection")
	if r.ContentLength > s.cfg.MaxBodyBytes {
		_, _ = fmt.Fprint(c, "HTTP/1.1 413 Request Entity Too Large\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	if r.Body != nil && s.cfg.MaxBodyBytes > 0 && r.ContentLength < 0 {
		body, err := readUnknownBody(r.Body, s.cfg.MaxBodyBytes)
		_ = r.Body.Close()
		if err != nil {
			_, _ = fmt.Fprint(c, "HTTP/1.1 413 Request Entity Too Large\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.TransferEncoding = nil
	}
	up, host, port, err := s.dialTarget(ctx, authority, "80", false, s.cfg.TargetPolicy)
	if err != nil {
		s.rejectDial(c, requestID, "proxy_http", host, port, err)
		return
	}
	defer up.Close()
	s.setIdle(up)
	if err := r.Write(up); err != nil {
		return
	}
	response, err := http.ReadResponse(bufio.NewReaderSize(idleConn{Conn: up, timeout: s.idleTimeout}, minInt(s.cfg.MaxHeaderBytes, 32<<10)), r)
	if err != nil {
		return
	}
	defer response.Body.Close()
	_ = response.Write(c)
	s.log("info", "proxy_http", requestID, map[string]any{"host": host, "port": port, "method": r.Method})
}

// rejectDial answers a request whose upstream could not be reached. A policy
// denial and an unreachable target used to share one 403 on CONNECT (and a
// silent close on plain HTTP), so clients could not tell "blocked by RelayHub"
// from "upstream down" and tests could not assert policy without the public
// internet (AUDIT RH-21). Policy verdicts stay 403; dial/resolve failures are
// 502 Bad Gateway.
func (s *HTTPServer) rejectDial(c net.Conn, requestID, kind, host string, port int, err error) {
	status, suffix := "502 Bad Gateway", "_failed"
	if errors.Is(err, errInvalidTarget) {
		status, suffix = "403 Forbidden", "_denied"
	}
	s.log("warn", kind+suffix, requestID, map[string]any{"host": host, "port": port, "error": err.Error()})
	_, _ = fmt.Fprintf(c, "HTTP/1.1 %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", status)
}

func (s *HTTPServer) log(level, kind, requestID string, fields map[string]any) {
	if s.cfg.Logger != nil {
		_ = s.cfg.Logger.Event(level, kind, requestID, fields)
	}
}

type deadlineConn struct {
	net.Conn
	timeout time.Duration
}

func (c *deadlineConn) Read(p []byte) (int, error) {
	if c.timeout > 0 {
		_ = c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	}
	return c.Conn.Read(p)
}
func (c *deadlineConn) Write(p []byte) (int, error) {
	if c.timeout > 0 {
		_ = c.Conn.SetWriteDeadline(time.Now().Add(c.timeout))
	}
	return c.Conn.Write(p)
}

// Unknown-length bodies are fully buffered before dialing or forwarding. This
// makes MaxBodyBytes an all-or-nothing request limit, at the cost of buffering
// up to the configured limit plus one byte in memory.
func readUnknownBody(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return io.ReadAll(body)
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("proxy request body exceeds limit")
	}
	return data, nil
}

type bufferedConn struct {
	net.Conn
	reader io.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
