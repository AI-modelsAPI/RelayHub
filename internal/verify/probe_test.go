package verify

import (
	"strings"
	"testing"
	"time"

	"relayhub/internal/domain"
)

var probeT0 = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func probeRegistry() *Registry {
	r := New()
	r.SetClock(func() time.Time { return probeT0 })
	return r
}

// honestOutcome is a canary answered correctly by the requested model.
func honestOutcome(channel string) ProbeOutcome {
	return ProbeOutcome{
		ChannelID:     channel,
		ModelID:       "sonnet",
		UpstreamModel: "claude-sonnet-4-5",
		Protocol:      "anthropic-messages",
		At:            probeT0,
		Latency:       800 * time.Millisecond,
		CanaryStatus:  200,
		CanaryText:    "apple river stone cloud",
		CanaryEchoed:  true,
		ReportedModel: "claude-sonnet-4-5-20250929",
		PromptTokens:  41,
	}
}

func checkNamed(t *testing.T, r ProbeResult, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("probe result has no %q check: %+v", name, r.Checks)
	return Check{}
}

func hasSignal(signals []string, want string) bool {
	for _, s := range signals {
		if s == want {
			return true
		}
	}
	return false
}

func TestProbeHonestChannelKeepsFullScore(t *testing.T) {
	r := probeRegistry()
	res := r.RecordProbe(honestOutcome("c1"))
	if !res.Conclusive || res.Score != 100 || len(res.Signals) != 0 {
		t.Fatalf("honest probe: %+v", res)
	}
	if res.ChannelID != "c1" || res.ModelID != "sonnet" || res.PromptTokens != 41 || res.LatencyMS != 800 || !res.CheckedAt.Equal(probeT0) {
		t.Fatalf("probe metadata not carried over: %+v", res)
	}
	for _, name := range []string{"canary", "model", "fingerprint"} {
		if c := checkNamed(t, res, name); c.Status != CheckPass {
			t.Errorf("%s check: %+v", name, c)
		}
	}
	if c := checkNamed(t, res, "tools"); c.Status != CheckSkip {
		t.Errorf("tools were not probed, check should be skipped: %+v", c)
	}
	if bad, why := r.Suspect("c1"); bad {
		t.Fatalf("honest channel is suspect: %s", why)
	}
	s := r.Get("c1")
	if s.Score != 100 || s.Suspect || len(s.Probes) != 1 || s.LastProbe == nil || !s.LastProbe.Conclusive {
		t.Fatalf("score after honest probe: %+v", s)
	}
}

func TestProbeCanaryFailureMakesChannelSuspect(t *testing.T) {
	r := probeRegistry()
	o := honestOutcome("c1")
	o.CanaryEchoed = false
	o.CanaryText = "Hello! How can I help you today?"
	res := r.RecordProbe(o)
	if !res.Conclusive || res.Score != 40 || !hasSignal(res.Signals, SignalCanaryFailed) {
		t.Fatalf("canary failure: %+v", res)
	}
	if c := checkNamed(t, res, "canary"); c.Status != CheckFail || !strings.Contains(c.Detail, "How can I help") {
		t.Fatalf("canary check should quote the reply: %+v", c)
	}
	bad, why := r.Suspect("c1")
	if !bad || !strings.Contains(why, SignalCanaryFailed) || !strings.Contains(why, "sonnet") {
		t.Fatalf("Suspect = %v, %q", bad, why)
	}
	if s := r.Get("c1"); s.Score != 40 || !s.Suspect || !hasSignal(s.Signals, SignalCanaryFailed) {
		t.Fatalf("combined score should carry the probe verdict: %+v", s)
	}
}

func TestProbeModelFieldMismatch(t *testing.T) {
	r := probeRegistry()
	o := honestOutcome("c1")
	o.ReportedModel = "glm-4.6"
	res := r.RecordProbe(o)
	if res.Score != 60 || !hasSignal(res.Signals, SignalModelMismatch) {
		t.Fatalf("substituted model: %+v", res)
	}
	if c := checkNamed(t, res, "model"); c.Status != CheckFail || !strings.Contains(c.Detail, "glm-4.6") || !strings.Contains(c.Detail, "claude-sonnet-4-5") {
		t.Fatalf("model check detail: %+v", c)
	}
	if bad, _ := r.Suspect("c1"); !bad {
		t.Fatal("a channel answering with another model must be suspect")
	}

	// A different spelling of the same model is reported but not penalised.
	o = honestOutcome("c2")
	o.ReportedModel = "deepseek-v3.1"
	o.UpstreamModel = "deepseek-chat"
	res = r.RecordProbe(o)
	if res.Score != 100 || !hasSignal(res.Signals, SignalModelRenamed) {
		t.Fatalf("renamed model: %+v", res)
	}
	if bad, _ := r.Suspect("c2"); bad {
		t.Fatal("a renamed model must not demote the channel")
	}

	// No model field at all: nothing to compare.
	o = honestOutcome("c3")
	o.ReportedModel = ""
	res = r.RecordProbe(o)
	if c := checkNamed(t, res, "model"); c.Status != CheckSkip || res.Score != 100 {
		t.Fatalf("missing model field: %+v", res)
	}
}

