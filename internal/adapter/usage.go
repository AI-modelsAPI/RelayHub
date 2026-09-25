package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"relayhub/internal/domain"
)

// Site-side usage logs (AUDIT §5 B2). The reconciliation core needs to know
// what the relay says it charged, not what RelayHub believes it used, so this
// reads the site's own consumption log: new-api family sites expose it as
// GET /api/log/self?type=2 with one entry per billed request.

// UsageLogQuery selects which log entries to read.
type UsageLogQuery struct {
	Since, Until time.Time
	// MaxEntries bounds one read; 0 means the adapter default.
	MaxEntries int
}

// UsageLogEntry is one billed request as the site recorded it.
type UsageLogEntry struct {
	CreatedAt        time.Time
	ModelName        string
	Quota            int64
	PromptTokens     int64
	CompletionTokens int64
	Multipliers      domain.Multipliers
}

// UsageLogResult is one page set plus the site's own accounting unit.
type UsageLogResult struct {
	Entries []UsageLogEntry
	// QuotaPerUnit converts native quota to USD for this site.
	QuotaPerUnit int64
	// Total is how many entries the site says match the query.
	Total int
	// Truncated is set when entries were left behind (the window is not
	// fully covered, so it must not be judged).
	Truncated bool
}

// UsageReporter is implemented by adapters that can read the site's billing
// log. It mirrors CheckinVerifier: optional per adapter, so declarative and
// non-new-api adapters simply do not implement it.
type UsageReporter interface {
	UsageLog(ctx context.Context, channel domain.Channel, q UsageLogQuery) (UsageLogResult, error)
}

const (
	usageLogPageSize    = 100
	defaultUsageEntries = 1000
)

// UsageLog reads the site's consumption log for the query window, paging until
// MaxEntries entries are collected. The returned QuotaPerUnit comes from
// /api/status (DefaultQuotaPerUnit when the site does not report one).
func (b *BaseNewAPIAdapter) UsageLog(ctx context.Context, channel domain.Channel, q UsageLogQuery) (UsageLogResult, error) {
	unit := b.FetchStatusQuotaPerUnit(ctx, channel)
	if unit <= 0 {
		unit = DefaultQuotaPerUnit
	}
	max := q.MaxEntries
	if max <= 0 {
		max = defaultUsageEntries
	}
	out := UsageLogResult{QuotaPerUnit: unit}
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		want := max - len(out.Entries)
		if want > usageLogPageSize {
			want = usageLogPageSize
		}
		if want <= 0 {
			out.Truncated = true
			break
		}
		code, body, err := b.CallWithAuth(ctx, channel, http.MethodGet, usageLogPath(q, page, want), nil)
		if err != nil {
			return out, err
		}
		if code != http.StatusOK {
			return out, fmt.Errorf("usage log: site returned HTTP %d", code)
		}
		items, total, err := parseUsageLogPage(body)
		if err != nil {
			return out, err
		}
		if page == 1 {
			out.Total = total
		}
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			if entry, ok := it.entry(q.Since, q.Until); ok {
				out.Entries = append(out.Entries, entry)
			}
		}
		if len(out.Entries) >= max {
			// Either the site had more than we asked for, or the window
			// filter dropped entries that were fetched.
			out.Truncated = out.Total > len(out.Entries) || len(items) >= want
			break
		}
		if len(items) < want {
			// Short page: the site has nothing more to give.
			out.Truncated = out.Total > len(out.Entries)
			break
		}
	}
	return out, nil
}

// usageLogPath builds the paged log query. Both `p` (current new-api) and
// `page` (older forks) are sent so every variant pages correctly, and the
// window is filtered server-side where the site supports it.
func usageLogPath(q UsageLogQuery, page, size int) string {
	var sb strings.Builder
	sb.WriteString("/api/log/self?type=2")
	sb.WriteString("&p=" + strconv.Itoa(page))
	sb.WriteString("&page=" + strconv.Itoa(page))
	sb.WriteString("&page_size=" + strconv.Itoa(size))
	if !q.Since.IsZero() {
		sb.WriteString("&start_timestamp=" + strconv.FormatInt(q.Since.Unix(), 10))
	}
	if !q.Until.IsZero() {
		sb.WriteString("&end_timestamp=" + strconv.FormatInt(q.Until.Unix(), 10))
	}
	return sb.String()
}

