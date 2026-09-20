package browser_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"relayhub/internal/browser"
	"testing"
	"time"
)

func TestCDPDoesNotAcceptHiddenSuccess(t *testing.T) {
	info := browser.Detect()
	if !info.Available {
		t.Skip("Chrome unavailable")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<div id="checkin-success" hidden>not actually successful</div><p>Waiting</p>`)
	}))
	defer srv.Close()
	rt := browser.NewRuntime()
	defer rt.Close(context.Background())
	e := browser.NewCDPExecutor(rt, t.TempDir(), func() browser.Info { return info })
	res, err := e.ExecuteCheckin(context.Background(), browser.CheckinRequest{URL: srv.URL, ProviderID: "p", ChannelID: "c", Timeout: 5 * time.Second})
	if res.Success || err == nil {
		t.Fatalf("hidden element produced false success: %+v %v", res, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout waiting for visible evidence: %v", err)
	}
}
