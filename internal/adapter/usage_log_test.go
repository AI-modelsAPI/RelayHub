package adapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"relayhub/internal/domain"
)

// The site-side consumption log is the other half of billing reconciliation
// (AUDIT §5 B2): every field the verdict depends on has to survive the shapes
// new-api forks actually return.

// usageSite serves /api/status and a paged /api/log/self, recording the
// queries it saw.
type usageSite struct {
	server  *httptest.Server
	items   []string // one JSON object per billed request, newest first
	total   int
	queries []string
	status  int
	body    string // overrides the page body when set
}

func newUsageSite(t *testing.T, items []string) *usageSite {
	t.Helper()
	s := &usageSite{items: items, total: len(items), status: http.StatusOK}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		case "/api/log/self":
			s.queries = append(s.queries, r.URL.RawQuery)
			if s.status != http.StatusOK {
				w.WriteHeader(s.status)
				return
			}
			if s.body != "" {
				_, _ = w.Write([]byte(s.body))
				return
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("p"))
			size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
			start := (page - 1) * size
			if start > len(s.items) {
				start = len(s.items)
			}
			end := start + size
			if end > len(s.items) {
				end = len(s.items)
			}
			_, _ = fmt.Fprintf(w, `{"success":true,"data":{"items":[%s],"total":%d,"page":%d,"page_size":%d}}`,
				strings.Join(s.items[start:end], ","), s.total, page, size)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *usageSite) adapter() *BaseNewAPIAdapter {
	return &BaseNewAPIAdapter{Secrets: staticSecrets{"cred": []byte("token-123")}}
}

func (s *usageSite) channel() domain.Channel {
	return domain.Channel{ID: "c1", BaseURL: s.server.URL, CredentialRef: "cred"}
}

func TestUsageLogReadsEntriesAndMultipliers(t *testing.T) {
	inside := time.Date(2026, 9, 25, 4, 0, 0, 0, time.UTC)
	// One entry with a Unix timestamp and a string-encoded `other`, one with
	// a formatted local timestamp and an object `other`.
	items := []string{
		fmt.Sprintf(`{"id":2,"created_at":%d,"type":2,"model_name":"claude-x","quota":250000,"prompt_tokens":400000,"completion_tokens":100000,"other":"{\"model_ratio\":1.5,\"group_ratio\":1.2,\"completion_ratio\":5}"}`, inside.Unix()),
		`{"id":1,"created_at":"2026-09-25 09:00:00","type":2,"model_name":"claude-x","quota":"50000","prompt_tokens":"100000","completion_tokens":null,"other":{"model_ratio":1.5,"group_ratio":1}}`,
	}
	site := newUsageSite(t, items)
	since := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	until := since.Add(24 * time.Hour)

	res, err := site.adapter().UsageLog(context.Background(), site.channel(), UsageLogQuery{Since: since, Until: until})
	if err != nil {
		t.Fatal(err)
	}
	if res.QuotaPerUnit != 500000 {
		t.Fatalf("quota per unit = %d", res.QuotaPerUnit)
	}
	if res.Total != 2 || res.Truncated {
		t.Fatalf("total=%d truncated=%v", res.Total, res.Truncated)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("entries = %+v", res.Entries)
	}
	first := res.Entries[0]
	if !first.CreatedAt.Equal(inside) || first.ModelName != "claude-x" || first.Quota != 250000 ||
		first.PromptTokens != 400000 || first.CompletionTokens != 100000 {
		t.Fatalf("first entry = %+v", first)
	}
	if !first.Multipliers.Known || first.Multipliers.GroupRatio != 1.2 || first.Multipliers.CompletionRatio != 5 {
		t.Fatalf("first multipliers = %+v", first.Multipliers)
	}
	// "2026-09-25 09:00:00" is Beijing time (the billing timezone).
	wantSecond := time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC)
	if !res.Entries[1].CreatedAt.Equal(wantSecond) || !res.Entries[1].Multipliers.Known {
		t.Fatalf("second entry = %+v", res.Entries[1])
	}
	if res.Entries[1].Quota != 50000 || res.Entries[1].PromptTokens != 100000 || res.Entries[1].CompletionTokens != 0 {
		t.Fatalf("string-encoded counters must parse: %+v", res.Entries[1])
	}
	if len(site.queries) != 1 {
		t.Fatalf("queries = %v", site.queries)
	}
	q := site.queries[0]
	for _, want := range []string{"type=2", "start_timestamp=" + strconv.FormatInt(since.Unix(), 10), "end_timestamp=" + strconv.FormatInt(until.Unix(), 10)} {
		if !strings.Contains(q, want) {
			t.Fatalf("query %q is missing %q", q, want)
		}
	}
}

