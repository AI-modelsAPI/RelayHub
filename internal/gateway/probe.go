package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"relayhub/internal/router"
	"relayhub/internal/verify"
)

// Prober sends authenticity canaries (AUDIT §5 B1) through the production
// upstream path — the credential selection, channel egress and identity
// headers client traffic uses — so the transport does not set a probe apart
// from a user request. Each run asks the model to repeat a random code and,
// for models declared tool-capable, to make a forced tool call. The raw
// observation goes to verify.Registry.RecordProbe for scoring.
//
// Probes are deliberately invisible to health, usage and the passive score:
// a relay rejecting a synthetic request must not trip the breaker for real
// traffic.
type Prober struct {
	Upstream Upstream
	// Timeout bounds each probe request (default 30s).
	Timeout time.Duration
	// Code returns the canary code (tests pin it); default: four random
	// words from probeWords.
	Code func() string
	Now  func() time.Time
}

const (
	probeToolName = "record_code"
	probeMaxBody  = 1 << 20
	// A code echo needs ~10 tokens; the headroom absorbs chatty models.
	probeCanaryTokens = 64
	probeToolTokens   = 256
	// OpenAI reasoning models spend completion tokens thinking before the
	// answer and reject max_tokens outright.
	probeReasoningTokens = 2048
)

// LivenessResult is the outcome of one cheap liveness request.
type LivenessResult struct {
	OK      bool
	Status  int
	Latency time.Duration
	Error   string
}

// ProbeLiveness asks a channel for a single token. Not implemented yet.
func (p Prober) ProbeLiveness(ctx context.Context, d router.Decision) LivenessResult {
	return LivenessResult{}
}

// probeWords are common English words that tokenize as single tokens in the
// major tokenizers, so every code has the same prompt size (the prompt-token
// fingerprint depends on it) while 64^4 codes stay unguessable.
var probeWords = []string{
	"apple", "river", "stone", "cloud", "green", "table", "window", "garden",
	"silver", "orange", "forest", "candle", "bridge", "pencil", "rocket", "winter",
	"summer", "island", "planet", "copper", "harbor", "lemon", "tiger", "eagle",
	"pepper", "coffee", "basket", "button", "castle", "dragon", "engine", "mirror",
	"pillow", "carpet", "jacket", "ticket", "bottle", "hammer", "ladder", "magnet",
	"needle", "pocket", "rabbit", "spider", "thunder", "valley", "wallet", "yellow",
	"anchor", "butter", "circle", "desert", "finger", "guitar", "honey", "jungle",
	"market", "number", "ocean", "parrot", "purple", "silk", "tower", "violin",
}

func probeCode() string {
	words := make([]string, 4)
	for i := range words {
		words[i] = probeWords[rand.IntN(len(probeWords))]
	}
	return strings.Join(words, " ")
}

func probeCanaryPrompt(code string) string {
	return "Reply with exactly the following words and nothing else: " + code
}

