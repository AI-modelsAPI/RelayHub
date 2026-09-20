package browser_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"relayhub/internal/browser"
	"sync/atomic"
	"testing"
	"time"
)

func TestCDPHumanGateBeforeAnyClick(t *testing.T) {
	info := browser.Detect()
	if !info.Available {
		t.Skip("Chrome unavailable")
	}
	for _, gate := range []string{`<div class="cf-turnstile"></div>`, `<p>人机验证</p>`} {
		t.Run(gate, func(t *testing.T) {
			var clicks atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/click" {
					clicks.Add(1)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, gate+`<button id="checkin-btn" onclick="fetch('/click',{method:'POST'});document.querySelector('#result').innerHTML='<div id=checkin-success>claimed</div>'">Sign</button><div id="result"></div>`)
			}))
			defer srv.Close()
			rt := browser.NewRuntime()
			defer rt.Close(context.Background())
			e := browser.NewCDPExecutor(rt, t.TempDir(), func() browser.Info { return info })
			res, err := e.ExecuteCheckin(context.Background(), browser.CheckinRequest{URL: srv.URL, ProviderID: "p", ChannelID: "c", Timeout: 10 * time.Second})
			if !errors.Is(err, browser.ErrCDPNeedManual) || res.Success || clicks.Load() != 0 {
				t.Fatalf("must yield before clicking: result=%+v err=%v clicks=%d", res, err, clicks.Load())
			}
		})
	}
}
