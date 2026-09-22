package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"relayhub/internal/router"
	"relayhub/internal/usage"
)

func requestModel(protocol string, body []byte) (string, error) {
	var input struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		return "", fmt.Errorf("invalid JSON request")
	}
	if strings.TrimSpace(input.Model) == "" {
		return "", fmt.Errorf("model is required")
	}
	return input.Model, nil
}

func requestStream(protocol string, body []byte) bool {
	var input struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &input)
	return input.Stream
}

func normalizeProtocol(p string) string {
	switch p {
	case "openai", "openai-chat":
		return "openai-chat"
	case "openai-responses":
		return "openai-responses"
	case "anthropic", "anthropic-messages":
		return "anthropic-messages"
	case "gemini":
		return "gemini"
	default:
		return p
	}
}

func transformRequest(protocol, endpoint string, body []byte, decision router.Decision) ([]byte, error) {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("invalid JSON request")
	}

	model := decision.ProviderModel.UpstreamModelName
	if model == "" {
		model = decision.Model.ID
	}
	obj["model"] = model

	reqProto := normalizeProtocol(protocol)
	upProto := normalizeProtocol(decision.ProviderModel.Protocol)
	if upProto == "" {
		upProto = reqProto
	}

	if reqProto != upProto {
		// The converters cannot carry these semantics losslessly; silently
		// dropping them previously made clients pay for degraded responses
		// (AUDIT RH-12). Refuse instead.
		if reason := unsupportedForConversion(obj, reqProto); reason != "" {
			return nil, fmt.Errorf("cross-protocol conversion (%s -> %s) %s", reqProto, upProto, reason)
		}
	}

	// Cross protocol conversion: Anthropic client -> OpenAI upstream
	if reqProto == "anthropic-messages" && upProto == "openai-chat" {
		return transformAnthropicToOpenAIRequest(obj, model)
	}

	// Cross protocol conversion: OpenAI client -> Anthropic upstream
	if reqProto == "openai-chat" && upProto == "anthropic-messages" {
		return transformOpenAIToAnthropicRequest(obj, model)
	}

	return json.Marshal(obj)
}

// unsupportedForConversion returns a human-readable reason when the request
// carries semantics the cross-protocol converters would silently drop, or ""
// when the conversion is safe (AUDIT RH-12).
func unsupportedForConversion(obj map[string]any, reqProto string) string {
	if msgs, ok := obj["messages"].([]any); ok {
		for _, m := range msgs {
			msg, _ := m.(map[string]any)
			if msg == nil {
				continue
			}
			content, _ := msg["content"].([]any)
			for _, c := range content {
				block, _ := c.(map[string]any)
				if block == nil {
					continue
				}
				switch block["type"] {
				case "image", "image_url", "input_image", "input_audio", "document", "thinking":
					return "does not support multimodal/thinking content blocks"
				}
			}
		}
	}
	if stream, _ := obj["stream"].(bool); stream {
		if tools, ok := obj["tools"].([]any); ok && len(tools) > 0 {
			return "does not support streamed tool calls"
		}
	}
	if reqProto == "openai-chat" {
		if choice, ok := obj["tool_choice"]; ok && choice != nil {
			if s, _ := choice.(string); s != "" && s != "auto" && s != "none" {
				return "does not preserve a forced tool_choice"
			}
			if _, isMap := choice.(map[string]any); isMap {
				return "does not preserve a forced tool_choice"
			}
		}
	}
	return ""
}

