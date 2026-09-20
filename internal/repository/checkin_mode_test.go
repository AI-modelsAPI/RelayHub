package repository_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

func TestChannel_CheckinMode(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(filepath.Join(t.TempDir(), "test_checkin_mode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	repo := repository.New(db.DB)

	p := domain.Provider{
		ID:              "p-test",
		Name:            "Test Provider",
		AdapterType:     "generic",
		Protocol:        "openai-chat",
		BaseURLTemplate: "https://api.test",
		Capabilities:    "chat",
		Enabled:         true,
	}
	if err := repo.CreateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}

	// 1. Create channel with default checkin_mode (should default to "auto" if empty or preserved)
	chAuto := domain.Channel{
		ID:          "ch-auto",
		ProviderID:  p.ID,
		Name:        "Channel Auto",
		BaseURL:     "https://api.test",
		CheckinMode: "auto",
		Enabled:     true,
		Weight:      1,
		CreatedAt:   time.Now().UTC(),
	}
	if err := repo.CreateChannel(ctx, chAuto); err != nil {
		t.Fatalf("failed to create channel auto: %v", err)
	}

	gotAuto, err := repo.GetChannel(ctx, chAuto.ID)
	if err != nil {
		t.Fatalf("failed to get channel auto: %v", err)
	}
	if gotAuto.CheckinMode != "auto" {
		t.Errorf("expected CheckinMode 'auto', got %q", gotAuto.CheckinMode)
	}

	// 2. Create channel with checkin_mode = "manual"
	chManual := domain.Channel{
		ID:          "ch-manual",
		ProviderID:  p.ID,
		Name:        "Channel Manual",
		BaseURL:     "https://api.test",
		CheckinMode: "manual",
		Enabled:     true,
		Weight:      1,
		CreatedAt:   time.Now().UTC(),
	}
	if err := repo.CreateChannel(ctx, chManual); err != nil {
		t.Fatalf("failed to create channel manual: %v", err)
	}

	gotManual, err := repo.GetChannel(ctx, chManual.ID)
	if err != nil {
		t.Fatalf("failed to get channel manual: %v", err)
	}
	if gotManual.CheckinMode != "manual" {
		t.Errorf("expected CheckinMode 'manual', got %q", gotManual.CheckinMode)
	}

	// 3. Update channel checkin_mode from manual to auto
	gotManual.CheckinMode = "auto"
	if err := repo.UpdateChannel(ctx, gotManual); err != nil {
		t.Fatalf("failed to update channel: %v", err)
	}
	updated, err := repo.GetChannel(ctx, chManual.ID)
	if err != nil {
		t.Fatalf("failed to get updated channel: %v", err)
	}
	if updated.CheckinMode != "auto" {
		t.Errorf("expected CheckinMode 'auto' after update, got %q", updated.CheckinMode)
	}
}
