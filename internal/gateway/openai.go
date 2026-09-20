package gateway

import "net/http"

// OpenAIHandler exposes the OpenAI-compatible endpoints through the shared
// gateway implementation.
type OpenAIHandler struct{ *Handler }

func NewOpenAIHandler(cfg Config) *OpenAIHandler { return &OpenAIHandler{Handler: New(cfg)} }

func (h *OpenAIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Handler.ServeHTTP(w, r)
}
