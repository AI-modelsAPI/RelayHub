package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relayhub/internal/auth"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/secrets"
	"relayhub/internal/storage"
)

func TestP1_6_CLISyncLifecycleRedToGreen(t *testing.T) {
	tempDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	repo := repository.New(db.DB)
	authBackend, _ := auth.NewSQLKeyBackend(db.DB)
	localKeys := auth.NewLocalKeyServiceWithBackend(authBackend)

	srv, err := NewConfiguredServer(Config{
		Repo:        repo,
		LocalOnly:   false,
		LocalKeys:   localKeys,
		Token:       "test-secret",
		BackupDir:   filepath.Join(tempDir, "backups"),
		ClaudePath:  filepath.Join(tempDir, "claude", "settings.json"),
		CodexPath:   filepath.Join(tempDir, "codex", "config.toml"),
		HermesHome:  filepath.Join(tempDir, "hermes"),
		GatewayAddr: "127.0.0.1:8789",
	})
	if err != nil {
		t.Fatal(err)
	}

	h := srv.Handler()

	// 1. Detect: GET /api/v1/cli-sync should return available tools status
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cli-sync", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/cli-sync expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var getResp struct {
		Supported bool             `json:"supported"`
		Targets   []map[string]any `json:"targets"`
		Records   []any            `json:"records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal get resp: %v", err)
	}
	if !getResp.Supported {
		t.Fatalf("expected supported=true, got %v", getResp.Supported)
	}
	if len(getResp.Targets) != 3 {
		t.Fatalf("expected 3 targets (claude, codex, hermes), got %d: %+v", len(getResp.Targets), getResp.Targets)
	}

	// 2. Preview hermes: POST /api/v1/cli-sync with action=preview
	previewReqBody := `{"cli":"hermes","action":"preview","desired":{"model":"coding"}}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/cli-sync", strings.NewReader(previewReqBody))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/cli-sync (preview) expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var previewResp struct {
		Action string `json:"action"`
		CLI    string `json:"cli"`
		Diff   struct {
			HasChanges  bool     `json:"has_changes"`
			ChangedKeys []string `json:"changed_keys"`
			OldContent  string   `json:"old_content"`
			NewContent  string   `json:"new_content"`
			APIKey      string   `json:"api_key"`
		} `json:"diff"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &previewResp); err != nil {
		t.Fatalf("unmarshal preview resp: %v", err)
	}
	if !previewResp.Diff.HasChanges || len(previewResp.Diff.ChangedKeys) == 0 {
		t.Fatalf("expected diff with changes, got: %+v", previewResp)
	}
	if previewResp.Diff.APIKey == "" {
		t.Fatalf("expected real local api key issued in preview, got empty")
	}
	if !localKeys.Validate(previewResp.Diff.APIKey) {
		t.Fatalf("issued preview APIKey is not valid in localKeys: %s", previewResp.Diff.APIKey)
	}

	// 3. Apply hermes: POST /api/v1/cli-sync with action=apply
	applyReqBody := `{"cli":"hermes","action":"apply","desired":{"model":"coding"}}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/cli-sync", strings.NewReader(applyReqBody))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/cli-sync (apply) expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var applyResp struct {
		Action string `json:"action"`
		CLI    string `json:"cli"`
		Status string `json:"status"`
		Backup struct {
			BackupPath string `json:"backup_path"`
		} `json:"backup"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &applyResp); err != nil {
		t.Fatalf("unmarshal apply resp: %v", err)
	}
	if applyResp.Status != "success" {
		t.Fatalf("expected status=success, got %s", applyResp.Status)
	}

	// Check that hermes config and .env actually exist on disk
	hermesCfg := filepath.Join(tempDir, "hermes", "config.yaml")
	if _, err := os.Stat(hermesCfg); err != nil {
		t.Fatalf("hermes config not created: %v", err)
	}
	hermesEnv := filepath.Join(tempDir, "hermes", ".env")
	envData, err := os.ReadFile(hermesEnv)
	if err != nil || !strings.Contains(string(envData), "HERMES_CUSTOM_RELAYHUB_API_KEY=rh_") {
		t.Fatalf("hermes .env missing real key: err=%v, content=%s", err, string(envData))
	}

	// Verify history record in repository
	records, err := repo.ListCLISyncRecords(context.Background())
	if err != nil || len(records) == 0 {
		t.Fatalf("expected sync record created, got records=%+v err=%v", records, err)
	}
	if records[0].CLI != "hermes" || records[0].Status != "success" {
		t.Fatalf("unexpected sync record: %+v", records[0])
	}

	// 4. Verify action: POST /api/v1/cli-sync with action=verify
	verifyReqBody := `{"cli":"hermes","action":"verify","desired":{"model":"coding"}}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/cli-sync", strings.NewReader(verifyReqBody))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/cli-sync (verify) expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestP1_7_ImportExportLifecycleRedToGreen(t *testing.T) {
	tempDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	repo := repository.New(db.DB)
	keyProvider, _ := secrets.NewStaticKeyProvider(make([]byte, 32))
	secStore, err := secrets.NewProductionStore(keyProvider, db.DB)
	if err != nil {
		t.Fatal(err)
	}

	// Insert test data: 1 provider, 1 channel, 1 model, 1 route
	ctx := context.Background()
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p1", Name: "Prov1", Protocol: "openai"})
	_ = secStore.Put(ctx, "cred-ch1", []byte("sk-secret-ch1"))
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c1", ProviderID: "p1", Name: "Chan1", BaseURL: "http://example.com", CredentialRef: "cred-ch1"})
	_ = repo.CreateModel(ctx, domain.Model{ID: "m1", DisplayName: "Model1"})
	_ = repo.CreateRoute(ctx, domain.Route{ID: "r1", Name: "Route1", Protocol: "openai", ModelPattern: "m1"})

	srv, err := NewConfiguredServer(Config{
		Repo:        repo,
		SecretStore: secStore,
		LocalOnly:   false,
		Token:       "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	h := srv.Handler()

	// 1. GET /api/v1/import-export -> 200 supported: true
	req := httptest.NewRequest(http.MethodGet, "/api/v1/import-export", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/import-export expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var getResp struct {
		Supported bool `json:"supported"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil || !getResp.Supported {
		t.Fatalf("GET /api/v1/import-export invalid resp: %s", w.Body.String())
	}

	// 2. POST /api/v1/import-export action=export with password
	exportBody := `{"action":"export","include_secrets":true,"password":"Password-1234!"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/import-export", strings.NewReader(exportBody))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST export expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var exportResp struct {
		Package string `json:"package"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &exportResp); err != nil || exportResp.Package == "" {
		t.Fatalf("POST export missing package: %s", w.Body.String())
	}

	// 3. POST /api/v1/import-export action=preview
	previewBody, _ := json.Marshal(map[string]any{
		"action":  "preview",
		"package": exportResp.Package,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/import-export", bytes.NewReader(previewBody))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST import preview expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var previewResp struct {
		Action  string `json:"action"`
		Preview struct {
			Version       int  `json:"version"`
			HasSecrets    bool `json:"has_secrets"`
			TotalEntities int  `json:"total_entities"`
			Conflicts     []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"conflicts"`
		} `json:"preview"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &previewResp); err != nil {
		t.Fatalf("unmarshal preview resp: %v", err)
	}
	if previewResp.Preview.TotalEntities != 4 || len(previewResp.Preview.Conflicts) != 2 {
		t.Fatalf("unexpected preview: %+v", previewResp)
	}

	// 4. In clean repo, POST /api/v1/import-export action=apply
	db2, _ := storage.Open(filepath.Join(tempDir, "test2.db"))
	defer db2.Close()
	_ = db2.Migrate(context.Background())
	repo2 := repository.New(db2.DB)
	keyProvider2, _ := secrets.NewStaticKeyProvider(make([]byte, 32))
	secStore2, _ := secrets.NewProductionStore(keyProvider2, db2.DB)

	srv2, _ := NewConfiguredServer(Config{
		Repo:        repo2,
		SecretStore: secStore2,
		LocalOnly:   false,
		Token:       "test-secret",
	})
	h2 := srv2.Handler()

	applyBody, _ := json.Marshal(map[string]any{
		"action":   "apply",
		"package":  exportResp.Package,
		"password": "Password-1234!",
		"policy":   "skip",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/import-export", bytes.NewReader(applyBody))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h2.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST import apply expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify imported data in repo2 and secStore2
	p, err := repo2.GetProvider(ctx, "p1")
	if err != nil || p.Name != "Prov1" {
		t.Fatalf("provider not imported: %+v err=%v", p, err)
	}
	secVal, err := secStore2.Get(ctx, "cred-ch1")
	if err != nil || string(secVal) != "sk-secret-ch1" {
		t.Fatalf("secret not imported correctly: %s err=%v", string(secVal), err)
	}
}
