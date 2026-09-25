package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Pre-commit window (AUDIT 2026-09-24 §5 B4).
//
// writeSSE sends "200 OK" before it has read a single event. A relay that
// answered a stream with 200 and then an error event, an HTML page or nothing
// at all therefore reached the client as a 200 stream, and failover (which
// only works before the first byte) never got a chance. The gateway now holds
// a 2xx stream back until the first event that carries output. Until then
// nothing has been written to the client, so the attempt can still fail over,
// and when no channel is left the client gets an HTTP error status.

// defaultStreamCommitWindow bounds how long a stream is held back waiting for
// its first meaningful event. When it elapses the stream is committed as is:
// a slow but healthy upstream must never be turned into an error.
const defaultStreamCommitWindow = 10 * time.Second

// maxPrecommitBytes bounds what is buffered before committing without a
// verdict.
const maxPrecommitBytes = 256 << 10

// precommitFailure describes an upstream that failed before producing any
// output. status is what the client gets when no other channel is left, with
// body (the upstream's own error JSON) or errType and message; class is the
// usage error class and reason the health diagnostic.
type precommitFailure struct {
	status  int
	class   string
	reason  string
	errType string
	message string
	body    []byte
}

// failover reports whether another channel may do better. Errors about the
// request itself (invalid, unknown model, too large) fail everywhere and do
// not count against the channel, the same way an HTTP 4xx is treated.
func (f *precommitFailure) failover() bool {
	switch f.status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return f.status < 400 || f.status >= 500
}

// vetResponse checks a 2xx response before anything is sent to the client.
// For a stream it replaces resp.Body with a body that replays what was
// consumed.
func (h *Handler) vetResponse(ctx context.Context, resp *Response, stream bool) (*precommitFailure, time.Duration) {
	// A relay answering 200 with an HTML page (Cloudflare challenge, login or
	// "insufficient balance" page) is a failure, not a completion to relay
	// (AUDIT 2026-09-24 F9).
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return htmlFailure(), 0
	}
	if !stream || resp.Body == nil {
		return nil, 0
	}
	window := h.cfg.StreamCommitWindow
	if window <= 0 {
		window = defaultStreamCommitWindow
	}
	body, failure, waited := probeStream(ctx, resp.Body, window)
	resp.Body = body
	return failure, waited
}

// writePrecommitFailure relays the upstream's own error when the client speaks
// the upstream protocol, otherwise a gateway error that keeps its type and
// message.
func writePrecommitFailure(w http.ResponseWriter, r *http.Request, f *precommitFailure, sameProtocol bool) {
	if !sameProtocol || len(f.body) == 0 {
		writeGatewayError(w, r, f.status, f.errType, f.message)
		return
	}
	w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.status)
	_, _ = w.Write(f.body)
}

type readResult struct {
	data []byte
	err  error
}

// readAsync performs one Read in the background so the probe can stop waiting
// when the window closes. The channel is buffered: an abandoned read never
// blocks its goroutine.
func readAsync(body io.Reader) chan readResult {
	ch := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 32<<10)
		n, err := body.Read(buf)
		ch <- readResult{data: buf[:n], err: err}
	}()
	return ch
}

// probeStream reads the start of a 2xx event stream until it can tell output
// from failure, the window elapses, ctx ends or maxPrecommitBytes are
// buffered. The returned body replays everything consumed, then the rest of
// the stream. A non-nil failure means the upstream produced no output.
func probeStream(ctx context.Context, body io.ReadCloser, window time.Duration) (io.ReadCloser, *precommitFailure, time.Duration) {
	start := time.Now()
	timer := time.NewTimer(window)
	defer timer.Stop()
	var head []byte
	for {
		pending := readAsync(body)
		select {
		case res := <-pending:
			head = append(head, res.data...)
			if ctx.Err() != nil {
				// The client left or the request timed out: no verdict on
				// the upstream either way.
				return &replayBody{head: head, err: res.err, rc: body}, nil, time.Since(start)
			}
			done := res.err != nil
			commit, failure := classifyStreamHead(head, done, res.err)
			if commit || failure != nil || done || len(head) >= maxPrecommitBytes {
				return &replayBody{head: head, err: res.err, rc: body}, failure, time.Since(start)
			}
		case <-timer.C:
			return &replayBody{head: head, pending: pending, rc: body}, nil, time.Since(start)
		case <-ctx.Done():
			return &replayBody{head: head, pending: pending, rc: body}, nil, time.Since(start)
		}
	}
}