// usageLogItem mirrors one site log row. Numeric fields use flexNumber and
// timestamps stay raw because forks disagree on integer vs float, on
// string-encoded numbers and on whether created_at is a Unix timestamp or a
// formatted string.
type usageLogItem struct {
	CreatedAt        json.RawMessage `json:"created_at"`
	Time             json.RawMessage `json:"time"`
	ModelName        string          `json:"model_name"`
	Quota            flexNumber      `json:"quota"`
	PromptTokens     flexNumber      `json:"prompt_tokens"`
	CompletionTokens flexNumber      `json:"completion_tokens"`
	Other            json.RawMessage `json:"other"`
}

// flexNumber decodes a field that may arrive as a number or as a string, and
// never fails: a single odd field must not make the whole log unreadable.
type flexNumber float64

func (n *flexNumber) UnmarshalJSON(b []byte) error {
	text := strings.TrimSpace(string(b))
	if text == "" || text == "null" {
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return nil
		}
		text = strings.TrimSpace(s)
		if text == "" {
			return nil
		}
	}
	if f, err := strconv.ParseFloat(text, 64); err == nil {
		*n = flexNumber(f)
	}
	return nil
}

func (it usageLogItem) entry(since, until time.Time) (UsageLogEntry, bool) {
	created, ok := parseLogTime(it.CreatedAt)
	if !ok {
		created, _ = parseLogTime(it.Time)
	}
	// The site may ignore the timestamp filter; drop anything outside the
	// window rather than mixing windows together.
	if !created.IsZero() && !since.IsZero() && created.Before(since) {
		return UsageLogEntry{}, false
	}
	if !created.IsZero() && !until.IsZero() && created.After(until) {
		return UsageLogEntry{}, false
	}
	return UsageLogEntry{
		CreatedAt:        created,
		ModelName:        strings.TrimSpace(it.ModelName),
		Quota:            int64(math.Round(float64(it.Quota))),
		PromptTokens:     int64(math.Round(float64(it.PromptTokens))),
		CompletionTokens: int64(math.Round(float64(it.CompletionTokens))),
		Multipliers:      parseMultipliers(it.Other),
	}, true
}

func parseUsageLogPage(body []byte) ([]usageLogItem, int, error) {
	var res struct {
		Success bool `json:"success"`
		Data    struct {
			Items []usageLogItem `json:"items"`
			Total int            `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, 0, fmt.Errorf("usage log: unreadable response: %w", err)
	}
	return res.Data.Items, res.Data.Total, nil
}

// parseLogTime accepts every timestamp shape seen in the wild: Unix seconds or
// milliseconds (number or string) and the formatted variants forks return.
func parseLogTime(raw json.RawMessage) (time.Time, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return time.Time{}, false
	}
	if strings.HasPrefix(text, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return time.Time{}, false
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return time.Time{}, false
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return fromUnix(n), true
		}
		for _, layout := range []string{
			"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00",
			"2006-01-02T15:04:05", "2006/01/02 15:04:05", time.RFC3339Nano,
		} {
			if t, err := time.ParseInLocation(layout, s, cstZone); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return time.Time{}, false
	}
	return fromUnix(int64(math.Round(n))), true
}

// fromUnix normalises seconds and milliseconds.
func fromUnix(n int64) time.Time {
	if n > 1_000_000_000_000 { // milliseconds
		return time.UnixMilli(n).UTC()
	}
	return time.Unix(n, 0).UTC()
}

// parseMultipliers reads the site's `other` field: a JSON object, sometimes
// wrapped in a string, holding the ratios it billed with. Unknown means the
// site reported none (older builds), never "no multiplier".
func parseMultipliers(raw json.RawMessage) domain.Multipliers {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return domain.Multipliers{}
	}
	if strings.HasPrefix(text, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return domain.Multipliers{}
		}
		text = strings.TrimSpace(s)
		if text == "" {
			return domain.Multipliers{}
		}
	}
	// Presence, not value, decides Known: a site that reports group_ratio 1
	// is telling us something real, a site that reports nothing is not.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &probe); err != nil || len(probe) == 0 {
		return domain.Multipliers{}
	}
	m := domain.Multipliers{Known: true}
	ratio := func(key string, dst *float64) {
		raw, ok := probe[key]
		if !ok {
			return
		}
		var f float64
		if err := json.Unmarshal(raw, &f); err == nil {
			*dst = f
		}
	}
	ratio("model_ratio", &m.ModelRatio)
	ratio("group_ratio", &m.GroupRatio)
	ratio("completion_ratio", &m.CompletionRatio)
	ratio("model_price", &m.ModelPrice)
	ratio("user_group_ratio", &m.UserGroupRatio)
	return m
}