func TestProbeTransportFailureIsInconclusive(t *testing.T) {
	r := probeRegistry()
	o := honestOutcome("c1")
	o.CanaryStatus, o.CanaryEchoed, o.ReportedModel, o.PromptTokens = 502, false, "", 0
	o.CanaryError = "HTTP 502: bad gateway"
	res := r.RecordProbe(o)
	if res.Conclusive || len(res.Signals) != 0 {
		t.Fatalf("a 502 says nothing about authenticity: %+v", res)
	}
	if c := checkNamed(t, res, "canary"); c.Status != CheckError || !strings.Contains(c.Detail, "502") {
		t.Fatalf("canary check: %+v", c)
	}
	s := r.Get("c1")
	if s.Suspect || s.Score != 100 || len(s.Probes) != 0 || s.LastProbe == nil || s.LastProbe.Conclusive {
		t.Fatalf("inconclusive probe must be visible but not scored: %+v", s)
	}

	// An inconclusive run neither clears nor condemns an earlier verdict.
	bad := honestOutcome("c2")
	bad.CanaryEchoed, bad.CanaryText = false, "nope"
	r.RecordProbe(bad)
	r.RecordProbe(ProbeOutcome{ChannelID: "c2", ModelID: "sonnet", Protocol: "anthropic-messages", At: probeT0, CanaryError: "dial tcp: i/o timeout"})
	if suspect, _ := r.Suspect("c2"); !suspect {
		t.Fatal("a timeout must not clear a failed canary")
	}
	if s := r.Get("c2"); len(s.Probes) != 1 || !s.Probes[0].Conclusive || s.LastProbe == nil || s.LastProbe.Conclusive {
		t.Fatalf("verdict and last attempt: %+v", s)
	}
}

func TestProbeTruncatedCanaryIsNotAFailure(t *testing.T) {
	r := probeRegistry()
	o := honestOutcome("c1")
	o.CanaryEchoed, o.CanaryText, o.CanaryTruncated = false, "", true
	res := r.RecordProbe(o)
	if !res.Conclusive || res.Score != 100 || hasSignal(res.Signals, SignalCanaryFailed) {
		t.Fatalf("a reply cut by the token limit is not a wrong answer: %+v", res)
	}
	if c := checkNamed(t, res, "canary"); c.Status != CheckError {
		t.Fatalf("canary check: %+v", c)
	}
	// The model field and fingerprint of the 2xx reply are still evaluated.
	if c := checkNamed(t, res, "model"); c.Status != CheckPass {
		t.Fatalf("model check: %+v", c)
	}
}

func TestProbeToolsMissing(t *testing.T) {
	r := probeRegistry()
	o := honestOutcome("c1")
	o.ToolsChecked, o.ToolsStatus, o.ToolsCalled = true, 200, false
	res := r.RecordProbe(o)
	if res.Score != 60 || !hasSignal(res.Signals, SignalToolsMissing) {
		t.Fatalf("plain text instead of a forced tool call: %+v", res)
	}
	if c := checkNamed(t, res, "tools"); c.Status != CheckFail {
		t.Fatalf("tools check: %+v", c)
	}

	o = honestOutcome("c2")
	o.ToolsChecked, o.ToolsStatus, o.ToolsCalled = true, 200, true
	if res := r.RecordProbe(o); res.Score != 100 || checkNamed(t, res, "tools").Status != CheckPass {
		t.Fatalf("tool call returned: %+v", res)
	}

	// A relay that rejects the tool request is inconclusive for tools.
	o = honestOutcome("c3")
	o.ToolsChecked, o.ToolsStatus, o.ToolsError = true, 400, "HTTP 400: tool_choice not supported"
	if res := r.RecordProbe(o); res.Score != 100 || checkNamed(t, res, "tools").Status != CheckError {
		t.Fatalf("rejected tool request: %+v", res)
	}
}

