package repository

import (
	"context"
	"testing"
	"time"

	"relayhub/internal/domain"
)

// Model provenance (AUDIT 2026-09-24 §5 B3): the review flag on a binding and
// the official-source mark on a channel are what the whole approval flow rests
// on, and the sync snapshot is the only thing that makes an undo possible, so
// every field has to survive a round trip.

func TestChannelOfficialSourceRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	created := timeNowUTC()
	if err := s.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "generic", Protocol: "openai-chat", Enabled: true, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateChannel(ctx, domain.Channel{ID: "vendor", ProviderID: "p", Name: "Vendor", BaseURL: "https://api.example.com", OfficialSource: true, Enabled: true, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateChannel(ctx, domain.Channel{ID: "relay", ProviderID: "p", Name: "Relay", BaseURL: "https://relay.example.com", Enabled: true, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetChannel(ctx, "vendor")
	if err != nil || !got.OfficialSource {
		t.Fatalf("vendor channel = %+v (%v)", got, err)
	}
	if other, err := s.GetChannel(ctx, "relay"); err != nil || other.OfficialSource {
		t.Fatalf("relay channel = %+v (%v); the mark must default off", other, err)
	}
	// Turning it off again has to stick too (it is an operator toggle).
	got.OfficialSource = false
	if err := s.UpdateChannel(ctx, got); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.GetChannel(ctx, "vendor"); again.OfficialSource {
		t.Fatal("official_source could not be turned off")
	}
}

func TestProviderModelHeldReasonRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	created := timeNowUTC()
	if err := s.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "generic", Protocol: "openai-chat", Enabled: true, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://relay.example.com", Enabled: true, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	// provider_models references models(id), so the global name must exist.
	if err := s.CreateModel(ctx, domain.Model{ID: "claude-sonnet-4", DisplayName: "Claude Sonnet 4"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProviderModel(ctx, domain.ProviderModel{ID: "pm", ProviderID: "p", ChannelID: "c", ModelID: "claude-sonnet-4", UpstreamModelName: "claude-sonnet-4", HeldReason: "reserved_model_name"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProviderModel(ctx, "pm")
	if err != nil || got.HeldReason != "reserved_model_name" {
		t.Fatalf("binding = %+v (%v)", got, err)
	}
	// Updating it (the approval path) clears the flag.
	got.HeldReason, got.Enabled = "", true
	if err := s.UpdateProviderModel(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := s.GetProviderModel(ctx, "pm")
	if again.HeldReason != "" || !again.Enabled {
		t.Fatalf("cleared binding = %+v", again)
	}
	list, err := s.ListProviderModels(ctx, "")
	if err != nil || len(list) != 1 || list[0].HeldReason != "" {
		t.Fatalf("list = %+v (%v)", list, err)
	}
}

func TestModelSyncSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	created := timeNowUTC()
	snap := domain.ModelSyncSnapshot{
		ID: "ms1", ChannelID: "c1", Source: "manual", CreatedAt: created,
		Bindings: []domain.ProviderModel{{
			ID: "pm-old", ProviderID: "p", ChannelID: "c1", ModelID: "hand-picked",
			UpstreamModelName: "hand-picked", Protocol: "openai-chat",
			RequestTransform: "req", ResponseTransform: "resp",
			Priority: 7, Weight: 5, Enabled: true, HeldReason: "served_elsewhere",
		}},
		AddedModels: []string{"claude-sonnet-4", "deepseek-r1"},
		Held:        []string{"claude-sonnet-4"},
	}
	if err := s.CreateModelSyncSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	second := snap
	second.ID, second.Source, second.CreatedAt = "ms2", "background", created.Add(time.Minute)
	if err := s.CreateModelSyncSnapshot(ctx, second); err != nil {
		t.Fatal(err)
	}

	got, err := s.LastModelSyncSnapshot(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "ms2" || got.Source != "background" || got.Undone {
		t.Fatalf("last snapshot = %+v", got)
	}
	if len(got.Bindings) != 1 {
		t.Fatalf("bindings not round-tripped: %+v", got.Bindings)
	}
	pm := got.Bindings[0]
	if pm.ID != "pm-old" || pm.ModelID != "hand-picked" || pm.UpstreamModelName != "hand-picked" ||
		pm.Protocol != "openai-chat" || pm.RequestTransform != "req" || pm.ResponseTransform != "resp" ||
		pm.Priority != 7 || pm.Weight != 5 || !pm.Enabled || pm.HeldReason != "served_elsewhere" {
		t.Fatalf("binding = %+v", pm)
	}
	if len(got.AddedModels) != 2 || got.AddedModels[0] != "claude-sonnet-4" || len(got.Held) != 1 {
		t.Fatalf("model lists = %+v %+v", got.AddedModels, got.Held)
	}
	if !got.CreatedAt.Equal(created.Add(time.Minute)) {
		t.Fatalf("created_at = %v", got.CreatedAt)
	}

	// A channel that never synced has nothing to undo.
	if _, err := s.LastModelSyncSnapshot(ctx, "other"); err != ErrNotFound {
		t.Fatalf("unknown channel snapshot err = %v", err)
	}

	// Once a snapshot has been rolled back it stays in the history, marked
	// undone, so the API can answer "nothing to undo" instead of undoing the
	// sync before it.
	if err := s.MarkModelSyncSnapshotUndone(ctx, "ms2"); err != nil {
		t.Fatal(err)
	}
	again, err := s.LastModelSyncSnapshot(ctx, "c1")
	if err != nil || again.ID != "ms2" || !again.Undone {
		t.Fatalf("undone mark not stored: %+v (%v)", again, err)
	}
	if len(again.Bindings) != 1 || again.Bindings[0].ModelID != "hand-picked" {
		t.Fatalf("undo history lost the before-image: %+v", again.Bindings)
	}
}
