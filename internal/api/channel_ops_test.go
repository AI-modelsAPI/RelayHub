package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"relayhub/internal/domain"
)

func TestChannelDuplicate(t *testing.T) {
	srv, repo := newCatalogServer(t)
	h := srv.Handler()
	ctx := context.Background()
	// Give the source a credential and enabled state to prove the clone drops them.
	src, _ := repo.GetChannel(ctx, "c")
	src.CredentialRef = "cred:c"
	src.Enabled = true
	src.Status = "enabled"
	src.RoutingTags = "prod"
	_ = repo.UpdateChannel(ctx, src)

	w := catalogDo(t, h, http.MethodPost, "/api/v1/channels/duplicate", `{"channel_id":"c","new_id":"c2","new_name":"Clone"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("duplicate status=%d body=%s", w.Code, w.Body.String())
	}
	clone, err := repo.GetChannel(ctx, "c2")
	if err != nil {
		t.Fatalf("clone not persisted: %v", err)
	}
	if clone.Name != "Clone" || clone.RoutingTags != "prod" {
		t.Fatalf("clone did not copy config: %+v", clone)
	}
	if clone.CredentialRef != "" {
		t.Fatalf("clone must not reuse source credential, got %q", clone.CredentialRef)
	}
	if clone.Enabled || clone.Status != "disabled" {
		t.Fatalf("clone must start disabled, got enabled=%v status=%q", clone.Enabled, clone.Status)
	}
}

func TestChannelBatchArchive(t *testing.T) {
	srv, repo := newCatalogServer(t)
	h := srv.Handler()
	ctx := context.Background()
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c3", ProviderID: "p", Name: "C3", BaseURL: "https://up.test", Weight: 1, Enabled: true, Status: "enabled"})

	w := catalogDo(t, h, http.MethodPost, "/api/v1/channels/batch", `{"action":"archive","ids":["c","c3"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("batch status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Affected int `json:"affected"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Affected != 2 {
		t.Fatalf("expected 2 affected, got %d", out.Affected)
	}
	for _, id := range []string{"c", "c3"} {
		c, _ := repo.GetChannel(ctx, id)
		if c.Enabled || c.Status != "archived" {
			t.Fatalf("channel %s not archived: enabled=%v status=%q", id, c.Enabled, c.Status)
		}
	}
}
