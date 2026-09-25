package router

import (
	"context"
	"testing"

	"relayhub/internal/domain"
)

// fakeTrust marks the listed channels suspect with the given reason.
type fakeTrust map[string]string

func (f fakeTrust) Suspect(channelID string) (bool, string) {
	why, ok := f[channelID]
	return ok, why
}

// trustFixture: "fake" is preferred by priority, "good" is the fallback.
func trustFixture() (map[string]domain.Model, map[string]domain.Channel, []domain.ProviderModel) {
	models := map[string]domain.Model{"m": {ID: "m", Enabled: true}}
	channels := map[string]domain.Channel{
		"fake": {ID: "fake", ProviderID: "p", Enabled: true, RoutingEnabled: true},
		"good": {ID: "good", ProviderID: "p", Enabled: true, RoutingEnabled: true},
	}
	pms := []domain.ProviderModel{
		{ID: "pm-fake", ProviderID: "p", ChannelID: "fake", ModelID: "m", Priority: 0, Enabled: true},
		{ID: "pm-good", ProviderID: "p", ChannelID: "good", ModelID: "m", Priority: 1, Enabled: true},
	}
	return models, channels, pms
}

func TestSuspectChannelIsDemoted(t *testing.T) {
	models, channels, pms := trustFixture()
	r := Resolver{Models: models, Channels: channels, ProviderModels: pms}

	d, err := r.Resolve(context.Background(), Request{Model: "m"})
	if err != nil || d.Channel.ID != "fake" {
		t.Fatalf("without a trust source priority wins: %v %+v", err, d.Channel)
	}

	r.Trust = fakeTrust{"fake": "authenticity probe on m: canary_failed"}
	d, err = r.Resolve(context.Background(), Request{Model: "m"})
	if err != nil || d.Channel.ID != "good" {
		t.Fatalf("a suspect channel must yield to a trusted one: %v %+v", err, d.Channel)
	}
	if why := d.Excluded["pm-fake/fake"]; why != "authenticity probe on m: canary_failed" {
		t.Fatalf("route explain must say why the channel was passed over: %q (%v)", why, d.Excluded)
	}
}

func TestSuspectChannelStillServesWhenNothingElseCan(t *testing.T) {
	models, channels, pms := trustFixture()
	r := Resolver{Models: models, Channels: channels, ProviderModels: pms,
		Trust: fakeTrust{"fake": "canary_failed", "good": "model_mismatch"}}
	d, err := r.Resolve(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatalf("demotion must never turn into an outage: %v", err)
	}
	if d.Channel.ID != "fake" {
		t.Fatalf("with every candidate suspect the normal order applies, got %s", d.Channel.ID)
	}
	if _, ok := d.Excluded["pm-good/good"]; ok {
		t.Fatalf("nothing was excluded for trust: %v", d.Excluded)
	}
}

// The live resolver serves from the published snapshot; the trust source
// must survive that copy (UpdateSnapshot is how config changes land).
func TestTrustSurvivesSnapshotUpdates(t *testing.T) {
	models, channels, pms := trustFixture()
	r := &Resolver{Trust: fakeTrust{"fake": "canary_failed"}}
	r.UpdateSnapshot(nil, channels, models, pms, nil, nil, nil)
	d, err := r.Resolve(context.Background(), Request{Model: "m"})
	if err != nil || d.Channel.ID != "good" {
		t.Fatalf("snapshot resolve ignored the trust source: %v %+v", err, d.Channel)
	}
	d, err = r.ResolveExcluding(context.Background(), Request{Model: "m"}, map[string]bool{})
	if err != nil || d.Channel.ID != "good" {
		t.Fatalf("failover resolve ignored the trust source: %v %+v", err, d.Channel)
	}
}

func TestChannelBindingsListsWhatAProbeCanReach(t *testing.T) {
	r := &Resolver{}
	r.UpdateSnapshot(
		map[string]domain.Provider{"p": {ID: "p", Enabled: true}, "q": {ID: "q", Enabled: true}},
		map[string]domain.Channel{
			// Disabled for routing: probes must still reach it, that is
			// how a demoted or paused channel earns its trust back.
			"c":     {ID: "c", ProviderID: "p", Name: "relay", Enabled: true, RoutingEnabled: false},
			"other": {ID: "other", ProviderID: "q", Enabled: true, RoutingEnabled: true},
		},
		map[string]domain.Model{
			"b":   {ID: "b", Enabled: true},
			"a":   {ID: "a", Enabled: true, ToolCallSupport: true},
			"off": {ID: "off", Enabled: false},
			"z":   {ID: "z", Enabled: true},
		},
		[]domain.ProviderModel{
			{ID: "pm-b", ProviderID: "p", ModelID: "b", UpstreamModelName: "upstream-b", Enabled: true},             // provider-wide binding
			{ID: "pm-a2", ProviderID: "p", ModelID: "a", UpstreamModelName: "wide-a", Enabled: true},                // provider-wide
			{ID: "pm-a1", ProviderID: "p", ChannelID: "c", ModelID: "a", UpstreamModelName: "own-a", Enabled: true}, // channel binding wins
			{ID: "pm-off", ProviderID: "p", ChannelID: "c", ModelID: "off", Enabled: true},                          // model disabled
			{ID: "pm-x", ProviderID: "p", ChannelID: "c", ModelID: "z", Enabled: false},                             // binding disabled
			{ID: "pm-o", ProviderID: "q", ChannelID: "other", ModelID: "z", Enabled: true},                          // other channel
		},
		nil, nil, nil,
	)
	got := r.ChannelBindings("c")
	if len(got) != 2 {
		t.Fatalf("expected bindings for models a and b, got %+v", got)
	}
	if got[0].Model.ID != "a" || got[0].ProviderModel.ID != "pm-a1" || got[0].ProviderModel.UpstreamModelName != "own-a" || !got[0].Model.ToolCallSupport {
		t.Fatalf("first binding: %+v", got[0])
	}
	if got[1].Model.ID != "b" || got[1].ProviderModel.ID != "pm-b" {
		t.Fatalf("second binding: %+v", got[1])
	}
	for _, d := range got {
		if d.Channel.ID != "c" || d.Channel.Name != "relay" || d.Excluded == nil {
			t.Fatalf("binding decision must carry the channel: %+v", d)
		}
	}
	if b := r.ChannelBindings("missing"); len(b) != 0 {
		t.Fatalf("unknown channel: %+v", b)
	}
}
