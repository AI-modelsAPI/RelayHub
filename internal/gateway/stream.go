package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"relayhub/internal/router"
)

// errStreamErrorEvent is returned when the upstream itself reported an error
// inside an already committed stream. The bytes reached the client verbatim;
// the gateway uses the error to penalise the attempt (AUDIT §5 B5).
var errStreamErrorEvent = errors.New("upstream reported an error mid-stream")

type streamMeta struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	TTFTMS           int
	FinishReason     string
	UpstreamModel    string
	// ErrorEvent holds the upstream's own error message when the stream
	// carried an error payload or event.
	ErrorEvent string
	// Truncated is set when the upstream stream ended without any explicit
	// finish/stop reason, so telemetry can flag "EOF instead of completion"
	// instead of disguising truncation as a normal end_turn (AUDIT RH-12).
	Truncated bool
}

func writeSSE(w http.ResponseWriter, body io.Reader, clientProto, upProto, logicalModel string) (streamMeta, error) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	clientN := normalizeProtocol(clientProto)
	upN := normalizeProtocol(upProto)
	if upN == "" {
		upN = clientN
	}

	start := time.Now()
	var meta streamMeta
	first := true

	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	emit := func(chunk string) error {
		if _, err := io.WriteString(w, chunk); err != nil {
			return err
		}
		flush()
		return nil
	}
	markFirst := func() {
		if first {
			meta.TTFTMS = int(time.Since(start).Milliseconds())
			first = false
		}
	}

	if clientN == upN || (clientN != "anthropic-messages" && clientN != "openai-chat") || (upN != "anthropic-messages" && upN != "openai-chat") {
		return pipeSSE(body, emit, markFirst, &meta)
	}
	if clientN == "anthropic-messages" && upN == "openai-chat" {
		return convertOpenAIStreamToAnthropic(body, logicalModel, emit, markFirst, &meta)
	}
	if clientN == "openai-chat" && upN == "anthropic-messages" {
		return convertAnthropicStreamToOpenAI(body, logicalModel, emit, markFirst, &meta)
	}
	return pipeSSE(body, emit, markFirst, &meta)
}

func pipeSSE(body io.Reader, emit func(string) error, markFirst func(), meta *streamMeta) (streamMeta, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.HasPrefix(line, []byte("data: ")) {
			payload := bytes.TrimSpace(line[6:])
			if len(payload) > 0 && !bytes.Equal(payload, []byte("[DONE]")) {
				if !json.Valid(payload) {
					return *meta, errors.New("malformed upstream SSE event")
				}
				markFirst()
				absorbUsage(payload, meta)
			}
		}
		if err := emit(string(line) + "\n"); err != nil {
			return *meta, err
		}
	}
	return *meta, scanner.Err()
}

func absorbUsage(payload []byte, meta *streamMeta) {
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil {
		return
	}
	if m, ok := obj["model"].(string); ok && m != "" {
		meta.UpstreamModel = m
	}
	// Anthropic carries the bulk of its usage on message_start.message.usage;
	// parsing only the top-level usage event loses input/cache tokens entirely
	// (AUDIT RH-17).
	if msg, ok := obj["message"].(map[string]any); ok {
		if m, ok := msg["model"].(string); ok && m != "" {
			meta.UpstreamModel = m
		}
		if u, ok := msg["usage"].(map[string]any); ok {
			if v, ok := asInt(u["input_tokens"]); ok {
				meta.InputTokens = v
			}
			if v, ok := asInt(u["output_tokens"]); ok {
				meta.OutputTokens = v
			}
			if v, ok := asInt(u["cache_read_input_tokens"]); ok {
				meta.CacheReadTokens = v
			}
			if v, ok := asInt(u["cache_creation_input_tokens"]); ok {
				meta.CacheWriteTokens = v
			}
		}
	}
	if u, ok := obj["usage"].(map[string]any); ok {
		if v, ok := asInt(u["prompt_tokens"]); ok {
			meta.InputTokens = v
		}
		if v, ok := asInt(u["completion_tokens"]); ok {
			meta.OutputTokens = v
		}
		if v, ok := asInt(u["input_tokens"]); ok {
			meta.InputTokens = v
		}
		if v, ok := asInt(u["output_tokens"]); ok {
			meta.OutputTokens = v
		}
		if v, ok := asInt(u["cache_read_input_tokens"]); ok {
			meta.CacheReadTokens = v
		}
		if v, ok := asInt(u["cache_creation_input_tokens"]); ok {
			meta.CacheWriteTokens = v
		}
		if details, ok := u["prompt_tokens_details"].(map[string]any); ok {
			if v, ok := asInt(details["cached_tokens"]); ok {
				meta.CacheReadTokens = v
			}
		}
	}
	if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
		if c0, ok := choices[0].(map[string]any); ok {
			if fr, ok := c0["finish_reason"].(string); ok && fr != "" {
				meta.FinishReason = fr
			}
		}
	}
	if fr, ok := obj["stop_reason"].(string); ok && fr != "" {
		meta.FinishReason = fr
	}
	if delta, ok := obj["delta"].(map[string]any); ok {
		if fr, ok := delta["stop_reason"].(string); ok && fr != "" {
			meta.FinishReason = fr
		}
	}
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func convertOpenAIStreamToAnthropic(body io.Reader, model string, emit func(string) error, markFirst func(), meta *streamMeta) (streamMeta, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	started := false
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	index := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		payload := bytes.TrimSpace(line[6:])
		if bytes.Equal(payload, []byte("[DONE]")) {
			break
		}
		if !json.Valid(payload) {
			return *meta, errors.New("malformed upstream SSE event")
		}
		markFirst()
		absorbUsage(payload, meta)
		var chunk map[string]any
		if json.Unmarshal(payload, &chunk) != nil {
			continue
		}
		if id, ok := chunk["id"].(string); ok && id != "" {
			msgID = id
		}
		text, finish := openaiDelta(chunk)
		if !started {
			started = true
			msg := map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id": msgID, "type": "message", "role": "assistant", "model": model,
					"content": []any{}, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
				},
			}
			if err := emitAnthropic(emit, "message_start", msg); err != nil {
				return *meta, err
			}
			startBlk := map[string]any{
				"type": "content_block_start", "index": index,
				"content_block": map[string]any{"type": "text", "text": ""},
			}
			if err := emitAnthropic(emit, "content_block_start", startBlk); err != nil {
				return *meta, err
			}
		}
		if text != "" {
			delta := map[string]any{
				"type": "content_block_delta", "index": index,
				"delta": map[string]any{"type": "text_delta", "text": text},
			}
			if err := emitAnthropic(emit, "content_block_delta", delta); err != nil {
				return *meta, err
			}
		}
		if finish != "" {
			meta.FinishReason = finish
			stop := "end_turn"
			switch finish {
			case "length":
				stop = "max_tokens"
			case "tool_calls", "function_call":
				stop = "tool_use"
			}
			if err := emitAnthropic(emit, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index}); err != nil {
				return *meta, err
			}
			md := map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{"stop_reason": stop},
				"usage": map[string]any{"output_tokens": meta.OutputTokens},
			}
			if err := emitAnthropic(emit, "message_delta", md); err != nil {
				return *meta, err
			}
			if err := emitAnthropic(emit, "message_stop", map[string]any{"type": "message_stop"}); err != nil {
				return *meta, err
			}
		}
	}
	if started && meta.FinishReason == "" {
		// The client still needs protocol termination events, but the missing
		// finish reason is flagged so it is not disguised as a normal
		// completion in telemetry (AUDIT RH-12).
		meta.Truncated = true
		_ = emitAnthropic(emit, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
		_ = emitAnthropic(emit, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}})
		_ = emitAnthropic(emit, "message_stop", map[string]any{"type": "message_stop"})
	}
	return *meta, scanner.Err()
}

