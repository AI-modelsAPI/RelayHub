package verify

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Authenticity probes (AUDIT §5 B1). The gateway sends small canary requests
// through a channel exactly like client traffic (same credential, egress and
// identity headers) and hands the raw observation to RecordProbe, which turns
// it into a verdict:
//
//   - canary: the model must repeat a random code. A relay that answers
//     with canned text, a cached reply or a model too weak to follow a
//     one-line instruction fails here.
//   - model: the response's "model" field against the requested model
//     (family / tier / version, see CompareModelNames).
//   - tools: a model declared tool-capable must honour a forced tool call;
//     relays that silently drop tools break every agent client.
//   - fingerprint: the reported prompt size of the fixed-shape canary is
//     a tokenizer + template fingerprint. It must stay stable per channel
//     (drift = backend swapped or a system prompt injected) and agree with
//     other channels serving the same model (outlier).
//
// A run whose canary got no 2xx answer is inconclusive: it neither clears
// nor condemns the channel. Verdicts live in memory and stop steering
// routing after ProbeTTL.

// Probe signals.
const (
	SignalCanaryFailed       = "canary_failed"
	SignalModelMismatch      = "model_mismatch"
	SignalModelRenamed       = "model_renamed" // informational, no penalty
	SignalToolsMissing       = "tools_missing"
	SignalFingerprintOutlier = "fingerprint_outlier"
	SignalFingerprintDrift   = "fingerprint_drift"
)

// Score penalties per signal.
const (
	penaltyCanaryFailed = 60
	penaltyModel        = 40
	penaltyTools        = 40
	penaltyOutlier      = 30
	penaltyDrift        = 25
)

// Check statuses.
const (
	CheckPass  = "pass"
	CheckFail  = "fail"
	CheckError = "error" // could not be evaluated: transport error, non-2xx, truncated
	CheckSkip  = "skip"
)

// SuspectBelow is the probe score under which routing demotes a channel: a
// failed canary, a substituted model or missing tool support each suffice;
// a fingerprint anomaly alone does not.
const SuspectBelow = 70

// ProbeTTL bounds how long a verdict steers routing without a fresh probe.
const ProbeTTL = 48 * time.Hour

// ErrNoProbeTarget reports a channel/model pair that no binding serves.
var ErrNoProbeTarget = errors.New("no probe target")

// ProbeOutcome is the raw observation of one probe run.
type ProbeOutcome struct {
	ChannelID     string
	ModelID       string // logical model
	UpstreamModel string // model name sent upstream
	Protocol      string // openai-chat | anthropic-messages
	At            time.Time
	Latency       time.Duration // canary round trip

	// Canary: "reply with exactly these words".
	CanaryStatus    int    // HTTP status; 0 when no response arrived
	CanaryError     string // transport error, upstream error or unparseable 2xx body
	CanaryText      string // assistant text (clipped)
	CanaryEchoed    bool   // the text contains the code
	CanaryTruncated bool   // stopped by the token limit
	ReportedModel   string // response "model" field
	PromptTokens    int    // reported prompt size; 0 = not reported

	// Tools: a forced call of the probe tool (declared tool-capable models only).
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
	// Conclusive is false when the canary got no usable 2xx answer; Score
	// is meaningful only for conclusive results.
	Conclusive    bool     `json:"conclusive"`
	Score         int      `json:"score"`
	Signals       []string `json:"signals"`
	Checks        []Check  `json:"checks"`
	ReportedModel string   `json:"reported_model,omitempty"`
	PromptTokens  int      `json:"prompt_tokens,omitempty"`
	LatencyMS     int64    `json:"latency_ms"`
}

// SetClock replaces the registry clock (tests).
func (r *Registry) SetClock(now func() time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
}

