package repository

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/storage"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return New(db.DB)
}

func TestAllResourceFamiliesRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	created := timeNowUTC()
	if err := s.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "generic", Protocol: "openai-chat", BaseURLTemplate: "https://provider.test", Capabilities: "chat", CheckinEnabled: true, AdapterVersion: "1", Enabled: true, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://provider.test", RoutingTags: "prod", QuotaState: "available", HealthState: "healthy", RateLimitState: "ready", Enabled: true, RoutingEnabled: true, CheckinEnabled: true, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateModel(ctx, domain.Model{ID: "m", DisplayName: "M", Aliases: []string{"alias"}, Family: "family", ProtocolRequirements: "chat", Capabilities: "tools", ContextWindow: 100, MaxOutputTokens: 20, ReasoningSupport: true, VisionSupport: true, ToolCallSupport: true, Enabled: true, Developer: "OpenAI", ModelType: "llm", InputPrice: 2.5, OutputPrice: 10, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProviderModel(ctx, domain.ProviderModel{ID: "pm", ProviderID: "p", ChannelID: "c", ModelID: "m", UpstreamModelName: "upstream", Protocol: "openai-chat", RequestTransform: "request", ResponseTransform: "response", Enabled: true, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateModelGroup(ctx, domain.ModelGroup{ID: "g", Name: "group", Description: "desc", Strategy: "priority", Enabled: true, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddModelGroupMember(ctx, domain.ModelGroupMember{GroupID: "g", ModelID: "m", Priority: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRoute(ctx, domain.Route{ID: "r", Name: "route", Protocol: "openai-chat", ModelPattern: "m", Strategy: "priority", ChannelFilter: "prod", RetryPolicy: "retry", FallbackPolicy: "fallback", Enabled: true, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	finished := created.Add(time.Second)
	if err := s.CreateCheckinRecord(ctx, domain.CheckinRecord{ID: "ci", ChannelID: "c", StartedAt: created, FinishedAt: &finished, Status: "success", Reward: "10"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateHealthRecord(ctx, domain.HealthRecord{ID: "h", ChannelID: "c", CheckedAt: created, Status: "healthy", LatencyMS: 12}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRequestRecord(ctx, domain.RequestRecord{ID: "rr", RequestID: "request", Protocol: "openai-chat", ModelID: "m", ProviderID: "p", ChannelID: "c", StatusCode: 200, LatencyMS: 42, InputTokens: 3, OutputTokens: 4, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateCLISyncRecord(ctx, domain.CLISyncRecord{ID: "sync", CLI: "codex", ConfigPath: "/tmp/config", Status: "success", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateConfigBackup(ctx, domain.ConfigBackup{ID: "backup", Path: "/tmp/backup", SHA256: "hash", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}

	if got, err := s.GetProvider(ctx, "p"); err != nil || got.BaseURLTemplate != "https://provider.test" {
		t.Fatalf("provider=%+v err=%v", got, err)
	}
	if got, err := s.GetChannel(ctx, "c"); err != nil || got.HealthState != "healthy" {
		t.Fatalf("channel=%+v err=%v", got, err)
	}
	if got, err := s.GetModel(ctx, "m"); err != nil || len(got.Aliases) != 1 || !got.VisionSupport || got.Developer != "OpenAI" || got.ModelType != "llm" || got.InputPrice != 2.5 || got.OutputPrice != 10 {
		t.Fatalf("model=%+v err=%v", got, err)
	}
	if got, err := s.GetProviderModel(ctx, "pm"); err != nil || got.ModelID != "m" || got.RequestTransform != "request" {
		t.Fatalf("provider model=%+v err=%v", got, err)
	}
	if got, err := s.GetModelGroup(ctx, "g"); err != nil || got.Name != "group" {
		t.Fatalf("group=%+v err=%v", got, err)
	}
	if members, err := s.ListModelGroupMembers(ctx, "g"); err != nil || len(members) != 1 {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	if got, err := s.GetRoute(ctx, "r"); err != nil || got.ChannelFilter != "prod" {
		t.Fatalf("route=%+v err=%v", got, err)
	}
	if got, err := s.GetCheckinRecord(ctx, "ci"); err != nil || got.FinishedAt == nil {
		t.Fatalf("checkin=%+v err=%v", got, err)
	}
	if got, err := s.GetHealthRecord(ctx, "h"); err != nil || got.LatencyMS != 12 {
		t.Fatalf("health=%+v err=%v", got, err)
	}
	if got, err := s.GetRequestRecord(ctx, "rr"); err != nil || got.StatusCode != 200 {
		t.Fatalf("request=%+v err=%v", got, err)
	}
	if got, err := s.GetCLISyncRecord(ctx, "sync"); err != nil || got.CLI != "codex" {
		t.Fatalf("sync=%+v err=%v", got, err)
	}
	if got, err := s.GetConfigBackup(ctx, "backup"); err != nil || got.SHA256 != "hash" {
		t.Fatalf("backup=%+v err=%v", got, err)
	}
}

func TestProviderDeletionRejectedWhileReferencedAndLogicalModelIDPreserved(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "generic", Protocol: "openai-chat"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://provider.test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateModel(ctx, domain.Model{ID: "logical-model", DisplayName: "Logical"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProviderModel(ctx, domain.ProviderModel{ID: "pm", ProviderID: "p", ChannelID: "c", ModelID: "logical-model", UpstreamModelName: "upstream-a", Protocol: "openai-chat"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProviderModel(ctx, domain.ProviderModel{ID: "pm", ProviderID: "p", ChannelID: "c", ModelID: "logical-model", UpstreamModelName: "upstream-b", Protocol: "openai-chat"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProviderModel(ctx, "pm")
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelID != "logical-model" || got.UpstreamModelName != "upstream-b" {
		t.Fatalf("mapping changed logical identity: %+v", got)
	}
	if err := s.DeleteProvider(ctx, "p"); err != ErrProviderReferenced {
		t.Fatalf("delete error=%v", err)
	}
}

func TestTransactionComposesRepositoryWritesAndRollsBack(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	err := s.WithTx(ctx, func(tx *Tx) error {
		if err := tx.CreateProvider(ctx, domain.Provider{ID: "rollback-provider", Name: "Rollback"}); err != nil {
			return err
		}
		if err := tx.CreateChannel(ctx, domain.Channel{ID: "rollback-channel", ProviderID: "rollback-provider", Name: "Rollback channel"}); err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("expected transaction error")
	}
	if _, err := s.GetProvider(ctx, "rollback-provider"); err != ErrNotFound {
		t.Fatalf("provider survived rollback: %v", err)
	}

	if err := s.WithTx(ctx, func(tx *Tx) error {
		if err := tx.CreateProvider(ctx, domain.Provider{ID: "commit-provider", Name: "Commit"}); err != nil {
			return err
		}
		return tx.CreateChannel(ctx, domain.Channel{ID: "commit-channel", ProviderID: "commit-provider", Name: "Commit channel"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProvider(ctx, "commit-provider"); err != nil {
		t.Fatalf("provider was not committed: %v", err)
	}
	if _, err := s.GetChannel(ctx, "commit-channel"); err != nil {
		t.Fatalf("channel was not committed: %v", err)
	}
}

func timeNowUTC() time.Time { return time.Now().UTC().Truncate(time.Nanosecond) }