// Probe runs one probe against the binding in d. It never returns an error:
// failures to reach or understand the upstream are part of the outcome.
func (p Prober) Probe(ctx context.Context, d router.Decision) verify.ProbeOutcome {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	upstreamModel := d.ProviderModel.UpstreamModelName
	if upstreamModel == "" {
		upstreamModel = d.Model.ID
	}
	proto := probeProtocol(d, upstreamModel)
	o := verify.ProbeOutcome{
		ChannelID:     d.Channel.ID,
		ModelID:       d.Model.ID,
		UpstreamModel: upstreamModel,
		Protocol:      proto,
		At:            now(),
	}
	if proto == "" {
		o.CanaryError = "probes do not support upstream protocol " + strconv.Quote(d.ProviderModel.Protocol)
		return o
	}
	code := probeCode()
	if p.Code != nil {
		code = p.Code()
	}
	reasoning := d.Model.ReasoningSupport

	start := time.Now()
	status, body, err := p.send(ctx, d, proto, probeCanaryBody(proto, upstreamModel, code, reasoning))
	o.Latency = time.Since(start)
	o.CanaryStatus = status
	reply, err := probeParse(proto, status, body, err)
	if err != nil {
		o.CanaryError = err.Error()
		return o
	}
	o.CanaryText = probeClip(reply.Text, 200)
	o.CanaryEchoed = probeHasCode(reply.Text, code)
	o.CanaryTruncated = reply.Truncated
	o.ReportedModel = reply.Model
	o.PromptTokens = reply.PromptTokens

	if !d.Model.ToolCallSupport {
		return o
	}
	o.ToolsChecked = true
	status, body, err = p.send(ctx, d, proto, probeToolBody(proto, upstreamModel, code, reasoning))
	o.ToolsStatus = status
	reply, err = probeParse(proto, status, body, err)
	if err != nil {
		o.ToolsError = err.Error()
		return o
	}
	for _, name := range reply.ToolCalls {
		if name == probeToolName {
			o.ToolsCalled = true
		}
	}
	o.ToolsTruncated = reply.Truncated
	return o
}

// probeProtocol picks the wire protocol for a binding. Without an explicit
// protocol the gateway forwards whatever the client speaks; Claude models
// are then probed over Messages, everything else over Chat Completions.
func probeProtocol(d router.Decision, upstreamModel string) string {
	switch normalizeProtocol(strings.ToLower(strings.TrimSpace(d.ProviderModel.Protocol))) {
	case "openai-chat":
		return "openai-chat"
	case "anthropic-messages":
		return "anthropic-messages"
	case "":
		if strings.Contains(strings.ToLower(upstreamModel), "claude") {
			return "anthropic-messages"
		}
		return "openai-chat"
	}
	return ""
}

func (p Prober) send(ctx context.Context, d router.Decision, proto string, body []byte) (int, []byte, error) {
	if p.Upstream == nil {
		return 0, nil, errors.New("no upstream configured")
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	path := "/v1/chat/completions"
	if proto == "anthropic-messages" {
		path = "/v1/messages"
	}
	resp, err := p.Upstream.Do(ctx, Request{Protocol: proto, Path: path, Headers: probeHeaders(proto, d), Body: body, Decision: d})
	if err != nil {
		return 0, nil, err
	}
	if resp.Body == nil {
		return resp.StatusCode, nil, nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, probeMaxBody))
	return resp.StatusCode, data, err
}

// probeHeaders mirrors what the gateway sends for client traffic, minus
// anything client-specific: the channel's identity profile (User-Agent,
// custom headers) is applied on top.
func probeHeaders(proto string, d router.Decision) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	if proto == "anthropic-messages" {
		h.Set("anthropic-version", "2023-06-01")
	}
	applyChannelHeaders(h, d.Channel.CustomHeaders)
	return h
}

func probeSetTokenLimit(body map[string]any, proto string, n int, reasoning bool) {
	if proto == "openai-chat" && reasoning {
		body["max_completion_tokens"] = probeReasoningTokens
		return
	}
	body["max_tokens"] = n
}

func probeCanaryBody(proto, model, code string, reasoning bool) []byte {
	body := map[string]any{
		"model":    model,
		"stream":   false,
		"messages": []map[string]any{{"role": "user", "content": probeCanaryPrompt(code)}},
	}
	probeSetTokenLimit(body, proto, probeCanaryTokens, reasoning)
	out, _ := json.Marshal(body)
	return out
}

