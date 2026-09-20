package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"relayhub/internal/logging"
)

type SOCKS5Config struct {
	Addr         string
	Listener     net.Listener
	Username     string
	Password     string
	MaxMethods   int
	MaxAuthBytes int
	DialTimeout  time.Duration
	IdleTimeout  time.Duration
	TargetPolicy TargetPolicy
	Logger       *logging.Logger
}

type SOCKS5Server struct {
	*baseServer
	cfg SOCKS5Config
}

func NewSOCKS5(cfg SOCKS5Config) (*SOCKS5Server, error) {
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, errors.New("socks5 username and password must be configured together")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8788"
	}
	if cfg.MaxMethods <= 0 {
		cfg.MaxMethods = 255
	}
	if cfg.MaxAuthBytes <= 0 {
		cfg.MaxAuthBytes = 255
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
	return &SOCKS5Server{baseServer: b, cfg: cfg}, nil
}

func (s *SOCKS5Server) Start(ctx context.Context) error {
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

func (s *SOCKS5Server) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	s.setIdle(c)
	r := bufio.NewReaderSize(c, 512)
	version, err := r.ReadByte()
	if err != nil || version != 5 {
		return
	}
	nmethods, err := r.ReadByte()
	if err != nil || int(nmethods) > s.cfg.MaxMethods {
		return
	}
	methods := make([]byte, nmethods)
	if _, err = io.ReadFull(r, methods); err != nil {
		return
	}
	method := byte(0xff)
	for _, candidate := range methods {
		if s.cfg.Username != "" && candidate == 2 {
			method = 2
			break
		}
		if s.cfg.Username == "" && candidate == 0 {
			method = 0
		}
	}
	if _, err = c.Write([]byte{5, method}); err != nil || method == 0xff {
		return
	}
	if method == 2 && !s.auth(r, c) {
		return
	}

	header := make([]byte, 4)
	if _, err = io.ReadFull(r, header); err != nil || header[0] != 5 || header[1] != 1 || header[2] != 0 {
		s.reply(c, 7)
		return
	}
	authority, err := readAddr(r, header[3])
	if err != nil {
		s.reply(c, 8)
		return
	}
	up, host, port, err := s.dialTarget(ctx, authority, "", true, s.cfg.TargetPolicy)
	if err != nil {
		s.log("warn", "socks_connect_denied", host, port, err)
		s.reply(c, replyCode(err))
		return
	}
	defer up.Close()
	if err = writeSuccess(c, up.LocalAddr()); err != nil {
		return
	}
	s.log("info", "socks_connect", host, port, nil)
	_ = tunnel(ctx, c, &deadlineConn{Conn: up, timeout: s.idleTimeout}, s.idleTimeout)
}

func (s *SOCKS5Server) auth(r *bufio.Reader, c net.Conn) bool {
	version, err := r.ReadByte()
	if err != nil || version != 1 {
		return false
	}
	ulen, err := r.ReadByte()
	if err != nil || int(ulen) > s.cfg.MaxAuthBytes {
		return false
	}
	user := make([]byte, ulen)
	if _, err = io.ReadFull(r, user); err != nil {
		return false
	}
	plen, err := r.ReadByte()
	if err != nil || int(plen) > s.cfg.MaxAuthBytes {
		return false
	}
	password := make([]byte, plen)
	if _, err = io.ReadFull(r, password); err != nil {
		return false
	}
	if string(user) != s.cfg.Username || string(password) != s.cfg.Password {
		_, _ = c.Write([]byte{1, 1})
		return false
	}
	_, err = c.Write([]byte{1, 0})
	return err == nil
}

func (s *SOCKS5Server) reply(c net.Conn, code byte) {
	_, _ = c.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
}

func writeSuccess(c net.Conn, addr net.Addr) error {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || tcp == nil {
		return writeReply(c, 0, net.IPv4zero, 0)
	}
	return writeReply(c, 0, tcp.IP, tcp.Port)
}

func writeReply(c net.Conn, code byte, ip net.IP, port int) error {
	if v4 := ip.To4(); v4 != nil {
		buf := []byte{5, code, 0, 1, v4[0], v4[1], v4[2], v4[3], byte(port >> 8), byte(port)}
		_, err := c.Write(buf)
		return err
	}
	v6 := ip.To16()
	if v6 == nil {
		v6 = net.IPv6zero
	}
	buf := []byte{5, code, 0, 4}
	buf = append(buf, v6...)
	buf = append(buf, byte(port>>8), byte(port))
	_, err := c.Write(buf)
	return err
}

func readAddr(r *bufio.Reader, atyp byte) (string, error) {
	var host string
	switch atyp {
	case 1:
		b := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		host = net.IP(b).String()
	case 3:
		n, err := r.ReadByte()
		if err != nil || n == 0 {
			return "", errors.New("invalid socks domain")
		}
		b := make([]byte, n)
		if _, err = io.ReadFull(r, b); err != nil {
			return "", err
		}
		host = string(b)
	case 4:
		b := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		host = net.IP(b).String()
	default:
		return "", errors.New("unsupported socks address")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(r, portBytes); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBytes)
	if port == 0 {
		return "", errors.New("invalid socks port")
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func replyCode(err error) byte {
	if errors.Is(err, errInvalidTarget) {
		return 8
	}
	return 5
}

func (s *SOCKS5Server) log(level, kind, host string, port int, err error) {
	if s.cfg.Logger == nil {
		return
	}
	fields := map[string]any{"host": host, "port": port}
	if err != nil {
		fields["error"] = err.Error()
	}
	_ = s.cfg.Logger.Event(level, kind, "", fields)
}
