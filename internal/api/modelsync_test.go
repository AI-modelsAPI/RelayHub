package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
)

// AUDIT 2026-09-24 §5 B3: an upstream model list is a claim, not a fact.
// Reserved vendor names are only bound automatically by a channel the operator
// marked as an official source; everything else waits in a review queue, and a
// sync can be rolled back in one step.

// modelSyncServer is the identity test server pointed at a stub upstream that
// claims three reserved vendor names plus one of its own.
func modelSyncServer(t *testing.T) (http.Handler, *repository.SQLiteStore) {
	t.Helper()
	h, repo := identityTestServer(t)
	up := newModelListServer(t, []string{"claude-sonnet-4", "gpt-4o", "gemini-2.5-pro", "deepseek-r1"})
	ctx := context.Background()
	ch, err := repo.GetChannel(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	ch.BaseURL = up
	ch.ProxyURL = ""
	if err := repo.UpdateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	return h, repo
}

func doJSON(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, h, method, path, "", body)
}

// newModelListServer serves the given IDs as an OpenAI-style model list and
// returns its base URL.
func newModelListServer(t *testing.T, models []string) string {
	t.Helper()
	body := `{"object":"list","data":[`
	for i, m := range models {
		if i > 0 {
			body += ","
		}
		body += `{"id":"` + m + `"}`
	}
	body += `]}`
	var payload atomic.Value
	payload.Store(body)
	return modelListServer(t, &payload).URL
}