func TestProbeFingerprintDrift(t *testing.T) {
	r := probeRegistry()
	r.RecordProbe(honestOutcome("c1"))
	o := honestOutcome("c1")
	o.PromptTokens = 42 // within tolerance
	if res := r.RecordProbe(o); res.Score != 100 {
		t.Fatalf("one token of noise is not drift: %+v", res)
	}
	o.PromptTokens = 1260 // a relay started injecting a system prompt, or swapped the model
	res := r.RecordProbe(o)
	if res.Score != 75 || !hasSignal(res.Signals, SignalFingerprintDrift) {
		t.Fatalf("drift: %+v", res)
	}
	if c := checkNamed(t, res, "fingerprint"); c.Status != CheckFail || !strings.Contains(c.Detail, "1260") || !strings.Contains(c.Detail, "41") {
		t.Fatalf("fingerprint detail: %+v", c)
	}
	if bad, _ := r.Suspect("c1"); bad {
		t.Fatal("drift alone must not demote a channel")
	}
}

func TestProbeFingerprintOutlierAmongPeers(t *testing.T) {
	r := probeRegistry()
	r.RecordProbe(honestOutcome("a"))
	r.RecordProbe(honestOutcome("b"))
	o := honestOutcome("c")
	o.PromptTokens = 58
	res := r.RecordProbe(o)
	if res.Score != 70 || !hasSignal(res.Signals, SignalFingerprintOutlier) {
		t.Fatalf("outlier: %+v", res)
	}
	if c := checkNamed(t, res, "fingerprint_peers"); c.Status != CheckFail || !strings.Contains(c.Detail, "58") || !strings.Contains(c.Detail, "41") {
		t.Fatalf("peer check: %+v", c)
	}
	// A channel that agrees with its peers passes the peer check.
	if res := r.RecordProbe(honestOutcome("d")); res.Score != 100 || checkNamed(t, res, "fingerprint_peers").Status != CheckPass {
		t.Fatalf("agreeing channel: %+v", res)
	}
	// Peers are only comparable over the same protocol.
	o = honestOutcome("e")
	o.Protocol, o.PromptTokens = "openai-chat", 58
	if res := r.RecordProbe(o); res.Score != 100 {
		t.Fatalf("different protocol must not be compared: %+v", res)
	}
}

func TestProbeVerdictExpires(t *testing.T) {
	now := probeT0
	r := New()
	r.SetClock(func() time.Time { return now })
	o := honestOutcome("c1")
	o.CanaryEchoed, o.CanaryText = false, "nope"
	r.RecordProbe(o)
	if bad, _ := r.Suspect("c1"); !bad {
		t.Fatal("expected suspect")
	}
	now = probeT0.Add(ProbeTTL + time.Minute)
	if bad, _ := r.Suspect("c1"); bad {
		t.Fatal("a stale verdict must stop steering routing")
	}
	if s := r.Get("c1"); s.Suspect || s.Score != 100 || len(s.Probes) != 1 {
		t.Fatalf("stale verdict stays visible but unscored: %+v", s)
	}
}

func TestCombinedScoreTakesTheWorse(t *testing.T) {
	r := probeRegistry()
	passive := r.Observe(domain.RequestRecord{ChannelID: "c1", StatusCode: 502})
	r.RecordProbe(honestOutcome("c1"))
	s := r.Get("c1")
	if s.Score != passive.Score || s.Passive != passive.Score {
		t.Fatalf("honest probe must not hide passive penalties: passive=%d got %+v", passive.Score, s)
	}
	o := honestOutcome("c1")
	o.ReportedModel = "gpt-4o-mini"
	r.RecordProbe(o)
	if s := r.Get("c1"); s.Score != 60 || s.Passive != passive.Score {
		t.Fatalf("combined score: %+v", s)
	}
}

func TestAllListsProbedChannels(t *testing.T) {
	r := probeRegistry()
	r.Observe(domain.RequestRecord{ChannelID: "b", StatusCode: 200})
	r.RecordProbe(honestOutcome("a"))
	all := r.All()
	if len(all) != 2 || all[0].ChannelID != "a" || all[1].ChannelID != "b" {
		t.Fatalf("All() = %+v", all)
	}
	if len(all[0].Probes) != 1 {
		t.Fatalf("probe-only channel lost its probe: %+v", all[0])
	}
}
