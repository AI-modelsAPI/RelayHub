package ratelimit

import (
	"net/http"
	"testing"
	"time"
)

func TestAIMDHalvesOn429AndWaits(t *testing.T) {
	l := New()
	l.Observe("k", 200, 0, nil)
	l.Observe("k", 429, time.Second, http.Header{"Retry-After": []string{"1"}})
	w := l.Wait("k")
	if w <= 0 {
		t.Fatalf("expected wait after 429, got %v", w)
	}
	rate, learned := l.Snapshot("k")
	if !learned || rate <= 0 {
		t.Fatalf("rate=%v learned=%v", rate, learned)
	}
}

func TestAIMDReadsLimitHeader(t *testing.T) {
	l := New()
	l.Observe("k", 200, 0, http.Header{"X-Ratelimit-Limit-Requests": []string{"120"}})
	rate, _ := l.Snapshot("k")
	if rate < 1.9 || rate > 2.1 {
		t.Fatalf("want ~2 rps from 120 rpm, got %v", rate)
	}
}