func TestUsageLogPagesAndFlagsTruncation(t *testing.T) {
	items := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		items = append(items, fmt.Sprintf(`{"created_at":%d,"model_name":"m","quota":1000,"prompt_tokens":10}`, 1_700_000_000+i))
	}
	site := newUsageSite(t, items)

	res, err := site.adapter().UsageLog(context.Background(), site.channel(), UsageLogQuery{MaxEntries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Total != 5 {
		t.Fatalf("entries=%d total=%d", len(res.Entries), res.Total)
	}
	if !res.Truncated {
		t.Fatal("a read that leaves entries behind must be flagged as truncated")
	}
	if len(site.queries) != 1 || !strings.Contains(site.queries[0], "page_size=2") {
		t.Fatalf("queries = %v", site.queries)
	}

	// Without a cap the whole log is read across pages.
	site2 := newUsageSite(t, items)
	res2, err := site2.adapter().UsageLog(context.Background(), site2.channel(), UsageLogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Entries) != 5 || res2.Truncated || len(site2.queries) != 1 {
		t.Fatalf("entries=%d truncated=%v queries=%v", len(res2.Entries), res2.Truncated, site2.queries)
	}
}

func TestUsageLogRejectsAnUnreadableLog(t *testing.T) {
	site := newUsageSite(t, nil)
	site.status = http.StatusBadGateway
	if _, err := site.adapter().UsageLog(context.Background(), site.channel(), UsageLogQuery{}); err == nil {
		t.Fatal("a failing log endpoint must be an error, not an empty log")
	}

	broken := newUsageSite(t, nil)
	broken.body = `<html>not json</html>`
	if _, err := broken.adapter().UsageLog(context.Background(), broken.channel(), UsageLogQuery{}); err == nil {
		t.Fatal("an unreadable body must be an error")
	}
}

func TestUsageLogDropsEntriesOutsideTheWindow(t *testing.T) {
	// The site ignored the timestamp filter; entries outside the window must
	// not leak into the comparison, and the read counts as incomplete.
	site := newUsageSite(t, []string{
		`{"created_at":1600000000,"model_name":"old","quota":1000,"prompt_tokens":10}`,
	})
	since := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	res, err := site.adapter().UsageLog(context.Background(), site.channel(), UsageLogQuery{Since: since, Until: since.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 {
		t.Fatalf("entries = %+v", res.Entries)
	}
	if !res.Truncated {
		t.Fatal("entries dropped by the window check must leave the read flagged incomplete")
	}
}

func TestParseLogTimeShapes(t *testing.T) {
	wantUnix := time.Date(2026, 9, 25, 4, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		`1790308800`:             wantUnix,
		`"1790308800"`:           wantUnix,
		`1790308800000`:          wantUnix,
		`"2026-09-25 12:00:00"`:  wantUnix, // Beijing noon is 04:00 UTC
		`"2026-09-25T04:00:00Z"`: wantUnix,
	}
	for raw, want := range cases {
		got, ok := parseLogTime([]byte(raw))
		if !ok || !got.Equal(want) {
			t.Errorf("parseLogTime(%s) = %v, %v; want %v", raw, got, ok, want)
		}
	}
	for _, bad := range []string{``, `null`, `"not a time"`, `{}`} {
		if _, ok := parseLogTime([]byte(bad)); ok {
			t.Errorf("parseLogTime(%s) accepted", bad)
		}
	}
}

func TestParseMultipliersOnlyKnownWhenReported(t *testing.T) {
	if m := parseMultipliers([]byte(`{}`)); m.Known {
		t.Fatal("an empty object is not a ratio report")
	}
	if m := parseMultipliers(nil); m.Known {
		t.Fatal("a missing `other` is not a ratio report")
	}
	m := parseMultipliers([]byte(`"{\"group_ratio\":2}"`))
	if !m.Known || m.GroupRatio != 2 {
		t.Fatalf("string-encoded other: %+v", m)
	}
	if m := parseMultipliers([]byte(`{"group_ratio":1}`)); !m.Known || m.GroupRatio != 1 {
		t.Fatalf("group_ratio 1 is a real report: %+v", m)
	}
}