func openaiDelta(chunk map[string]any) (text, finish string) {
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return
	}
	c0, _ := choices[0].(map[string]any)
	if c0 == nil {
		return
	}
	if fr, ok := c0["finish_reason"].(string); ok {
		finish = fr
	}
	if d, ok := c0["delta"].(map[string]any); ok {
		if s, ok := d["content"].(string); ok {
			text = s
		}
	}
	return
}

func convertAnthropicStreamToOpenAI(body io.Reader, model string, emit func(string) error, markFirst func(), meta *streamMeta) (streamMeta, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	var eventName string
	sawEvent := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if !json.Valid([]byte(payload)) {
			return *meta, errors.New("malformed upstream SSE event")
		}
		markFirst()
		sawEvent = true
		absorbUsage([]byte(payload), meta)
		var obj map[string]any
		if json.Unmarshal([]byte(payload), &obj) != nil {
			continue
		}
		typ, _ := obj["type"].(string)
		if typ == "" {
			typ = eventName
		}
		switch typ {
		case "message_start":
			if msg, ok := obj["message"].(map[string]any); ok {
				if mid, ok := msg["id"].(string); ok && mid != "" {
					id = mid
				}
				if um, ok := msg["model"].(string); ok {
					meta.UpstreamModel = um
				}
			}
		case "content_block_delta":
			text := ""
			if d, ok := obj["delta"].(map[string]any); ok {
				if s, ok := d["text"].(string); ok {
					text = s
				}
			}
			chunk := map[string]any{
				"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": nil}},
			}
			b, _ := json.Marshal(chunk)
			if err := emit("data: " + string(b) + "\n\n"); err != nil {
				return *meta, err
			}
		case "message_delta":
			fr := "stop"
			if d, ok := obj["delta"].(map[string]any); ok {
				if sr, ok := d["stop_reason"].(string); ok {
					switch sr {
					case "max_tokens":
						fr = "length"
					case "tool_use":
						fr = "tool_calls"
					default:
						fr = "stop"
					}
					meta.FinishReason = fr
				}
			}
			chunk := map[string]any{
				"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": fr}},
			}
			b, _ := json.Marshal(chunk)
			if err := emit("data: " + string(b) + "\n\n"); err != nil {
				return *meta, err
			}
		case "message_stop":
			if err := emit("data: [DONE]\n\n"); err != nil {
				return *meta, err
			}
		}
		eventName = ""
	}
	if sawEvent && meta.FinishReason == "" {
		// Anthropic upstream ended without message_delta/stop_reason — truncated
		// rather than completed (AUDIT RH-12).
		meta.Truncated = true
	}
	return *meta, scanner.Err()
}

func emitAnthropic(emit func(string) error, event string, obj any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	return emit("event: " + event + "\ndata: " + string(b) + "\n\n")
}

func applyStreamMeta(rec *streamMeta, decision router.Decision) {
	if rec.UpstreamModel == "" {
		rec.UpstreamModel = decision.ProviderModel.UpstreamModelName
	}
}