func transformAnthropicToOpenAIRequest(src map[string]any, model string) ([]byte, error) {
	dst := make(map[string]any)
	dst["model"] = model

	var openAIMsgs []map[string]any

	// 1. System prompt
	if sys, ok := src["system"]; ok && sys != nil {
		if sysStr, ok := sys.(string); ok && strings.TrimSpace(sysStr) != "" {
			openAIMsgs = append(openAIMsgs, map[string]any{
				"role":    "system",
				"content": sysStr,
			})
		} else if sysArr, ok := sys.([]any); ok {
			var sb strings.Builder
			for _, item := range sysArr {
				if m, ok := item.(map[string]any); ok {
					if txt, ok := m["text"].(string); ok {
						sb.WriteString(txt)
					}
				}
			}
			if sb.Len() > 0 {
				openAIMsgs = append(openAIMsgs, map[string]any{
					"role":    "system",
					"content": sb.String(),
				})
			}
		}
	}

	// 2. Messages
	if rawMsgs, ok := src["messages"].([]any); ok {
		for _, rawMsg := range rawMsgs {
			m, ok := rawMsg.(map[string]any)
			if !ok {
				continue
			}
			role, _ := m["role"].(string)

			switch c := m["content"].(type) {
			case string:
				openAIMsgs = append(openAIMsgs, map[string]any{
					"role":    role,
					"content": c,
				})
			case []any:
				var textParts []string
				var toolCalls []map[string]any

				for _, block := range c {
					b, ok := block.(map[string]any)
					if !ok {
						continue
					}
					bType, _ := b["type"].(string)
					switch bType {
					case "text":
						if txt, ok := b["text"].(string); ok {
							textParts = append(textParts, txt)
						}
					case "tool_use":
						tID, _ := b["id"].(string)
						tName, _ := b["name"].(string)
						tInput := b["input"]
						argBytes, _ := json.Marshal(tInput)
						toolCalls = append(toolCalls, map[string]any{
							"id":   tID,
							"type": "function",
							"function": map[string]any{
								"name":      tName,
								"arguments": string(argBytes),
							},
						})
					case "tool_result":
						tID, _ := b["tool_use_id"].(string)
						var resContent string
						switch rc := b["content"].(type) {
						case string:
							resContent = rc
						default:
							b, _ := json.Marshal(rc)
							resContent = string(b)
						}
						openAIMsgs = append(openAIMsgs, map[string]any{
							"role":         "tool",
							"tool_call_id": tID,
							"content":      resContent,
						})
					}
				}

				if len(textParts) > 0 || len(toolCalls) > 0 {
					msg := map[string]any{
						"role":    role,
						"content": strings.Join(textParts, "\n"),
					}
					if len(toolCalls) > 0 {
						msg["tool_calls"] = toolCalls
					}
					openAIMsgs = append(openAIMsgs, msg)
				}
			}
		}
	}
	dst["messages"] = openAIMsgs

	// 3. Parameters
	if maxTok, ok := src["max_tokens"]; ok {
		dst["max_tokens"] = maxTok
	}
	if temp, ok := src["temperature"]; ok {
		dst["temperature"] = temp
	}
	if topP, ok := src["top_p"]; ok {
		dst["top_p"] = topP
	}
	if stream, ok := src["stream"]; ok {
		dst["stream"] = stream
	}
	if stopSeq, ok := src["stop_sequences"]; ok {
		dst["stop"] = stopSeq
	}
	if cc, ok := src["cache_control"]; ok {
		dst["cache_control"] = cc
	}

	// 4. Tools & tool_choice
	if tools, ok := src["tools"].([]any); ok && len(tools) > 0 {
		var openAITools []map[string]any
		for _, t := range tools {
			toolMap, ok := t.(map[string]any)
			if !ok {
				continue
			}
			tName, _ := toolMap["name"].(string)
			tDesc, _ := toolMap["description"].(string)
			schema := toolMap["input_schema"]
			openAITools = append(openAITools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        tName,
					"description": tDesc,
					"parameters":  schema,
				},
			})
		}
		if len(openAITools) > 0 {
			dst["tools"] = openAITools
		}
	}
	if tc, ok := src["tool_choice"].(map[string]any); ok {
		tType, _ := tc["type"].(string)
		switch tType {
		case "auto":
			dst["tool_choice"] = "auto"
		case "any":
			dst["tool_choice"] = "required"
		case "tool":
			if name, ok := tc["name"].(string); ok {
				dst["tool_choice"] = map[string]any{
					"type": "function",
					"function": map[string]any{
						"name": name,
					},
				}
			}
		}
	}

	return json.Marshal(dst)
}

