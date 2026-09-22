package domain

import (
	"encoding/json"
	"strings"
	"time"
)

// QuotaSnapshot is the structured form of Channel.QuotaState. USD is the
// cross-site comparable unit (new-api family: quota / quota_per_unit); Remaining
// keeps the site's native unit for display. Source records who observed it
// ("balance" poll or "checkin" follow-up) so operators can tell how fresh and
// how trustworthy a number is.
type QuotaSnapshot struct {
	AvailableUSD float64    `json:"available_usd"`
	UsedUSD      float64    `json:"used_usd,omitempty"`
	TodayUsedUSD float64    `json:"today_used_usd,omitempty"`
	Remaining    int64      `json:"remaining"`
	Total        int64      `json:"total,omitempty"`
	ResetAt      *time.Time `json:"reset_at,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Source       string     `json:"source,omitempty"`
	Username     string     `json:"username,omitempty"`
}

// Encode serialises the snapshot for Channel.QuotaState.
func (q QuotaSnapshot) Encode() string {
	b, err := json.Marshal(q)
	if err != nil {
		return ""
	}
	return string(b)
}

// ParseQuotaSnapshot decodes Channel.QuotaState. Legacy free-form strings
// (pre-structured releases stored arbitrary text) are tolerated and reported
// as not-a-snapshot rather than as an error.
func ParseQuotaSnapshot(s string) (QuotaSnapshot, bool) {
	s = strings.TrimSpace(s)
	if s == "" || !strings.HasPrefix(s, "{") {
		return QuotaSnapshot{}, false
	}
	var q QuotaSnapshot
	if err := json.Unmarshal([]byte(s), &q); err != nil {
		return QuotaSnapshot{}, false
	}
	if q.UpdatedAt.IsZero() {
		return QuotaSnapshot{}, false
	}
	return q, true
}

// Known reports whether the snapshot carries a usable balance figure.
func (q QuotaSnapshot) Known() bool {
	return !q.UpdatedAt.IsZero()
}
