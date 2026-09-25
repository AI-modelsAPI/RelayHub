package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The hold introduced by B4 costs a little latency before the first byte.
// DisableStreamCommit restores the pre-B4 behaviour for operators who prefer
// latency over failover, and the window itself is configurable.
func TestDisableStreamCommitForwardsTheFirstEvent(t *testing.T) {
	up := &channelUpstream{responses: []Response{
		sseResponse(`data: {"error":{"message":"Rate limit reached","type":"rate_limit_error"}}`, ``, `data: [DONE]`),
		sseResponse(goodOpenAIStream...),
	}}
	h := New(Config{Resolver: twoChannelResolver("openai-chat"), Upstream: up, DisableStreamCommit: true})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(openAIStreamRequest)))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Rate limit reached") {
		t.Fatalf("the upstream event must reach the client: %s", w.Body.String())
	}
	if len(up.channels) != 1 {
		t.Fatalf("with the hold disabled there is nothing to fail over from, attempts went to %v", up.channels)
	}
}
