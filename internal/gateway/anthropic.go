package gateway

import "net/http"

// AnthropicHandler exposes /v1/messages and /messages through the shared
// gateway implementation while preserving Anthropic's error envelope.
type AnthropicHandler struct{ *Handler }

func NewAnthropicHandler(cfg Config) *AnthropicHandler { return &AnthropicHandler{Handler: New(cfg)} }

func (h *AnthropicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Handler.ServeHTTP(w, r)
}
