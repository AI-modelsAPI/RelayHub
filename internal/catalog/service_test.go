package catalog

import (
	"context"
	"path/filepath"
	"testing"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

func TestValidation(t *testing.T) {
	s := NewService()
	if e := s.AddProvider(domain.Provider{ID: "p", Name: "P", AdapterType: "generic", Protocol: "openai"}); e != nil {
		t.Fatal(e)
	}
	if e := s.AddProvider(domain.Provider{ID: "p"}); e == nil {
		t.Fatal("duplicate accepted")
	}
	if e := s.AddModel(domain.Model{ID: "m"}); e != nil {
		t.Fatal(e)
	}
	if e := s.AddChannel(domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://example.test"}); e != nil {
		t.Fatal(e)
	}
	if e := s.AddProviderModel(domain.ProviderModel{ID: "pm", ProviderID: "p", ChannelID: "c", ModelID: "m", UpstreamModelName: "m", Protocol: "openai"}); e != nil {
		t.Fatal(e)
	}
}
func TestServicePersistsAndContextCRUD(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s := NewService(repository.New(db.DB))
	p := domain.Provider{ID: "p", Name: "Provider", AdapterType: "generic", Protocol: "openai", Enabled: true}
	if err := s.CreateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetProvider(ctx, "p"); err != nil || got.ID != "p" {
		t.Fatalf("get provider: %+v %v", got, err)
	}
	p.Name = "Updated"
	if err := s.UpdateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetProvider(ctx, "p"); err != nil || got.Name != "Updated" {
		t.Fatalf("updated provider: %+v %v", got, err)
	}
	if err := s.DeleteProvider(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProvider(ctx, "p"); err == nil {
		t.Fatal("deleted provider still returned")
	}
}
func TestServiceLoadsPersistedCatalog(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store := repository.New(db.DB)
	if err := store.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "generic", Protocol: "openai", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s := NewService(store)
	if err := s.Load(ctx); err != nil {
		t.Fatal(err)
	}
	providers, _, _, _, _, _, _ := s.Snapshot()
	if providers["p"].Name != "P" {
		t.Fatalf("catalog did not load persisted provider: %+v", providers)
	}
}
func TestUpdatesValidateFallbackCyclesAndRoutes(t *testing.T) {
	s := NewService()
	for _, id := range []string{"a", "b"} {
		if err := s.CreateModelGroup(context.Background(), domain.ModelGroup{ID: id, Name: id, Strategy: "priority", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdateModelGroup(context.Background(), domain.ModelGroup{ID: "a", Name: "a", Strategy: "priority", FallbackGroupID: "missing", Enabled: true}); err == nil {
		t.Fatal("missing fallback accepted")
	}
	if err := s.UpdateModelGroup(context.Background(), domain.ModelGroup{ID: "a", Name: "a", Strategy: "priority", FallbackGroupID: "b", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateModelGroup(context.Background(), domain.ModelGroup{ID: "b", Name: "b", Strategy: "priority", FallbackGroupID: "a", Enabled: true}); err == nil {
		t.Fatal("fallback cycle accepted")
	}
	if err := s.CreateModel(context.Background(), domain.Model{ID: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRoute(context.Background(), domain.Route{ID: "r", Protocol: "openai", ModelPattern: "m", Strategy: "priority", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRoute(context.Background(), domain.Route{ID: "r", Protocol: "", ModelPattern: "m", Strategy: "invalid", Enabled: true}); err == nil {
		t.Fatal("invalid route update accepted")
	}
}
