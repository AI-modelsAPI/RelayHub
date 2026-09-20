package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relayhub/internal/api"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

// newCatalogServer builds a repo-backed Server with one provider/channel so
// batch-created and cataloged models have a home.
func newCatalogServer(t *testing.T) (*api.Server, repository.ResourceRepository) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db.DB)
	if err := repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "generic", Protocol: "openai-chat", BaseURLTemplate: "https://up.test", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://up.test", Weight: 1, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	srv, err := api.NewConfiguredServer(api.Config{Token: "test-token", Repo: repo, LocalOnly: true, StartedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return srv, repo
}

func catalogDo(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestModelBatchCreate(t *testing.T) {
	srv, repo := newCatalogServer(t)
	h := srv.Handler()
	body := `{"models":[
		{"id":"gpt-x","display_name":"GPT-X","developer":"OpenAI","model_type":"llm","input_price":2.5,"output_price":10},
		{"id":"emb-1","display_name":"Emb 1","developer":"OpenAI","model_type":"embedding"}
	]}`
	w := catalogDo(t, h, http.MethodPost, "/api/v1/models/batch", body)
	if w.Code != http.StatusOK {
		t.Fatalf("batch create status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Created int `json:"created"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Created != 2 {
		t.Fatalf("expected 2 created, got %d (%s)", out.Created, w.Body.String())
	}
	m, err := repo.GetModel(context.Background(), "gpt-x")
	if err != nil || m.Developer != "OpenAI" || m.ModelType != "llm" || m.InputPrice != 2.5 {
		t.Fatalf("gpt-x not persisted with catalog fields: %+v err=%v", m, err)
	}
}

func TestModelBatchEnableDisable(t *testing.T) {
	srv, repo := newCatalogServer(t)
	h := srv.Handler()
	ctx := context.Background()
	_ = repo.CreateModel(ctx, domain.Model{ID: "m1", DisplayName: "M1", Enabled: true})
	_ = repo.CreateModel(ctx, domain.Model{ID: "m2", DisplayName: "M2", Enabled: true})
	w := catalogDo(t, h, http.MethodPost, "/api/v1/models/batch", `{"action":"disable","ids":["m1","m2"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("batch disable status=%d body=%s", w.Code, w.Body.String())
	}
	m1, _ := repo.GetModel(ctx, "m1")
	m2, _ := repo.GetModel(ctx, "m2")
	if m1.Enabled || m2.Enabled {
		t.Fatalf("expected both disabled, got m1=%v m2=%v", m1.Enabled, m2.Enabled)
	}
}

func TestModelCatalogAggregatesBindings(t *testing.T) {
	srv, repo := newCatalogServer(t)
	h := srv.Handler()
	ctx := context.Background()
	_ = repo.CreateModel(ctx, domain.Model{ID: "m1", DisplayName: "M1", Developer: "Anthropic", ModelType: "llm", InputPrice: 3, OutputPrice: 15, Enabled: true})
	// Two provider-model bindings referencing m1 (different upstream names).
	_ = repo.CreateProviderModel(ctx, domain.ProviderModel{ID: "pm1", ProviderID: "p", ChannelID: "c", ModelID: "m1", UpstreamModelName: "up-a", Protocol: "openai-chat", Weight: 1, Enabled: true})
	_ = repo.CreateProviderModel(ctx, domain.ProviderModel{ID: "pm2", ProviderID: "p", ChannelID: "c", ModelID: "m1", UpstreamModelName: "up-b", Protocol: "openai-chat", Weight: 1, Enabled: true})

	w := catalogDo(t, h, http.MethodGet, "/api/v1/models/catalog", "")
	if w.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Catalog []struct {
			ID           string  `json:"id"`
			Developer    string  `json:"developer"`
			ModelType    string  `json:"model_type"`
			InputPrice   float64 `json:"input_price"`
			BindingCount int     `json:"binding_count"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode catalog: %v (%s)", err, w.Body.String())
	}
	if len(out.Catalog) != 1 {
		t.Fatalf("expected 1 catalog entry, got %d", len(out.Catalog))
	}
	e := out.Catalog[0]
	if e.ID != "m1" || e.Developer != "Anthropic" || e.ModelType != "llm" || e.InputPrice != 3 || e.BindingCount != 2 {
		t.Fatalf("catalog entry wrong: %+v", e)
	}
}
