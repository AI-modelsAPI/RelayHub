package browser_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"relayhub/internal/browser"
	"sync/atomic"
	"testing"
	"time"
)

func TestCDPClicksOnceWhileWaiting(t *testing.T) {
	info := browser.Detect()
	if !info.Available {
		t.Skip("Chrome unavailable")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/clicked" {
			requests.Add(1)
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<button id="checkin-btn" onclick="clicks++;fetch('/clicked',{method:'POST'});if(clicks===1)setTimeout(()=>{document.querySelector('#result').innerHTML='<div id=checkin-success data-reward='+clicks+'></div>'},1200)">Sign</button><div id="result"></div><script>let clicks=0</script>`)
	}))
	defer server.Close()
	rt := browser.NewRuntime()
	defer rt.Close(context.Background())
	executor := browser.NewCDPExecutor(rt, t.TempDir(), func() browser.Info { return info })
	// Executor default is 30s; a 15s budget flaked under -race load (chrome
	// start + CDP ready + navigate exceeded it), and CI once needed more than
	// 15s just to attach. The generous budget only matters when the machine
	// is slow; a passing run takes about a second.
	res, err := executor.ExecuteCheckin(context.Background(), browser.CheckinRequest{URL: server.URL, ProviderID: "p", ChannelID: "c", Timeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.Reward != "1" || requests.Load() != 1 {
		t.Fatalf("must click only once, result=%+v server_requests=%d", res, requests.Load())
	}
}
