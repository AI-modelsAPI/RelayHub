package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// AUDIT 2026-09-24 F6: upstream model lists were trusted verbatim (no
// validation or bound) and every re-sync reset operator binding edits.
func TestModelSyncValidatesBoundsAndPreservesOperatorEdits(t *testing.T) {
	var body atomic.Value
	body.Store(`{"object":"list","data":[{"id":"gpt-4o"},{"id":"gpt-4o"},{"id":"bad id"},{"id":"ctl\u0007x"},{"id":"deepseek/deepseek-r1"}]}`)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer up.Close()
	h, repo := identityTestServer(t)
	ctx := context.Background()
	ch, _ := repo.GetChannel(ctx, "c1")
	ch.BaseURL = up.URL
	ch.ProxyURL = ""
	if err := repo.UpdateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}

	w := request(t, h, http.MethodPost, "/api/v1/channels/sync-models", "", `{"channel_id":"c1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	pms, _ := repo.ListProviderModels(ctx, "")
	got := map[string]bool{}
	for _, pm := range pms {
		got[pm.ModelID] = true
	}
	if len(pms) != 2 || !got["gpt-4o"] || !got["deepseek/deepseek-r1"] {
		t.Fatalf("expected only the two valid, de-duplicated models, got %+v", got)
	}

	// Operator edits survive a re-sync.
	for _, pm := range pms {
		if pm.ModelID == "gpt-4o" {
			pm.Priority, pm.Weight, pm.Enabled = 7, 3, false
			if err := repo.UpdateProviderModel(ctx, pm); err != nil {
				t.Fatal(err)
			}
		}
	}
	if w := request(t, h, http.MethodPost, "/api/v1/channels/sync-models", "", `{"channel_id":"c1"}`); w.Code != http.StatusOK {
		t.Fatalf("re-sync: %d %s", w.Code, w.Body.String())
	}
	pms, _ = repo.ListProviderModels(ctx, "gpt-4o")
	if len(pms) != 1 || pms[0].Priority != 7 || pms[0].Weight != 3 || pms[0].Enabled {
		t.Fatalf("re-sync reset operator edits: %+v", pms)
	}

	// Absurd lists are refused rather than registered.
	var b strings.Builder
	b.WriteString(`{"object":"list","data":[`)
	for i := 0; i < 2500; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":"m-%d"}`, i)
	}
	b.WriteString(`]}`)
	body.Store(b.String())
	if w := request(t, h, http.MethodPost, "/api/v1/channels/sync-models", "", `{"channel_id":"c1"}`); w.Code < 400 {
		t.Fatalf("oversized model list accepted: %d", w.Code)
	}
	if pms, _ := repo.ListProviderModels(ctx, ""); len(pms) != 2 {
		t.Fatalf("refused sync still changed bindings: %d", len(pms))
	}
}