func transformOpenAIToAnthropicRequest(src map[string]any, model string) ([]byte, error) {
	dst := make(map[string]any)
	dst["model"] = model

	var anthropicMsgs []map[string]any
	var systemParts []string

	if rawMsgs, ok := src["messages"].([]any); ok {
		for _, rawMsg := range rawMsgs {
			m, ok := rawMsg.(map[string]any)
			if !ok {
				continue
			}
			role, _ := m["role"].(string)

			if role == "system" {
				if c, ok := m["content"].(string); ok {
					systemParts = append(systemParts, c)
				}
				continue
			}

			if role == "tool" {
				tID, _ := m["tool_call_id"].(string)
				cStr, _ := m["content"].(string)
				anthropicMsgs = append(anthropicMsgs, map[string]any{
					"role": "user",
					"content": []map[string]any{
						{
							"type":        "tool_result",
							"tool_use_id": tID,
							"content":     cStr,
						},
					},
				})
				continue
			}

			// User or assistant
			var contentBlocks []map[string]any
			if cStr, ok := m["content"].(string); ok && cStr != "" {
				contentBlocks = append(contentBlocks, map[string]any{
					"type": "text",
					"text": cStr,
				})
			}

			if tCalls, ok := m["tool_calls"].([]any); ok {
				for _, tc := range tCalls {
					callMap, ok := tc.(map[string]any)
					if !ok {
						continue
					}
					id, _ := callMap["id"].(string)
					fn, _ := callMap["function"].(map[string]any)
					name, _ := fn["name"].(string)
					argStr, _ := fn["arguments"].(string)
					var inputMap map[string]any
					_ = json.Unmarshal([]byte(argStr), &inputMap)
					contentBlocks = append(contentBlocks, map[string]any{
						"type":  "tool_use",
						"id":    id,
						"name":  name,
						"input": inputMap,
					})
				}
			}

			if len(contentBlocks) > 0 {
				anthropicMsgs = append(anthropicMsgs, map[string]any{
					"role":    role,
					"content": contentBlocks,
				})
			}
		}
	}

	if len(systemParts) > 0 {
		dst["system"] = strings.Join(systemParts, "\n\n")
	}
	dst["messages"] = anthropicMsgs

	// Anthropic requires max_tokens
	maxTokens := 4096
	if mt, ok := src["max_tokens"].(float64); ok && mt > 0 {
		maxTokens = int(mt)
	}
	dst["max_tokens"] = maxTokens

	if temp, ok := src["temperature"]; ok {
		dst["temperature"] = temp
	}
	if topP, ok := src["top_p"]; ok {
		dst["top_p"] = topP
	}
	if stream, ok := src["stream"]; ok {
		dst["stream"] = stream
	}
	if cc, ok := src["cache_control"]; ok {
		dst["cache_control"] = cc
	}
	if stop, ok := src["stop"]; ok {
		if stopStr, ok := stop.(string); ok {
			dst["stop_sequences"] = []string{stopStr}
		} else if stopArr, ok := stop.([]any); ok {
			dst["stop_sequences"] = stopArr
		}
	}

	// Tools
	if tools, ok := src["tools"].([]any); ok && len(tools) > 0 {
		var anthropicTools []map[string]any
		for _, t := range tools {
			toolMap, ok := t.(map[string]any)
			if !ok {
				continue
			}
			fn, _ := toolMap["function"].(map[string]any)
			if fn == nil {
				continue
			}
			name, _ := fn["name"].(string)
			desc, _ := fn["description"].(string)
			params := fn["parameters"]
			anthropicTools = append(anthropicTools, map[string]any{
				"name":         name,
				"description":  desc,
				"input_schema": params,
			})
		}
		if len(anthropicTools) > 0 {
			dst["tools"] = anthropicTools
		}
	}

	return json.Marshal(dst)
}

func transformResponse(protocol string, body []byte, decision router.Decision, now time.Time) ([]byte, error) {
	if !json.Valid(body) {
		return nil, errors.New("malformed upstream JSON response")
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, errors.New("malformed upstream JSON response")
	}
	if obj == nil {
		// A JSON "null" is valid but not a protocol response; passing it through
		// produced a literal null reply to the client.
		return nil, errors.New("empty upstream response")
	}
	if _, ok := obj["error"]; ok {
		return nil, errors.New("upstream returned an error response")
	}

	reqProto := normalizeProtocol(protocol)
	upProto := normalizeProtocol(decision.ProviderModel.Protocol)
	if upProto == "" {
		upProto = reqProto
	}

	// 1. Anthropic Client received response from OpenAI Upstream
	if reqProto == "anthropic-messages" && upProto == "openai-chat" {
		return transformOpenAIToAnthropicResponse(obj, decision)
	}

	// 2. OpenAI Client received response from Anthropic Upstream
	if reqProto == "openai-chat" && upProto == "anthropic-messages" {
		return transformAnthropicToOpenAIResponse(obj, decision)
	}

	// Same protocol passthrough: just map the model ID
	if _, ok := obj["model"]; ok {
		obj["model"] = decision.Model.ID
	}
	return json.Marshal(obj)
}

