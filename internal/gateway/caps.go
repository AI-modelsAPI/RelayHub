package gateway

import (
	"encoding/json"
	"strings"
)

func requestCapabilities(protocol string, body []byte) (tools, vision, reasoning bool) {
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil {
		return
	}
	if raw, ok := obj["tools"].([]any); ok && len(raw) > 0 {
		tools = true
	}
	if obj["tool_choice"] != nil {
		tools = true
	}
	if obj["thinking"] != nil || obj["reasoning"] != nil || obj["reasoning_effort"] != nil {
		reasoning = true
	}
	vision = contentHasImage(obj["messages"]) || contentHasImage(obj["system"]) || contentHasImage(obj["input"])
	_ = protocol
	return
}

func contentHasImage(v any) bool {
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			if contentHasImage(item) {
				return true
			}
		}
	case map[string]any:
		typ, _ := t["type"].(string)
		if typ == "image" || typ == "image_url" || typ == "input_image" {
			return true
		}
		if _, ok := t["image_url"]; ok {
			return true
		}
		if src, ok := t["source"].(map[string]any); ok {
			if st, _ := src["type"].(string); st == "base64" || st == "url" {
				return true
			}
		}
		for _, child := range t {
			if contentHasImage(child) {
				return true
			}
		}
	}
	return false
}

func requestSessionKey(rHeader string, body []byte) string {
	if s := strings.TrimSpace(rHeader); s != "" {
		return s
	}
	var obj struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if json.Unmarshal(body, &obj) == nil && strings.TrimSpace(obj.Metadata.UserID) != "" {
		return obj.Metadata.UserID
	}
	return ""
}
