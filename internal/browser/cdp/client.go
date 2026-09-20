package cdp

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

// TargetPage represents a browser target page returned by /json/list or /json/new.
type TargetPage struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// Client is a lightweight, zero-external-dependency CDP client communicating over WebSocket.
type Client struct {
	conn       net.Conn
	rw         *bufio.ReadWriter
	msgID      uint64
	pendingMu  sync.Mutex
	pending    map[uint64]chan cdpResponse
	eventsMu   sync.RWMutex
	eventChans map[string][]chan json.RawMessage
	closeCh    chan struct{}
	closeOnce  sync.Once
}

type cdpRequest struct {
	ID     uint64      `json:"id"`
	Method string      `json:"method"`
	Params interface{} `json:"params,omitempty"`
}

type cdpResponse struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpEvent struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string {
	return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message)
}

// GetFirstPageTarget discovers the primary target page websocket URL from Chrome remote debugging port.
func GetFirstPageTarget(ctx context.Context, debugPort int) (*TargetPage, error) {
	reqURL := fmt.Sprintf("http://127.0.0.1:%d/json/list", debugPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var targets []TargetPage
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			return &t, nil
		}
	}
	// If no page target found, try /json/new
	newURL := fmt.Sprintf("http://127.0.0.1:%d/json/new", debugPort)
	newReq, err := http.NewRequestWithContext(ctx, http.MethodPut, newURL, nil)
	if err != nil {
		return nil, errors.New("no page target found and failed to create new target request")
	}
	newResp, err := http.DefaultClient.Do(newReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create target: %w", err)
	}
	defer newResp.Body.Close()
	var newTarget TargetPage
	if err := json.NewDecoder(newResp.Body).Decode(&newTarget); err != nil {
		return nil, err
	}
	return &newTarget, nil
}

// Connect dials the WebSocketDebuggerURL and performs a RFC 6455 handshake.
func Connect(ctx context.Context, wsURL string) (*Client, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return nil, fmt.Errorf("invalid websocket url %q: %w", wsURL, err)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host = net.JoinHostPort(host, "80")
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("failed to dial websocket host %s: %w", host, err)
	}

	// Generate Sec-WebSocket-Key
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		conn.Close()
		return nil, err
	}
	secKey := base64.StdEncoding.EncodeToString(nonce)

	reqPath := u.Path
	if reqPath == "" {
		reqPath = "/"
	}
	if u.RawQuery != "" {
		reqPath += "?" + u.RawQuery
	}

	handshake := fmt.Sprintf(
		"GET %s HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\n"+
			"Sec-WebSocket-Version: 13\r\n\r\n",
		reqPath, u.Host, secKey,
	)

	if _, err := conn.Write([]byte(handshake)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to write websocket handshake: %w", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read websocket handshake response: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("unexpected websocket upgrade status: %d", resp.StatusCode)
	}

	// Verify Sec-WebSocket-Accept
	expectedAccept := computeAccept(secKey)
	if resp.Header.Get("Sec-WebSocket-Accept") != expectedAccept {
		conn.Close()
		return nil, fmt.Errorf("invalid Sec-WebSocket-Accept header")
	}

	c := &Client{
		conn:       conn,
		rw:         bufio.NewReadWriter(reader, bufio.NewWriter(conn)),
		pending:    make(map[uint64]chan cdpResponse),
		eventChans: make(map[string][]chan json.RawMessage),
		closeCh:    make(chan struct{}),
	}

	go c.readLoop()
	return c, nil
}

func computeAccept(key string) string {
	const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key + magicGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Close closes connection and releases resources.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closeCh)
		c.conn.Close()
		c.pendingMu.Lock()
		for _, ch := range c.pending {
			close(ch)
		}
		c.pending = make(map[uint64]chan cdpResponse)
		c.pendingMu.Unlock()
	})
	return nil
}

// Call sends a CDP command and waits for the response.
func (c *Client) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := atomic.AddUint64(&c.msgID, 1)
	req := cdpRequest{
		ID:     id,
		Method: method,
		Params: params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	respCh := make(chan cdpResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.writeFrame(1, data); err != nil { // 1 = text frame
		return nil, fmt.Errorf("failed to send frame: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closeCh:
		return nil, errors.New("cdp client closed")
	case resp, ok := <-respCh:
		if !ok {
			return nil, errors.New("cdp connection closed")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// writeFrame writes a masked client-to-server WebSocket frame.
func (c *Client) writeFrame(opcode byte, payload []byte) error {
	length := len(payload)
	var header []byte

	// Fin=1, Opcode
	header = append(header, 0x80|opcode)

	// Mask=1
	maskKey := make([]byte, 4)
	if _, err := rand.Read(maskKey); err != nil {
		return err
	}

	if length <= 125 {
		header = append(header, 0x80|byte(length))
	} else if length <= 65535 {
		header = append(header, 0x80|126)
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, uint16(length))
		header = append(header, b...)
	} else {
		header = append(header, 0x80|127)
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(length))
		header = append(header, b...)
	}

	header = append(header, maskKey...)

	// Apply mask to payload copy
	masked := make([]byte, length)
	for i := 0; i < length; i++ {
		masked[i] = payload[i] ^ maskKey[i%4]
	}

	c.rw.Writer.Write(header)
	c.rw.Writer.Write(masked)
	return c.rw.Writer.Flush()
}

func (c *Client) readLoop() {
	defer c.Close()

	for {
		// Read 2-byte header
		b0, err := c.rw.ReadByte()
		if err != nil {
			return
		}
		b1, err := c.rw.ReadByte()
		if err != nil {
			return
		}

		opcode := b0 & 0x0f
		isMasked := (b1 & 0x80) != 0
		var payloadLen int64 = int64(b1 & 0x7f)

		if payloadLen == 126 {
			var b [2]byte
			if _, err := io.ReadFull(c.rw, b[:]); err != nil {
				return
			}
			payloadLen = int64(binary.BigEndian.Uint16(b[:]))
		} else if payloadLen == 127 {
			var b [8]byte
			if _, err := io.ReadFull(c.rw, b[:]); err != nil {
				return
			}
			payloadLen = int64(binary.BigEndian.Uint64(b[:]))
		}

		var mask [4]byte
		if isMasked {
			if _, err := io.ReadFull(c.rw, mask[:]); err != nil {
				return
			}
		}

		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(c.rw, payload); err != nil {
			return
		}

		if isMasked {
			for i := int64(0); i < payloadLen; i++ {
				payload[i] ^= mask[i%4]
			}
		}

		if opcode == 0x8 { // Close frame
			return
		}
		if opcode == 0x9 { // Ping
			_ = c.writeFrame(0xA, payload) // Pong
			continue
		}
		if opcode != 0x1 { // Not text
			continue
		}

		c.dispatch(payload)
	}
}

func (c *Client) dispatch(data []byte) {
	// Can be response (with id) or event (with method)
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}

	if idVal, hasID := raw["id"]; hasID {
		idNum, ok := idVal.(float64)
		if !ok {
			return
		}
		var resp cdpResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[uint64(idNum)]
		c.pendingMu.Unlock()
		if ok {
			ch <- resp
		}
		return
	}
}
