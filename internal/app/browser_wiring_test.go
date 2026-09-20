package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"relayhub/internal/adapter"
	"relayhub/internal/browser"
	"relayhub/internal/domain"
	"strings"
	"testing"
	"time"
)

type webviewFixtureAdapter struct{ adapter.ProviderAdapter }

func (webviewFixtureAdapter) CheckIn(context.Context, domain.Channel) (adapter.CheckInResult, error) {
	return adapter.CheckInResult{}, adapter.ErrNeedWebview
}

func TestFullStackBrowserCheckinWiring(t *testing.T) {
	if !browser.Detect().Available {
		t.Skip("system Chromium unavailable")
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<button id="checkin-btn" onclick="this.disabled=true;document.getElementById('result').innerHTML='<div id=checkin-success data-reward=1></div>'">Sign</button><div id="result"></div>`)
	}))
	defer fixture.Close()
	a, err := New(Config{DataDir: t.TempDir(), HTTPProxyAddr: "127.0.0.1:0", SOCKS5Addr: "127.0.0.1:0", GatewayAddr: "127.0.0.1:0", ManagementAddr: "127.0.0.1:0", WireFullStack: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if err := a.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	rt := a.Runtime()
	if err := rt.Adapters.Register("fixture", webviewFixtureAdapter{}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "fixture", AdapterType: "fixture", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Repo.CreateChannel(ctx, domain.Channel{ID: "c", Name: "fixture", ProviderID: "p", BaseURL: fixture.URL, Enabled: true, CheckinEnabled: true}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post("http://"+rt.APIListener.Addr().String()+"/api/v1/checkin", "application/json", strings.NewReader(`{"channel_id":"c"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"status":"success"`) {
		t.Fatalf("actual management request did not execute browser: %d %s", resp.StatusCode, body)
	}
	records, err := rt.Repo.ListCheckinRecords(ctx, "c")
	if err != nil || len(records) != 1 || records[0].Status != "success" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}