func transformOpenAIToAnthropicResponse(src map[string]any, decision router.Decision) ([]byte, error) {
	dst := make(map[string]any)
	dst["id"] = src["id"]
	dst["type"] = "message"
	dst["role"] = "assistant"
	dst["model"] = decision.Model.ID

	var content []map[string]any
	stopReason := "end_turn"

	if choices, ok := src["choices"].([]any); ok && len(choices) > 0 {
		choice0, _ := choices[0].(map[string]any)
		if choice0 != nil {
			msg, _ := choice0["message"].(map[string]any)
			if msg != nil {
				if text, ok := msg["content"].(string); ok && text != "" {
					content = append(content, map[string]any{
						"type": "text",
						"text": text,
					})
				}
				if toolCalls, ok := msg["tool_calls"].([]any); ok {
					for _, tc := range toolCalls {
						tcMap, _ := tc.(map[string]any)
						if tcMap == nil {
							continue
						}
						id, _ := tcMap["id"].(string)
						fn, _ := tcMap["function"].(map[string]any)
						if fn == nil {
							continue
						}
						name, _ := fn["name"].(string)
						argsStr, _ := fn["arguments"].(string)
						var inputMap map[string]any
						_ = json.Unmarshal([]byte(argsStr), &inputMap)
						content = append(content, map[string]any{
							"type":  "tool_use",
							"id":    id,
							"name":  name,
							"input": inputMap,
						})
					}
				}
			}

			if fr, ok := choice0["finish_reason"].(string); ok {
				switch fr {
				case "stop":
					stopReason = "end_turn"
				case "length":
					stopReason = "max_tokens"
				case "tool_calls", "function_call":
					stopReason = "tool_use"
				default:
					stopReason = fr
				}
			}
		}
	}

	dst["content"] = content
	dst["stop_reason"] = stopReason

	// Usage mapping
	usageMap := map[string]any{
		"input_tokens":  0,
		"output_tokens": 0,
	}
	if u, ok := src["usage"].(map[string]any); ok && u != nil {
		if pt, ok := u["prompt_tokens"].(float64); ok {
			usageMap["input_tokens"] = int(pt)
		}
		if ct, ok := u["completion_tokens"].(float64); ok {
			usageMap["output_tokens"] = int(ct)
		}
	}
	dst["usage"] = usageMap

	return json.Marshal(dst)
}

func transformAnthropicToOpenAIResponse(src map[string]any, decision router.Decision) ([]byte, error) {
	dst := make(map[string]any)
	id, _ := src["id"].(string)
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().Unix())
	}
	dst["id"] = id
	dst["object"] = "chat.completion"
	dst["created"] = time.Now().Unix()
	dst["model"] = decision.Model.ID

	var textParts []string
	var toolCalls []map[string]any

	if content, ok := src["content"].([]any); ok {
		for _, block := range content {
			b, ok := block.(map[string]any)
			if !ok {
				continue
			}
			bType, _ := b["type"].(string)
			switch bType {
			case "text":
				if txt, ok := b["text"].(string); ok {
					textParts = append(textParts, txt)
				}
			case "tool_use":
				tID, _ := b["id"].(string)
				tName, _ := b["name"].(string)
				inputData := b["input"]
				argBytes, _ := json.Marshal(inputData)
				toolCalls = append(toolCalls, map[string]any{
					"id":   tID,
					"type": "function",
					"function": map[string]any{
						"name":      tName,
						"arguments": string(argBytes),
					},
				})
			}
		}
	}

	msg := map[string]any{
		"role":    "assistant",
		"content": strings.Join(textParts, "\n"),
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}

	finishReason := "stop"
	if sr, ok := src["stop_reason"].(string); ok {
		switch sr {
		case "end_turn", "stop_sequence":
			finishReason = "stop"
		case "max_tokens":
			finishReason = "length"
		case "tool_use":
			finishReason = "tool_calls"
		default:
			finishReason = sr
		}
	}

	dst["choices"] = []map[string]any{
		{
			"index":         0,
			"message":       msg,
			"finish_reason": finishReason,
		},
	}

	// Usage
	promptTokens := 0
	compTokens := 0
	if u, ok := src["usage"].(map[string]any); ok && u != nil {
		if it, ok := u["input_tokens"].(float64); ok {
			promptTokens = int(it)
		}
		if ot, ok := u["output_tokens"].(float64); ok {
			compTokens = int(ot)
		}
	}
	dst["usage"] = map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": compTokens,
		"total_tokens":      promptTokens + compTokens,
	}

	return json.Marshal(dst)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeGatewayError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
	if strings.HasPrefix(r.URL.Path, "/v1/messages") || r.URL.Path == "/messages" {
		writeJSON(w, status, map[string]any{"type": "error", "error": map[string]any{"type": code, "message": message}})
		return
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": code, "code": code}})
}

