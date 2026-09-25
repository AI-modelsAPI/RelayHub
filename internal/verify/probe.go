package verify

import (
	"errors"
	"time"
)

// Probe signals.
const (
	SignalCanaryFailed       = "canary_failed"
	SignalModelMismatch      = "model_mismatch"
	SignalModelRenamed       = "model_renamed"
	SignalToolsMissing       = "tools_missing"
	SignalFingerprintOutlier = "fingerprint_outlier"
	SignalFingerprintDrift   = "fingerprint_drift"
)

// Check statuses.
const (
	CheckPass  = "pass"
	CheckFail  = "fail"
	CheckError = "error"
	CheckSkip  = "skip"
)

// SuspectBelow is the probe score under which routing demotes a channel.
const SuspectBelow = 70

// ProbeTTL bounds how long a verdict steers routing without a fresh probe.
const ProbeTTL = 48 * time.Hour

// ErrNoProbeTarget reports a channel/model pair that no binding serves.
var ErrNoProbeTarget = errors.New("no probe target")

// ProbeOutcome is the raw observation of one probe run.
type ProbeOutcome struct {
	ChannelID     string
	ModelID       string
	UpstreamModel string
	Protocol      string
	At            time.Time
	Latency       time.Duration

	CanaryStatus    int
	CanaryError     string
	CanaryText      string
	CanaryEchoed    bool
	CanaryTruncated bool
	ReportedModel   string
	PromptTokens    int

	ToolsChecked   bool
	ToolsStatus    int
	ToolsError     string
	ToolsCalled    bool
	ToolsTruncated bool
}

// Check is one evaluated signal of a probe.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// ProbeResult is the verdict of one probe run.
type ProbeResult struct {
	ChannelID     string    `json:"channel_id"`
	ModelID       string    `json:"model_id"`
	UpstreamModel string    `json:"upstream_model"`
	Protocol      string    `json:"protocol"`
	CheckedAt     time.Time `json:"checked_at"`
	Conclusive    bool      `json:"conclusive"`
	Score         int       `json:"score"`
	Signals       []string  `json:"signals"`
	Checks        []Check   `json:"checks"`
	ReportedModel string    `json:"reported_model,omitempty"`
	PromptTokens  int       `json:"prompt_tokens,omitempty"`
	LatencyMS     int64     `json:"latency_ms"`
}

// SetClock replaces the registry clock (tests).
func (r *Registry) SetClock(now func() time.Time) {}

// RecordProbe evaluates and stores a probe outcome. Not implemented yet.
func (r *Registry) RecordProbe(o ProbeOutcome) ProbeResult {
	return ProbeResult{ChannelID: o.ChannelID}
}

// Suspect reports whether routing should demote the channel. Not implemented yet.
func (r *Registry) Suspect(channelID string) (bool, string) { return false, "" }
