package cdp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ConnectPipe speaks CDP over Chrome's --remote-debugging-pipe transport:
// the browser reads NUL-terminated JSON commands from its fd 3 and writes
// NUL-terminated replies and events to its fd 4. r is the read end of the
// browser's fd 4 and w the write end of its fd 3.
//
// Nothing listens on a TCP port, so other local processes and users cannot
// attach to the browser, drive it, or read the check-in profile's cookies
// while it runs (AUDIT 2026-09-24 F15).
func ConnectPipe(r io.ReadCloser, w io.WriteCloser) *Client {
	c := &Client{
		pending:    make(map[uint64]chan cdpResponse),
		eventChans: make(map[string][]chan json.RawMessage),
		closeCh:    make(chan struct{}),
		closer:     pipeCloser{r: r, w: w},
	}
	c.send = func(b []byte) error {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		msg := make([]byte, 0, len(b)+1)
		msg = append(msg, b...)
		msg = append(msg, 0)
		_, err := w.Write(msg)
		return err
	}
	go c.pipeReadLoop(r)
	return c
}

type pipeCloser struct {
	r io.Closer
	w io.Closer
}

func (p pipeCloser) Close() error {
	return errors.Join(p.w.Close(), p.r.Close())
}

// pipeReadLoop splits the stream on NUL bytes. A message larger than
// maxWSFrame ends the session instead of growing the buffer without bound.
func (c *Client) pipeReadLoop(r io.Reader) {
	defer c.Close()
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	for {
		chunk, err := br.ReadSlice(0)
		buf = append(buf, chunk...)
		switch {
		case err == nil:
			msg := buf[:len(buf)-1]
			if len(msg) > 0 {
				c.dispatch(msg)
			}
			buf = buf[:0]
		case errors.Is(err, bufio.ErrBufferFull):
			// keep accumulating the current message
		default:
			return
		}
		if len(buf) > maxWSFrame {
			return
		}
	}
}

// Session addresses one attached target over a browser-level connection
// (Target.attachToTarget with flatten=true): every command carries the
// session id and replies are matched by the connection-wide message id.
type Session struct {
	client *Client
	id     string
}

// Session returns a handle that sends commands to the given session.
func (c *Client) Session(sessionID string) *Session {
	return &Session{client: c, id: sessionID}
}

// ID is the CDP session id.
func (s *Session) ID() string { return s.id }

// Call sends a CDP command to the attached target and waits for its reply.
func (s *Session) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	return s.client.call(ctx, method, params, s.id)
}

// AttachFirstPage finds (or creates) a page target on a browser-level
// connection and attaches to it in flatten mode.
func AttachFirstPage(ctx context.Context, c *Client) (*Session, error) {
	targetID, err := firstPageTarget(ctx, c)
	if err != nil {
		return nil, err
	}
	raw, err := c.Call(ctx, "Target.attachToTarget", map[string]interface{}{
		"targetId": targetID,
		"flatten":  true,
	})
	if err != nil {
		return nil, fmt.Errorf("attach to page target: %w", err)
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &attached); err != nil || attached.SessionID == "" {
		return nil, fmt.Errorf("attach to page target: no session id in %s", string(raw))
	}
	return c.Session(attached.SessionID), nil
}

func firstPageTarget(ctx context.Context, c *Client) (string, error) {
	raw, err := c.Call(ctx, "Target.getTargets", nil)
	if err != nil {
		return "", fmt.Errorf("list targets: %w", err)
	}
	var list struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return "", fmt.Errorf("list targets: %w", err)
	}
	for _, t := range list.TargetInfos {
		if t.Type == "page" && t.TargetID != "" {
			return t.TargetID, nil
		}
	}
	raw, err = c.Call(ctx, "Target.createTarget", map[string]interface{}{"url": "about:blank"})
	if err != nil {
		return "", fmt.Errorf("create page target: %w", err)
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.TargetID == "" {
		return "", fmt.Errorf("create page target: no target id in %s", string(raw))
	}
	return created.TargetID, nil
}
