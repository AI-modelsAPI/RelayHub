package checkin_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"relayhub/internal/adapter"
	"relayhub/internal/browser"
	"relayhub/internal/checkin"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"relayhub/internal/storage"
)

type capturingExecutor struct {
	req    browser.CheckinRequest
	result browser.CheckinResult
	err    error
}

func (c *capturingExecutor) ExecuteCheckin(ctx context.Context, req browser.CheckinRequest) (browser.CheckinResult, error) {
	c.req = req
	return c.result, c.err
}

// The browser must be launched through the channel's egress proxy and with
// the provider's declared selectors, otherwise the "unified exit" story only
// covers two of the three outbound paths.
func TestSchedulerPassesEgressAndSelectorsToBrowser(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "egress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db.DB)
	_ = repo.CreateProvider(ctx, domain.Provider{ID: "p", Name: "P", AdapterType: "seekai", Enabled: true, Capabilities: `{"checkin_button":"button.daily"}`})
	_ = repo.CreateChannel(ctx, domain.Channel{ID: "c", ProviderID: "p", Name: "C", BaseURL: "https://seekai.example", Enabled: true, CheckinEnabled: true, ProxyURL: "socks5://127.0.0.1:1080"})
	reg := adapter.NewRegistry()
	_ = reg.Register("seekai", &needWebviewMockAdapter{})
	sched := checkin.NewScheduler(checkin.Config{Interval: time.Hour, BaseBackoff: time.Millisecond, MaxRetries: 0}, reg, repo)
	sched.SetBrowserDetector(func() browser.Info { return browser.Info{Available: true, Path: "/dummy/chrome"} })
	exec := &capturingExecutor{err: browser.ErrCDPNeedManual}
	sched.SetBrowserExecutor(exec)
	sched.SetProxyResolver(func(ch domain.Channel) string { return ch.ProxyURL })

	_ = sched.RunNow(ctx, "c")
	if exec.req.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("browser request must carry the channel egress proxy, got %q", exec.req.ProxyURL)
	}
	if exec.req.Selectors.Button != "button.daily" || exec.req.Selectors.Success != browser.DefaultSelectors().Success {
		t.Fatalf("selectors from provider capabilities not applied: %+v", exec.req.Selectors)
	}
}