// replayBody returns the bytes probeStream consumed, then the result of the
// read it left in flight (if any), then the rest of the upstream body.
type replayBody struct {
	head    []byte
	pending chan readResult
	err     error
	rc      io.ReadCloser
}

func (b *replayBody) Read(p []byte) (int, error) {
	if len(b.head) == 0 && b.pending != nil {
		res := <-b.pending
		b.pending = nil
		b.head, b.err = res.data, res.err
	}
	if len(b.head) > 0 {
		n := copy(p, b.head)
		b.head = b.head[n:]
		return n, nil
	}
	if b.err != nil {
		return 0, b.err
	}
	return b.rc.Read(p)
}

func (b *replayBody) Close() error { return b.rc.Close() }

// classifyStreamHead inspects the complete lines received so far. It reports
// commit once an event carries output (or has a shape the gateway does not
// know, which is left to the normal stream path), or a failure when the
// upstream clearly failed; neither means keep waiting. done reports that the
// body ended with readErr.
func classifyStreamHead(head []byte, done bool, readErr error) (bool, *precommitFailure) {
	text := bytes.TrimLeft(head, " \t\r\n\ufeff")
	if len(text) > 0 && text[0] == '<' {
		return false, htmlFailure()
	}
	if len(text) > 0 && (text[0] == '{' || text[0] == '[') {
		// Not an event stream at all: a plain JSON body, typically an error
		// object sent with status 200.
		if !done && len(head) < maxPrecommitBytes {
			return false, nil
		}
		return false, jsonBodyFailure(text)
	}
	event := ""
	rest := text
	for len(rest) > 0 {
		line := rest
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else if done {
			rest = nil
		} else {
			break // wait for the rest of the line
		}
		line = bytes.TrimRight(line, "\r")
		switch {
		case len(line) == 0:
			event = ""
		case bytes.HasPrefix(line, []byte("event:")):
			event = string(bytes.TrimSpace(line[len("event:"):]))
		case bytes.HasPrefix(line, []byte("data:")):
			commit, failure := classifyEvent(event, bytes.TrimSpace(line[len("data:"):]))
			if commit || failure != nil {
				return commit, failure
			}
		}
	}
	if done {
		return false, emptyStreamFailure(readErr)
	}
	return false, nil
}

// preambleEvents open a stream without carrying output yet.
var preambleEvents = map[string]bool{"message_start": true, "ping": true, "response.created": true, "response.in_progress": true, "response.queued": true}

// classifyEvent inspects one SSE data payload received before commit.
func classifyEvent(event string, payload []byte) (bool, *precommitFailure) {
	if len(payload) == 0 {
		return false, nil
	}
	if string(payload) == "[DONE]" {
		return false, emptyStreamFailure(nil)
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		if json.Valid(payload) {
			return true, nil // valid JSON of another shape: writeSSE decides
		}
		return false, protocolFailure("malformed SSE event", "malformed upstream SSE event")
	}
	typ, _ := obj["type"].(string)
	if event == "error" || typ == "error" || typ == "response.failed" || hasError(obj["error"]) {
		return false, upstreamErrorFailure(payload, obj)
	}
	if preambleEvents[typ] {
		return false, nil
	}
	choices, ok := obj["choices"].([]any)
	if typ != "" || !ok {
		return true, nil
	}
	// OpenAI chat chunk: the opening role-only delta is not output yet; a
	// usage-only chunk (stream_options.include_usage) ends the stream.
	return chunkHasOutput(choices) || (len(choices) == 0 && obj["usage"] != nil), nil
}

func hasError(v any) bool {
	switch e := v.(type) {
	case string:
		return e != ""
	case map[string]any:
		return len(e) > 0
	case bool:
		return e
	}
	return false
}

