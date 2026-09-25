package router

import (
	"sort"

	"relayhub/internal/domain"
)

// TrustSource reports channels whose authenticity probes failed (AUDIT §5
// B1): a canned or wrong answer, another model behind the requested name,
// missing tool support. verify.Registry implements it.
type TrustSource interface {
	Suspect(channelID string) (bool, string)
}

// preferTrusted drops candidates on suspect channels as long as a trusted
// candidate remains, recording why on the decision so route explain can show
// it. When every candidate is suspect all of them are kept: a demoted
// channel still beats no answer, and the strategy picks among them as usual.
func preferTrusted(c []Candidate, t TrustSource, d *Decision) []Candidate {
	if t == nil || len(c) == 0 {
		return c
	}
	trusted := make([]Candidate, 0, len(c))
	demoted := map[string]string{}
	for _, v := range c {
		if bad, why := t.Suspect(v.Channel.ID); bad {
			demoted[v.ProviderModel.ID+"/"+v.Channel.ID] = why
			continue
		}
		trusted = append(trusted, v)
	}
	if len(trusted) == 0 {
		return c
	}
	for key, why := range demoted {
		d.Excluded[key] = why
	}
	return trusted
}

// ChannelBindings lists one decision per enabled logical model that
// channelID serves, sorted by model ID: how the gateway would reach that
// model over that channel. Strategy, health, trust and the channel's own
// routing switches are deliberately ignored — probes must be able to reach
// a paused or demoted channel so it can earn its trust back. A binding on
// the channel itself wins over a provider-wide one, then priority, then ID.
func (r Resolver) ChannelBindings(channelID string) []Decision {
	snap := r.snapshot()
	ch, ok := snap.Channels[channelID]
	if !ok {
		return nil
	}
	better := func(a, b domain.ProviderModel) bool {
		if (a.ChannelID != "") != (b.ChannelID != "") {
			return a.ChannelID != ""
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	}
	best := map[string]domain.ProviderModel{}
	for _, pm := range snap.ProviderModels {
		if !pm.Enabled {
			continue
		}
		if pm.ChannelID != "" && pm.ChannelID != channelID {
			continue
		}
		if pm.ChannelID == "" && pm.ProviderID != ch.ProviderID {
			continue
		}
		if p, ok := snap.Providers[pm.ProviderID]; len(snap.Providers) > 0 && (!ok || !p.Enabled) {
			continue
		}
		if m, ok := snap.Models[pm.ModelID]; !ok || !m.Enabled {
			continue
		}
		if cur, ok := best[pm.ModelID]; ok && !better(pm, cur) {
			continue
		}
		best[pm.ModelID] = pm
	}
	ids := make([]string, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Decision, 0, len(ids))
	for _, id := range ids {
		pm, m := best[id], snap.Models[id]
		c := Candidate{ProviderModel: pm, Channel: ch, Model: m, Weight: pm.Weight, Priority: pm.Priority}
		out = append(out, Decision{
			Candidate:     c,
			Model:         m,
			ProviderModel: pm,
			Channel:       ch,
			Transform:     pm,
			Excluded:      map[string]string{},
			Strategy:      "probe",
		})
	}
	return out
}
