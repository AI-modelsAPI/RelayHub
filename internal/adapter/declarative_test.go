package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"relayhub/internal/domain"
)

func TestGenericAdapterModelsAndHealth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"claude-3-sonnet"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	g := NewGenericAdapter(ts.Client())
	ch := domain.Channel{BaseURL: ts.URL}

	models, err := g.Models(context.Background(), ch)
	if err != nil {
		t.Fatalf("Models failed: %v", err)
	}
	if len(models) != 2 || models[0].ID != "gpt-4o" {
		t.Fatalf("unexpected models: %+v", models)
	}

	h, err := g.Health(context.Background(), ch)
	if err != nil || h.Status != "healthy" {
		t.Fatalf("unexpected health: %+v, err=%v", h, err)
	}

	// Generic must explicitly refuse check-in
	_, err = g.CheckIn(context.Background(), ch)
	if err != ErrUnsupportedOperation {
		t.Fatalf("expected ErrUnsupportedOperation, got %v", err)
	}
}

func TestDeclarativeAdapterCheckinAndBalance(t *testing.T) {
	var checkinHit, balanceHit bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/checkin":
			checkinHit = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"reward":"1000","message":"checked in"}`))
		case "/api/quota":
			balanceHit = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"remaining":500000,"total":1000000}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := DeclarativeConfig{
		Name: "test-site",
		CheckIn: DeclarativeStep{
			Method: "POST",
			Path:   "api/checkin",
		},
		Balance: DeclarativeStep{
			Method: "GET",
			Path:   "/api/quota",
		},
	}

	adapter, err := NewDeclarativeAdapter(cfg, ts.Client())
	if err != nil {
		t.Fatalf("NewDeclarativeAdapter failed: %v", err)
	}

	ch := domain.Channel{BaseURL: ts.URL}
	res, err := adapter.CheckIn(context.Background(), ch)
	if err != nil || !res.Success || res.Reward != "1000" {
		t.Fatalf("checkin failed: %+v, err=%v", res, err)
	}
	if !checkinHit {
		t.Fatal("checkin endpoint was not hit")
	}

	bal, err := adapter.Balance(context.Background(), ch)
	if err != nil || bal.Remaining != 500000 {
		t.Fatalf("balance failed: %+v, err=%v", bal, err)
	}
	if !balanceHit {
		t.Fatal("balance endpoint was not hit")
	}
}

func TestDeclarativeSecuritySanitization(t *testing.T) {
	tests := []struct {
		name string
		step DeclarativeStep
	}{
		{
			name: "forbidden method DELETE",
			step: DeclarativeStep{Method: "DELETE", Path: "/api"},
		},
		{
			name: "forbidden method PUT",
			step: DeclarativeStep{Method: "PUT", Path: "/api"},
		},
		{
			name: "path traversal dots",
			step: DeclarativeStep{Method: "GET", Path: "../admin/delete"},
		},
		{
			name: "absolute URL attempt",
			step: DeclarativeStep{Method: "POST", Path: "https://attacker.com/leak"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewDeclarativeAdapter(DeclarativeConfig{
				Name:    "bad",
				CheckIn: tt.step,
			}, nil)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestRegistryResolve(t *testing.T) {
	reg := NewRegistry()
	g := NewGenericAdapter(nil)
	_ = reg.Register("generic", g)

	decl, _ := NewDeclarativeAdapter(DeclarativeConfig{Name: "my-site"}, nil)
	_ = reg.Register("my-site", decl)

	// Resolve generic provider
	p1 := domain.Provider{ID: "p1", Name: "OpenAI", AdapterType: "generic"}
	a1, err := reg.Resolve(p1)
	if err != nil || a1 != g {
		t.Fatalf("failed to resolve generic: %v", err)
	}

	// Resolve custom declarative provider
	p2 := domain.Provider{ID: "p2", Name: "Custom", AdapterType: "my-site"}
	a2, err := reg.Resolve(p2)
	if err != nil || a2 != decl {
		t.Fatalf("failed to resolve custom: %v", err)
	}

	// Unregistered adapter
	p3 := domain.Provider{ID: "p3", Name: "Unknown", AdapterType: "not-exist"}
	_, err = reg.Resolve(p3)
	if err == nil {
		t.Fatal("expected error for non-existent adapter")
	}
}