// chunkHasOutput reports whether an OpenAI chat chunk carries anything beyond
// the opening role-only delta.
func chunkHasOutput(choices []any) bool {
	for _, c := range choices {
		choice, _ := c.(map[string]any)
		if reason, _ := choice["finish_reason"].(string); reason != "" {
			return true
		}
		delta, _ := choice["delta"].(map[string]any)
		for _, key := range []string{"content", "reasoning_content", "reasoning", "refusal"} {
			if s, _ := delta[key].(string); s != "" {
				return true
			}
		}
		if delta["tool_calls"] != nil || delta["function_call"] != nil {
			return true
		}
	}
	return false
}

func htmlFailure() *precommitFailure {
	return protocolFailure("HTML page instead of an API response", "upstream returned an HTML page instead of an API response")
}

func protocolFailure(reason, message string) *precommitFailure {
	return &precommitFailure{status: http.StatusBadGateway, class: "provider_protocol", reason: "precommit: " + reason, errType: "provider_protocol", message: message}
}

func emptyStreamFailure(readErr error) *precommitFailure {
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return &precommitFailure{status: http.StatusBadGateway, class: "network_error", reason: "precommit: stream failed before any output: " + readErr.Error(), errType: "network_error", message: "upstream stream failed before sending any output"}
	}
	return protocolFailure("stream ended before any output", "upstream stream ended without sending any output")
}

// jsonBodyFailure handles a streaming request answered with a plain JSON body.
func jsonBodyFailure(body []byte) *precommitFailure {
	var obj map[string]any
	if json.Unmarshal(body, &obj) == nil && hasError(obj["error"]) {
		return upstreamErrorFailure(bytes.TrimSpace(body), obj)
	}
	return protocolFailure("JSON body instead of an event stream", "upstream answered a streaming request without an event stream")
}

// upstreamErrorFailure keeps the upstream's error type and message and maps
// them to the status the client would have seen without the 200 wrapper.
func upstreamErrorFailure(payload []byte, obj map[string]any) *precommitFailure {
	errObj, _ := obj["error"].(map[string]any)
	if errObj == nil {
		if resp, ok := obj["response"].(map[string]any); ok {
			errObj, _ = resp["error"].(map[string]any) // response.failed
		}
	}
	if errObj == nil {
		errObj = obj // OpenAI Responses "error" event
	}
	errType := firstString(errObj, "type", "code")
	if errType == "" || errType == "error" {
		errType = firstString(errObj, "code")
	}
	if errType == "" {
		errType = "upstream_error"
	}
	message := firstString(errObj, "message")
	if s, ok := obj["error"].(string); ok && message == "" {
		message = s
	}
	if message == "" {
		message = "upstream returned an error before sending any output"
	}
	status := errorStatus(errType, errObj)
	f := &precommitFailure{status: status, class: classifyUpstreamStatus(status), reason: "precommit: upstream error " + errType + ": " + truncateRunes(message, 200), errType: errType, message: message}
	if _, ok := obj["error"]; ok {
		f.body = payload
	}
	return f
}

// errorStatus maps an upstream error to an HTTP status: a numeric code in the
// error wins, otherwise its type and string code decide (new-api puts the
// useful part in code, e.g. type new_api_error, code insufficient_user_quota).
func errorStatus(errType string, errObj map[string]any) int {
	for _, key := range []string{"status", "status_code", "code"} {
		if f, ok := errObj[key].(float64); ok && f >= 400 && f < 600 {
			return int(f)
		}
	}
	t := strings.ToLower(errType + " " + firstString(errObj, "code"))
	switch {
	case strings.Contains(t, "overloaded"):
		return 529
	case strings.Contains(t, "rate_limit"), strings.Contains(t, "quota"):
		return http.StatusTooManyRequests
	case strings.Contains(t, "authentication"), strings.Contains(t, "api_key"):
		return http.StatusUnauthorized
	case strings.Contains(t, "permission"):
		return http.StatusForbidden
	case strings.Contains(t, "not_found"):
		return http.StatusNotFound
	case strings.Contains(t, "too_large"):
		return http.StatusRequestEntityTooLarge
	case strings.Contains(t, "invalid_request"), strings.Contains(t, "context_length"):
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

func firstString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := obj[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