// RecordProbe evaluates a probe outcome, stores the verdict and returns it.
func (r *Registry) RecordProbe(o ProbeOutcome) ProbeResult {
	if r == nil {
		return ProbeResult{ChannelID: o.ChannelID, ModelID: o.ModelID, Score: 100}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureLocked()
	res := r.evaluateLocked(o)
	r.lastProbe[o.ChannelID] = res
	if res.Conclusive {
		byModel := r.probes[o.ChannelID]
		if byModel == nil {
			byModel = map[string]ProbeResult{}
			r.probes[o.ChannelID] = byModel
		}
		byModel[o.ModelID] = res
	}
	return res
}

// Suspect reports whether a fresh, conclusive probe verdict demotes the
// channel, with a reason fit for route explain.
func (r *Registry) Suspect(channelID string) (bool, string) {
	if r == nil {
		return false, ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clockLocked()
	var worst *ProbeResult
	for _, id := range sortedKeys(r.probes[channelID]) {
		p := r.probes[channelID][id]
		if now.Sub(p.CheckedAt) > ProbeTTL || p.Score >= SuspectBelow {
			continue
		}
		if worst == nil || p.Score < worst.Score {
			pc := p
			worst = &pc
		}
	}
	if worst == nil {
		return false, ""
	}
	return true, fmt.Sprintf("authenticity probe on %s: %s (score %d)", worst.ModelID, strings.Join(penaltySignals(worst.Signals), ", "), worst.Score)
}

func (r *Registry) evaluateLocked(o ProbeOutcome) ProbeResult {
	at := o.At
	if at.IsZero() {
		at = r.clockLocked()
	}
	res := ProbeResult{
		ChannelID:     o.ChannelID,
		ModelID:       o.ModelID,
		UpstreamModel: o.UpstreamModel,
		Protocol:      o.Protocol,
		CheckedAt:     at,
		Score:         100,
		Signals:       []string{},
		ReportedModel: o.ReportedModel,
		PromptTokens:  o.PromptTokens,
		LatencyMS:     o.Latency.Milliseconds(),
	}
	add := func(name, status, detail string) {
		res.Checks = append(res.Checks, Check{Name: name, Status: status, Detail: detail})
	}
	penalize := func(signal string, points int) {
		res.Score -= points
		res.Signals = append(res.Signals, signal)
	}

	// Canary. Only a parseable 2xx answer makes the run conclusive.
	if o.CanaryStatus < 200 || o.CanaryStatus > 299 || o.CanaryError != "" {
		detail := o.CanaryError
		if detail == "" && o.CanaryStatus != 0 {
			detail = fmt.Sprintf("HTTP %d", o.CanaryStatus)
		}
		if detail == "" {
			detail = "no response"
		}
		add("canary", CheckError, detail)
		return res
	}
	res.Conclusive = true
	switch {
	case o.CanaryEchoed:
		add("canary", CheckPass, "repeated the code")
	case o.CanaryTruncated:
		add("canary", CheckError, "answer cut off by the token limit")
	default:
		add("canary", CheckFail, "did not repeat the code; replied "+quoteClip(o.CanaryText, 80))
		penalize(SignalCanaryFailed, penaltyCanaryFailed)
	}

	// Model field.
	switch {
	case o.ReportedModel == "":
		add("model", CheckSkip, "response carries no model field")
	default:
		switch CompareModelNames(o.UpstreamModel, o.ReportedModel) {
		case ModelDifferent:
			add("model", CheckFail, fmt.Sprintf("requested %s, response says %s", o.UpstreamModel, o.ReportedModel))
			penalize(SignalModelMismatch, penaltyModel)
		case ModelRenamed:
			add("model", CheckPass, fmt.Sprintf("requested %s, response says %s (renamed)", o.UpstreamModel, o.ReportedModel))
			res.Signals = append(res.Signals, SignalModelRenamed)
		default:
			add("model", CheckPass, o.ReportedModel)
		}
	}

	// Tools.
	switch {
	case !o.ToolsChecked:
		add("tools", CheckSkip, "model does not declare tool support")
	case o.ToolsStatus < 200 || o.ToolsStatus > 299 || o.ToolsError != "":
		detail := o.ToolsError
		if detail == "" {
			detail = fmt.Sprintf("HTTP %d", o.ToolsStatus)
		}
		add("tools", CheckError, detail)
	case o.ToolsCalled:
		add("tools", CheckPass, "forced tool call returned")
	case o.ToolsTruncated:
		add("tools", CheckError, "answer cut off by the token limit")
	default:
		add("tools", CheckFail, "a forced tool call came back as plain text")
		penalize(SignalToolsMissing, penaltyTools)
	}

	r.fingerprintLocked(o, at, add, penalize)
	if res.Score < 0 {
		res.Score = 0
	}
	return res
}

// fingerprintLocked compares the reported prompt size with this channel's
// first observation and with other channels serving the same model.
func (r *Registry) fingerprintLocked(o ProbeOutcome, at time.Time, add func(name, status, detail string), penalize func(string, int)) {
	pt := o.PromptTokens
	if pt <= 0 {
		add("fingerprint", CheckSkip, "no prompt token count reported")
		return
	}
	key := o.ChannelID + "|" + o.Protocol + "|" + strings.ToLower(o.UpstreamModel)
	switch base, ok := r.baselines[key]; {
	case !ok:
		r.baselines[key] = pt
		add("fingerprint", CheckPass, fmt.Sprintf("prompt tokens %d (baseline recorded)", pt))
	case tokensAgree(base, pt):
		add("fingerprint", CheckPass, fmt.Sprintf("prompt tokens %d, stable", pt))
	default:
		add("fingerprint", CheckFail, fmt.Sprintf("prompt tokens %d, first seen %d", pt, base))
		penalize(SignalFingerprintDrift, penaltyDrift)
	}

	var peers []int
	for _, ch := range sortedKeys(r.probes) {
		if ch == o.ChannelID {
			continue
		}
		p, ok := r.probes[ch][o.ModelID]
		if !ok || p.Protocol != o.Protocol || p.PromptTokens <= 0 || at.Sub(p.CheckedAt) > ProbeTTL {
			continue
		}
		peers = append(peers, p.PromptTokens)
	}
	consensus, n, ok := consensusOf(peers)
	switch {
	case !ok:
		return
	case tokensAgree(consensus, pt):
		add("fingerprint_peers", CheckPass, fmt.Sprintf("matches %d other channels (%d)", n, consensus))
	default:
		add("fingerprint_peers", CheckFail, fmt.Sprintf("prompt tokens %d, %d other channels report %d", pt, n, consensus))
		penalize(SignalFingerprintOutlier, penaltyOutlier)
	}
}

// tokensAgree tolerates template noise: 2 tokens or 5%, whichever is larger.
func tokensAgree(a, b int) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= max(2, max(a, b)/20)
}

// consensusOf returns the median of values when a strict majority (and at
// least two) agree with it.
func consensusOf(values []int) (median, agreeing int, ok bool) {
	if len(values) < 2 {
		return 0, 0, false
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	median = sorted[len(sorted)/2]
	for _, v := range sorted {
		if tokensAgree(median, v) {
			agreeing++
		}
	}
	if agreeing < 2 || agreeing*2 <= len(sorted) {
		return 0, 0, false
	}
	return median, agreeing, true
}

// penaltySignals drops informational signals from a reason string.
func penaltySignals(signals []string) []string {
	out := make([]string, 0, len(signals))
	for _, s := range signals {
		if s != SignalModelRenamed {
			out = append(out, s)
		}
	}
	return out
}

func quoteClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "nothing"
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return fmt.Sprintf("%q", s)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