func pendingQueue(t *testing.T, h http.Handler) []struct {
	ChannelID string `json:"channel_id"`
	ModelID   string `json:"model_id"`
	Reason    string `json:"reason"`
} {
	t.Helper()
	w := doJSON(t, h, http.MethodGet, "/api/v1/models/pending", "")
	if w.Code != http.StatusOK {
		t.Fatalf("pending: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Pending []struct {
			ChannelID string `json:"channel_id"`
			ModelID   string `json:"model_id"`
			Reason    string `json:"reason"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("pending body %s: %v", w.Body.String(), err)
	}
	return out.Pending
}

func TestReservedModelNames(t *testing.T) {
	reserved := []string{
		"claude-sonnet-4", "claude-3-5-haiku-20241022", "Claude-Opus-4",
		"gpt-4o", "gpt-5-mini", "GPT4", "openai/gpt-4o", "anthropic/claude-sonnet-4",
		"gemini-2.5-pro", "o1", "o3-mini", "o4",
		"text-embedding-3-large", "dall-e-3", "whisper-1", "sora-2", "grok-4",
	}
	for _, id := range reserved {
		if !reservedModelName(id) {
			t.Errorf("%q must be reserved for official sources", id)
		}
	}
	open := []string{
		"deepseek-r1", "deepseek/deepseek-r1", "qwen3-235b-a22b", "kimi-k2",
		"glm-4.6", "gemma-3-27b", "my-claude-proxy", "openai-gpt-4o", "claudette-3",
		"llama-3.3-70b", "opus-relay", "orion-mini", "",
	}
	for _, id := range open {
		if reservedModelName(id) {
			t.Errorf("%q is not a vendor family name and must stay automatic", id)
		}
	}
}

func TestHoldDecision(t *testing.T) {
	official := domain.Channel{ID: "c1", OfficialSource: true}
	relay := domain.Channel{ID: "c1"}
	cases := []struct {
		name      string
		model     string
		ch        domain.Channel
		known     bool
		bg        bool
		onboarded bool
		want      string
	}{
		{"reserved name from an unvouched channel", "claude-sonnet-4", relay, false, false, false, "reserved_model_name"},
		{"reserved name on a background re-sync", "gpt-4o", relay, false, true, true, "reserved_model_name"},
		{"reserved name already bound by the operator", "gpt-4o", relay, true, true, true, ""},
		{"reserved name from an official source", "claude-sonnet-4", official, false, false, false, ""},
		{"contested name on a background re-sync", "deepseek-r1", relay, false, true, true, "served_elsewhere"},
		{"contested name on a manual sync", "deepseek-r1", relay, false, false, true, ""},
		{"contested name on a channel that is not onboarded yet", "deepseek-r1", relay, false, true, false, ""},
		{"fresh name", "kimi-k2", relay, false, true, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			previous := map[string]domain.ProviderModel{}
			if tc.known {
				previous[tc.model] = domain.ProviderModel{ModelID: tc.model, Enabled: true}
			}
			got := holdReasonFor(tc.model, holdContext{
				Channel:         tc.ch,
				Previous:        previous,
				ServedElsewhere: map[string]bool{"deepseek-r1": true},
				Background:      tc.bg,
				Onboarded:       tc.onboarded,
			})
			if got != tc.want {
				t.Fatalf("holdReasonFor(%q) = %q; want %q", tc.model, got, tc.want)
			}
		})
	}
}

func bindingsByModel(t *testing.T, repo *repository.SQLiteStore) map[string]domain.ProviderModel {
	t.Helper()
	pms, err := repo.ListProviderModels(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]domain.ProviderModel{}
	for _, pm := range pms {
		out[pm.ModelID] = pm
	}
	return out
}

func TestSyncHoldsReservedNamesUntilApproved(t *testing.T) {
	h, repo := modelSyncServer(t)
	ctx := context.Background()

	w := doJSON(t, h, http.MethodPost, "/api/v1/channels/sync-models", `{"channel_id":"c1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	var synced struct {
		Models []string `json:"models"`
		Held   []string `json:"held"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &synced); err != nil {
		t.Fatal(err)
	}
	if len(synced.Models) != 4 {
		t.Fatalf("synced models = %v", synced.Models)
	}
	if len(synced.Held) != 3 {
		t.Fatalf("held = %v; the three reserved names need review", synced.Held)
	}

	got := bindingsByModel(t, repo)
	if pm := got["claude-sonnet-4"]; pm.Enabled || pm.HeldReason != "reserved_model_name" {
		t.Fatalf("reserved binding went live: %+v", pm)
	}
	if pm := got["deepseek-r1"]; !pm.Enabled || pm.HeldReason != "" {
		t.Fatalf("a non-reserved name must not need approval: %+v", pm)
	}
	// The global models exist but are not routable until approved.
	if m, err := repo.GetModel(ctx, "claude-sonnet-4"); err != nil || m.Enabled {
		t.Fatalf("held model row = %+v (%v); it must exist disabled", m, err)
	}

	queue := pendingQueue(t, h)
	if len(queue) != 3 {
		t.Fatalf("pending = %+v", queue)
	}
	seen := map[string]string{}
	for _, item := range queue {
		if item.ChannelID != "c1" {
			t.Fatalf("pending item for another channel: %+v", item)
		}
		seen[item.ModelID] = item.Reason
	}
	if seen["gpt-4o"] != "reserved_model_name" || seen["gemini-2.5-pro"] != "reserved_model_name" {
		t.Fatalf("pending reasons = %+v", seen)
	}

	// Approving enables both the binding and the global model.
	w = doJSON(t, h, http.MethodPost, "/api/v1/models/pending", `{"channel_id":"c1","model_id":"claude-sonnet-4","action":"approve"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	pm, err := repo.GetProviderModel(ctx, "pm-c1-claude-sonnet-4")
	if err != nil || !pm.Enabled || pm.HeldReason != "" {
		t.Fatalf("approved binding = %+v (%v)", pm, err)
	}
	if m, err := repo.GetModel(ctx, "claude-sonnet-4"); err != nil || !m.Enabled {
		t.Fatalf("approved model = %+v (%v)", m, err)
	}
	if queue := pendingQueue(t, h); len(queue) != 2 {
		t.Fatalf("queue after approval = %+v", queue)
	}
}

func TestOfficialSourceChannelClaimsReservedNamesDirectly(t *testing.T) {
	h, repo := modelSyncServer(t)
	ctx := context.Background()

	ch, err := repo.GetChannel(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	ch.OfficialSource = true
	if err := repo.UpdateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if w := doJSON(t, h, http.MethodPost, "/api/v1/channels/sync-models", `{"channel_id":"c1"}`); w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	pm, err := repo.GetProviderModel(ctx, "pm-c1-claude-sonnet-4")
	if err != nil || !pm.Enabled || pm.HeldReason != "" {
		t.Fatalf("official source binding = %+v (%v); it must go live", pm, err)
	}
	if queue := pendingQueue(t, h); len(queue) != 0 {
		t.Fatalf("official source produced review items: %+v", queue)
	}
}

func TestPendingReviewRejectAndRemap(t *testing.T) {
	h, repo := modelSyncServer(t)
	ctx := context.Background()
	if w := doJSON(t, h, http.MethodPost, "/api/v1/channels/sync-models", `{"channel_id":"c1"}`); w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}

	// Rejecting a held claim drops the binding and the global name the sync
	// invented, so the relay's claim leaves nothing behind.
	if w := doJSON(t, h, http.MethodPost, "/api/v1/models/pending", `{"channel_id":"c1","model_id":"gpt-4o","action":"reject"}`); w.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", w.Code, w.Body.String())
	}
	if _, err := repo.GetProviderModel(ctx, "pm-c1-gpt-4o"); err == nil {
		t.Fatal("rejected binding still exists")
	}
	if _, err := repo.GetModel(ctx, "gpt-4o"); err == nil {
		t.Fatal("rejected model name still exists")
	}

	// Mapping: the operator decides which global name the claim may use.
	if w := doJSON(t, h, http.MethodPost, "/api/v1/models", `{"id":"my-gemini","display_name":"My Gemini","enabled":true}`); w.Code >= 400 {
		t.Fatalf("create target model: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(t, h, http.MethodPost, "/api/v1/models/pending", `{"channel_id":"c1","model_id":"gemini-2.5-pro","action":"approve","model_id_target":"my-gemini"}`); w.Code != http.StatusOK {
		t.Fatalf("remap: %d %s", w.Code, w.Body.String())
	}
	pm, err := repo.GetProviderModel(ctx, "pm-c1-gemini-2.5-pro")
	if err != nil {
		t.Fatalf("remapped binding missing: %v", err)
	}
	if pm.ModelID != "my-gemini" || pm.UpstreamModelName != "gemini-2.5-pro" || !pm.Enabled || pm.HeldReason != "" {
		t.Fatalf("remapped binding = %+v", pm)
	}
	if _, err := repo.GetModel(ctx, "gemini-2.5-pro"); err == nil {
		t.Fatal("the vendor name the relay invented must not stay in the catalog")
	}
	// The third reserved claim is still waiting: decisions are per claim.
	if queue := pendingQueue(t, h); len(queue) != 1 || queue[0].ModelID != "claude-sonnet-4" {
		t.Fatalf("queue after two decisions = %+v", queue)
	}
	if w := doJSON(t, h, http.MethodPost, "/api/v1/models/pending", `{"channel_id":"c1","model_id":"claude-sonnet-4","action":"reject"}`); w.Code != http.StatusOK {
		t.Fatalf("reject the last claim: %d %s", w.Code, w.Body.String())
	}
	if queue := pendingQueue(t, h); len(queue) != 0 {
		t.Fatalf("queue after decisions = %+v", queue)
	}

	// Unknown targets and decisions are refused.
	for _, body := range []string{
		`{"channel_id":"c1","model_id":"deepseek-r1","action":"approve"}`,
		`{"channel_id":"c1","model_id":"gpt-4o","action":"maybe"}`,
		`{"channel_id":"ghost","model_id":"gpt-4o","action":"approve"}`,
		`{"channel_id":"c1","model_id":"gpt-4o","action":"approve","model_id_target":"nope"}`,
	} {
		if w := doJSON(t, h, http.MethodPost, "/api/v1/models/pending", body); w.Code < 400 {
			t.Fatalf("%s accepted: %d %s", body, w.Code, w.Body.String())
		}
	}
}

func TestModelSyncUndoRestoresThePreviousBindings(t *testing.T) {
	h, repo := modelSyncServer(t)
	ctx := context.Background()
	// The channel already serves a model the operator bound by hand.
	if err := repo.CreateModel(ctx, domain.Model{ID: "hand-picked", DisplayName: "Hand", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateProviderModel(ctx, domain.ProviderModel{
		ID: "pm-c1-hand-picked", ProviderID: "p1", ChannelID: "c1", ModelID: "hand-picked",
		UpstreamModelName: "hand-picked", Protocol: "openai-chat", Priority: 7, Weight: 5, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	if w := doJSON(t, h, http.MethodPost, "/api/v1/channels/sync-models", `{"channel_id":"c1"}`); w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	if pms, _ := repo.ListProviderModels(ctx, ""); len(pms) != 4 {
		t.Fatalf("sync bindings = %d, want 4", len(pms))
	}
	snap, err := repo.LastModelSyncSnapshot(ctx, "c1")
	if err != nil {
		t.Fatalf("sync left no snapshot: %v", err)
	}
	if snap.Undone || len(snap.Bindings) != 1 || snap.Bindings[0].ModelID != "hand-picked" {
		t.Fatalf("snapshot = %+v", snap)
	}

	if w := doJSON(t, h, http.MethodPost, "/api/v1/channels/sync-undo", `{"channel_id":"c1"}`); w.Code != http.StatusOK {
		t.Fatalf("undo: %d %s", w.Code, w.Body.String())
	}
	pms, _ := repo.ListProviderModels(ctx, "")
	if len(pms) != 1 || pms[0].ModelID != "hand-picked" || pms[0].Priority != 7 || pms[0].Weight != 5 || !pms[0].Enabled {
		t.Fatalf("undo did not restore the binding: %+v", pms)
	}
	// Models the sync invented are gone with it; hand-made ones stay.
	if _, err := repo.GetModel(ctx, "claude-sonnet-4"); err == nil {
		t.Fatal("undo left a model the sync created")
	}
	if m, err := repo.GetModel(ctx, "hand-picked"); err != nil || !m.Enabled {
		t.Fatalf("undo touched a pre-existing model: %+v (%v)", m, err)
	}
	if snap, err := repo.LastModelSyncSnapshot(ctx, "c1"); err != nil || !snap.Undone {
		t.Fatalf("snapshot not marked undone: %+v (%v)", snap, err)
	}

	// There is nothing left to undo: the last sync is already rolled back.
	if w := doJSON(t, h, http.MethodPost, "/api/v1/channels/sync-undo", `{"channel_id":"c1"}`); w.Code != http.StatusConflict {
		t.Fatalf("second undo: %d %s; want 409", w.Code, w.Body.String())
	}
	if w := doJSON(t, h, http.MethodPost, "/api/v1/channels/sync-undo", `{"channel_id":"ghost"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown channel undo: %d", w.Code)
	}
}

func TestChannelOfficialSourceIsEditable(t *testing.T) {
	h, repo := modelSyncServer(t)
	ctx := context.Background()
	w := request(t, h, http.MethodPut, "/api/v1/channels/c1", "",
		`{"id":"c1","provider_id":"p1","name":"c1","base_url":"https://relay.example.com","enabled":true,"official_source":true}`)
	if w.Code >= 400 {
		t.Fatalf("save channel: %d %s", w.Code, w.Body.String())
	}
	ch, err := repo.GetChannel(ctx, "c1")
	if err != nil || !ch.OfficialSource {
		t.Fatalf("official_source did not persist: %+v (%v)", ch, err)
	}
	if w := doJSON(t, h, http.MethodGet, "/api/v1/channels/c1", ""); w.Code != http.StatusOK {
		t.Fatalf("read channel: %d", w.Code)
	} else {
		var out struct {
			Channel domain.Channel `json:"channel"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.Channel.OfficialSource {
			t.Fatalf("channel read back = %s (%v)", w.Body.String(), err)
		}
	}
}