func probeToolBody(proto, model, code string, reasoning bool) []byte {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code": map[string]any{"type": "string", "description": "The code to record."},
		},
		"required": []string{"code"},
	}
	const description = "Records a verification code."
	body := map[string]any{
		"model":    model,
		"stream":   false,
		"messages": []map[string]any{{"role": "user", "content": "Call the " + probeToolName + " tool with code set to: " + code}},
	}
	if proto == "anthropic-messages" {
		body["tools"] = []map[string]any{{"name": probeToolName, "description": description, "input_schema": schema}}
		body["tool_choice"] = map[string]any{"type": "tool", "name": probeToolName}
	} else {
		body["tools"] = []map[string]any{{"type": "function", "function": map[string]any{"name": probeToolName, "description": description, "parameters": schema}}}
		body["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": probeToolName}}
	}
	probeSetTokenLimit(body, proto, probeToolTokens, reasoning)
	out, _ := json.Marshal(body)
	return out
}

type probeReply struct {
	Text         string
	Model        string
	PromptTokens int
	Truncated    bool
	ToolCalls    []string
}

// probeParse turns one upstream exchange into a reply, or the reason there is
// none: a transport error, a non-2xx status, a body that is not a completion.
func probeParse(proto string, status int, body []byte, err error) (probeReply, error) {
	if err != nil {
		return probeReply{}, err
	}
	if status < 200 || status > 299 {
		return probeReply{}, fmt.Errorf("HTTP %d: %s", status, probeErrorText(body))
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return probeReply{}, fmt.Errorf("response is not a JSON completion: %s", probeClip(string(trimmed), 120))
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(trimmed, &envelope) == nil {
		switch e := string(bytes.TrimSpace(envelope.Error)); e {
		case "", "null", `""`, "{}", "false":
		default:
			return probeReply{}, fmt.Errorf("error object in a %d response: %s", status, probeErrorText(trimmed))
		}
	}
	if proto == "anthropic-messages" {
		return parseAnthropicProbe(trimmed)
	}
	return parseOpenAIProbe(trimmed)
}

func parseAnthropicProbe(body []byte) (probeReply, error) {
	var m struct {
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return probeReply{}, fmt.Errorf("malformed JSON completion: %v", err)
	}
	var out probeReply
	var text strings.Builder
	for _, c := range m.Content {
		switch c.Type {
		case "text":
			text.WriteString(c.Text)
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, c.Name)
		}
	}
	out.Text = text.String()
	out.Model = m.Model
	// Cached prompt tokens are reported separately by Messages.
	out.PromptTokens = m.Usage.InputTokens + m.Usage.CacheCreationInputTokens + m.Usage.CacheReadInputTokens
	out.Truncated = m.StopReason == "max_tokens"
	return out, nil
}

func parseOpenAIProbe(body []byte) (probeReply, error) {
	var m struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   json.RawMessage `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return probeReply{}, fmt.Errorf("malformed JSON completion: %v", err)
	}
	if len(m.Choices) == 0 {
		return probeReply{}, errors.New("completion has no choices")
	}
	c := m.Choices[0]
	out := probeReply{
		Text:         probeContentText(c.Message.Content),
		Model:        m.Model,
		PromptTokens: m.Usage.PromptTokens,
		Truncated:    c.FinishReason == "length",
	}
	for _, tc := range c.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, tc.Function.Name)
	}
	return out, nil
}

// probeContentText reads Chat Completions content: a string, null, or an
// array of typed parts.
func probeContentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		if part.Type == "" || part.Type == "text" || part.Type == "output_text" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

// probeHasCode matches the code as a word sequence, ignoring case and
// punctuation ("Apple, river, stone, cloud." counts).
func probeHasCode(text, code string) bool {
	norm := func(s string) string {
		return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}), " ")
	}
	want := norm(code)
	return want != "" && strings.Contains(" "+norm(text)+" ", " "+want+" ")
}

// probeErrorText extracts an upstream error message for the operator.
func probeErrorText(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil {
		switch {
		case e.Error.Message != "":
			return probeClip(e.Error.Message, 200)
		case e.Message != "":
			return probeClip(e.Message, 200)
		case e.Error.Type != "":
			return e.Error.Type
		}
	}
	if s := probeClip(string(bytes.TrimSpace(body)), 120); s != "" {
		return s
	}
	return "empty body"
}

func probeClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