type UpstreamResponse = Response

func writeUpstreamError(w http.ResponseWriter, r *http.Request, response Response) {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if json.Valid(body) && len(bytes.TrimSpace(body)) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
		return
	}
	writeGatewayError(w, r, upstreamStatus(response.StatusCode), classifyHTTPStatus(response.StatusCode), "upstream request failed")
}

func upstreamStatus(status int) int {
	if status >= 400 && status < 600 {
		return status
	}
	return http.StatusBadGateway
}

func classifyHTTPStatus(status int) string {
	switch status {
	case 401:
		return "authentication_error"
	case 403:
		return "authorization_error"
	case 429:
		return "rate_limited"
	case 408:
		return "timeout"
	case 500, 502, 503, 504:
		return "provider_unavailable"
	default:
		return "provider_protocol"
	}
}

func recordFor(r *http.Request, protocol string, decision router.Decision, body []byte, status int, now time.Time) (record usage.RequestRecord) {
	record.ID = fmt.Sprintf("rec-%d", now.UnixNano())
	record.RequestID = r.Header.Get("X-Request-ID")
	record.Protocol, record.ModelID = protocol, decision.Model.ID
	record.ProviderID, record.ChannelID = decision.ProviderModel.ProviderID, decision.Channel.ID
	record.StatusCode, record.CreatedAt = status, now.UTC()
	record.UpstreamModel = decision.ProviderModel.UpstreamModelName
	if status >= 400 {
		if status == 429 {
			record.ErrorClass = "rate_limited"
		} else if status >= 500 {
			record.ErrorClass = "upstream_error"
		} else {
			record.ErrorClass = "client_error"
		}
	}
	var obj struct {
		Model string `json:"model"`
		Usage struct {
			PromptTokens             int `json:"prompt_tokens"`
			CompletionTokens         int `json:"completion_tokens"`
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			PromptTokensDetails      struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
		Choices    []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &obj) == nil {
		record.InputTokens = obj.Usage.PromptTokens + obj.Usage.InputTokens
		record.OutputTokens = obj.Usage.CompletionTokens + obj.Usage.OutputTokens
		record.CacheReadTokens = obj.Usage.CacheReadInputTokens
		if obj.Usage.PromptTokensDetails.CachedTokens > 0 {
			record.CacheReadTokens = obj.Usage.PromptTokensDetails.CachedTokens
		}
		record.CacheWriteTokens = obj.Usage.CacheCreationInputTokens
		if obj.Model != "" && obj.Model != decision.Model.ID {
			// The transformed response echoes the logical model id; adopting it
			// as the upstream model name produced false "model mismatch" trust
			// penalties for every alias-mapped request (AUDIT RH-17). Only a
			// name that actually came from the upstream counts.
			record.UpstreamModel = obj.Model
		}
		if obj.StopReason != "" {
			record.FinishReason = obj.StopReason
		} else if len(obj.Choices) > 0 {
			record.FinishReason = obj.Choices[0].FinishReason
		}
	}
	return record
}
