package router

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"relayhub/internal/affinity"
	"relayhub/internal/domain"
	"relayhub/internal/health"
)

type Request struct {
	Protocol, Model   string
	Now               time.Time
	Source            uint64
	SessionKey        string
	ToolCallRequired  bool
	VisionRequired    bool
	ReasoningRequired bool
}
type Candidate struct {
	ProviderModel domain.ProviderModel
	Channel       domain.Channel
	Model         domain.Model
	Weight        int
	Priority      int
}
type Decision struct {
	Candidate      Candidate
	Model          domain.Model
	ProviderModel  domain.ProviderModel
	Channel        domain.Channel
	Transform      domain.ProviderModel
	Excluded       map[string]string
	Route          domain.Route
	Strategy       string
	PreferredKeyID string
}
type Resolver struct {
	state          *resolverState
	Models         map[string]domain.Model
	ProviderModels []domain.ProviderModel
	Channels       map[string]domain.Channel
	Providers      map[string]domain.Provider
	Groups         map[string]domain.ModelGroup
	Members        map[string]map[string]domain.ModelGroupMember
	Routes         []domain.Route
	Health         *health.Registry
	Strategy       string
	FixedChannel   string
	Rand           RandomSource
	Sticky         *affinity.Table
}

type resolverState struct {
	mu             sync.RWMutex
	Models         map[string]domain.Model
	ProviderModels []domain.ProviderModel
	Channels       map[string]domain.Channel
	Providers      map[string]domain.Provider
	Groups         map[string]domain.ModelGroup
	Members        map[string]map[string]domain.ModelGroupMember
	Routes         []domain.Route
}

func (r *Resolver) ensureState() *resolverState {
	if r.state != nil {
		return r.state
	}
	r.state = &resolverState{
		Models:         r.Models,
		ProviderModels: r.ProviderModels,
		Channels:       r.Channels,
		Providers:      r.Providers,
		Groups:         r.Groups,
		Members:        r.Members,
		Routes:         r.Routes,
	}
	return r.state
}

func (r *Resolver) UpdateSnapshot(
	providers map[string]domain.Provider,
	channels map[string]domain.Channel,
	models map[string]domain.Model,
	providerModels []domain.ProviderModel,
	groups map[string]domain.ModelGroup,
	members map[string]map[string]domain.ModelGroupMember,
	routes []domain.Route,
) {
	st := r.ensureState()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Providers = providers
	st.Channels = channels
	st.Models = models
	st.ProviderModels = providerModels
	st.Groups = groups
	st.Members = members
	st.Routes = routes
}

func (r Resolver) snapshot() Resolver {
	if r.state != nil {
		r.state.mu.RLock()
		defer r.state.mu.RUnlock()
		return Resolver{
			Models:         r.state.Models,
			ProviderModels: r.state.ProviderModels,
			Channels:       r.state.Channels,
			Providers:      r.state.Providers,
			Groups:         r.state.Groups,
			Members:        r.state.Members,
			Routes:         r.state.Routes,
			Health:         r.Health,
			Strategy:       r.Strategy,
			FixedChannel:   r.FixedChannel,
			Rand:           r.Rand,
			Sticky:         r.Sticky,
		}
	}
	return Resolver{
		Models:         r.Models,
		ProviderModels: r.ProviderModels,
		Channels:       r.Channels,
		Providers:      r.Providers,
		Groups:         r.Groups,
		Members:        r.Members,
		Routes:         r.Routes,
		Health:         r.Health,
		Strategy:       r.Strategy,
		FixedChannel:   r.FixedChannel,
		Rand:           r.Rand,
		Sticky:         r.Sticky,
	}
}

type RandomSource interface{ Uint64() uint64 }
type counterSource struct{ n atomic.Uint64 }

func (c *counterSource) Uint64() uint64 { return c.n.Add(1) - 1 }

var ErrModelNotFound = fmt.Errorf("model not found")
var ErrNoCandidates = fmt.Errorf("no available candidates")

func normalizeProtocol(p string) string {
	switch p {
	case "openai", "openai-chat":
		return "openai-chat"
	case "openai-responses":
		return "openai-responses"
	case "anthropic", "anthropic-messages":
		return "anthropic-messages"
	case "gemini":
		return "gemini"
	default:
		return p
	}
}

func (r Resolver) Resolve(ctx context.Context, req Request) (Decision, error) {
	return r.snapshot().resolve(ctx, req, nil)
}

// RoutableModels returns all distinct, enabled logical models that have at least one
// eligible enabled provider-model binding and channel.
func (r Resolver) RoutableModels() []domain.Model {
	snap := r.snapshot()
	now := time.Now()
	seen := make(map[string]domain.Model)
	for _, m := range snap.Models {
		if !m.Enabled {
			continue
		}
		dummyD := &Decision{Excluded: map[string]string{}}
		cands := snap.candidates("", m, dummyD, "", now, Request{})
		if len(cands) > 0 {
			seen[m.ID] = m
		}
	}
	out := make([]domain.Model, 0, len(seen))
	for _, m := range seen {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// ResolveExcluding resolves the next eligible candidate while excluding
// channels that already failed this request. It is used by protocol gateways
// before any response event has been sent to the client.
func (r Resolver) ResolveExcluding(ctx context.Context, req Request, excluded map[string]bool) (Decision, error) {
	return r.snapshot().resolve(ctx, req, excluded)
}

func (r Resolver) resolve(ctx context.Context, req Request, excluded map[string]bool) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	req.Protocol = normalizeProtocol(req.Protocol)
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	d := Decision{Excluded: map[string]string{}}
	modelID := req.Model
	route, matched := MatchRoute(r.Routes, req.Protocol, req.Model, r.Groups)
	if matched {
		d.Route = route
		if route.GroupID != "" {
			var err error
			modelID, err = r.selectGroupModel(route.GroupID, req, req.Protocol, route.ChannelFilter, now, &d, map[string]bool{})
			if err != nil {
				return d, err
			}
		}
	} else if g, ok := r.findGroup(req.Model); ok && g.Enabled {
		var err error
		modelID, err = r.selectGroupModel(g.ID, req, req.Protocol, "", now, &d, map[string]bool{})
		if err != nil {
			return d, err
		}
	}
	m, ok := r.Models[modelID]
	if !ok || !m.Enabled {
		return d, fmt.Errorf("%w: %s", ErrModelNotFound, modelID)
	}
	d.Model = m
	d.Strategy = r.Strategy
	if route.Strategy != "" {
		d.Strategy = route.Strategy
	}
	if d.Strategy == "" {
		d.Strategy = "priority"
	}
	candidates := r.candidates(req.Protocol, m, &d, route.ChannelFilter, now, req, excluded)
	if len(candidates) == 0 {
		return d, fmt.Errorf("%w: %s", ErrNoCandidates, exclusionSummary(d.Excluded))
	}
	chosen := selectCandidate(candidates, d.Strategy, r.FixedChannel, r.Health, r.Rand, req.Source)
	if req.SessionKey != "" && r.Sticky != nil {
		if chID, _, ok := r.Sticky.Lookup(req.SessionKey); ok {
			for _, c := range candidates {
				if c.Channel.ID == chID {
					chosen = c
					break
				}
			}
		}
	}
	d.Candidate, d.ProviderModel, d.Channel, d.Transform = chosen, chosen.ProviderModel, chosen.Channel, chosen.ProviderModel
	return d, nil
}
func (r Resolver) findGroup(nameOrID string) (domain.ModelGroup, bool) {
	if g, ok := r.Groups[nameOrID]; ok {
		return g, true
	}
	for _, g := range r.Groups {
		if g.Name == nameOrID {
			return g, true
		}
	}
	return domain.ModelGroup{}, false
}

func (r Resolver) selectGroupModel(groupID string, req Request, protocol, filter string, now time.Time, d *Decision, visited map[string]bool) (string, error) {
	g, ok := r.Groups[groupID]
	if !ok || !g.Enabled {
		return "", fmt.Errorf("%w: group %s", ErrModelNotFound, groupID)
	}
	visited[groupID] = true
	list := []domain.ModelGroupMember{}
	for _, m := range r.Members[groupID] {
		if model, ok := r.Models[m.ModelID]; ok && model.Enabled && len(r.candidates(protocol, model, d, filter, now, req)) > 0 {
			list = append(list, m)
		}
	}
	if len(list) == 0 {
		if g.FallbackGroupID != "" {
			return r.selectGroupModel(g.FallbackGroupID, req, protocol, filter, now, d, visited)
		}
		return "", fmt.Errorf("%w: group %s has no available members", ErrNoCandidates, groupID)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Priority != list[j].Priority {
			return list[i].Priority < list[j].Priority
		}
		return list[i].ModelID < list[j].ModelID
	})
	strategy := strings.ToLower(g.Strategy)
	if strategy == "weighted" || strategy == "weighted-round-robin" || strategy == "round-robin" {
		bestPriority := list[0].Priority
		for len(list) > 0 && list[0].Priority != bestPriority {
			list = list[1:]
		}
		total := 0
		for _, m := range list {
			w := m.Weight
			if w <= 0 {
				w = 1
			}
			total += w
		}
		n := req.Source % uint64(total)
		for _, m := range list {
			w := m.Weight
			if w <= 0 {
				w = 1
			}
			if n < uint64(w) {
				return m.ModelID, nil
			}
			n -= uint64(w)
		}
	}
	return list[0].ModelID, nil
}
func (r Resolver) candidates(protocol string, m domain.Model, d *Decision, filter string, now time.Time, req Request, excluded ...map[string]bool) []Candidate {
	out := []Candidate{}
	for _, pm := range r.ProviderModels {
		if pm.ModelID != m.ID || !pm.Enabled {
			continue
		}
		pmProto := normalizeProtocol(pm.Protocol)
		protoMatch := (pmProto == "" || protocol == "" || pmProto == protocol)
		if !protoMatch {
			// Cross-protocol conversion is supported between openai-chat and anthropic-messages
			if (protocol == "openai-chat" && pmProto == "anthropic-messages") || (protocol == "anthropic-messages" && pmProto == "openai-chat") {
				protoMatch = true
			}
		}
		if !protoMatch {
			d.Excluded[pm.ID] = "protocol mismatch"
			continue
		}
		p, ok := r.Providers[pm.ProviderID]
		if len(r.Providers) > 0 && (!ok || !p.Enabled) {
			d.Excluded[pm.ID] = "provider disabled"
			continue
		}
		channels := []domain.Channel{}
		if pm.ChannelID != "" {
			ch, exists := r.Channels[pm.ChannelID]
			if !exists {
				d.Excluded[pm.ID] = "missing channel"
				continue
			}
			channels = append(channels, ch)
		} else {
			for _, ch := range r.Channels {
				if ch.ProviderID == pm.ProviderID {
					channels = append(channels, ch)
				}
			}
			sort.Slice(channels, func(i, j int) bool {
				if channels[i].Priority != channels[j].Priority {
					return channels[i].Priority < channels[j].Priority
				}
				return channels[i].ID < channels[j].ID
			})
		}
		if len(channels) == 0 {
			d.Excluded[pm.ID] = "provider has no eligible channel"
			continue
		}
		for _, ch := range channels {
			key := pm.ID + "/" + ch.ID
			if len(excluded) > 0 && excluded[0] != nil && excluded[0][ch.ID] {
				d.Excluded[key] = "already attempted"
				continue
			}
			if !ch.Enabled || !ch.RoutingEnabled {
				d.Excluded[key] = "disabled channel"
				continue
			}
			if filter != "" && !hasTag(ch.RoutingTags, filter) {
				d.Excluded[key] = "route channel filter mismatch"
				continue
			}
			if r.FixedChannel != "" && ch.ID != r.FixedChannel {
				d.Excluded[key] = "fixed channel mismatch"
				continue
			}
			if r.Health != nil && !r.Health.Available(ch.ID, now) {
				d.Excluded[key] = r.Health.Reason(ch.ID, now)
				continue
			}
			if req.ToolCallRequired && !m.ToolCallSupport {
				d.Excluded[key] = "model missing required tool_call capability"
				continue
			}
			if req.VisionRequired && !m.VisionSupport {
				d.Excluded[key] = "model missing required vision capability"
				continue
			}
			if req.ReasoningRequired && !m.ReasoningSupport {
				d.Excluded[key] = "model missing required reasoning capability"
				continue
			}
			w := pm.Weight
			if w <= 0 {
				w = ch.Weight
			}
			if w <= 0 {
				w = 1
			}
			priority := pm.Priority
			if pm.ChannelID == "" && ch.Priority < priority {
				priority = ch.Priority
			}
			out = append(out, Candidate{pm, ch, m, w, priority})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ProviderModel.ID < out[j].ProviderModel.ID
	})
	return out
}
func hasTag(tags, want string) bool {
	for _, tag := range strings.Split(tags, ",") {
		if strings.TrimSpace(tag) == want {
			return true
		}
	}
	return false
}

// isQuotaStrategy reports whether the strategy routes by observed balance.
// "quota-first" is the documented name; "quota-aware" is the historical one.
func isQuotaStrategy(strategy string) bool {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "quota-first", "quota_first", "quota-aware", "quota_aware":
		return true
	}
	return false
}

func selectCandidate(c []Candidate, strategy, fixed string, h *health.Registry, rand RandomSource, source uint64) Candidate {
	if fixed != "" {
		for _, v := range c {
			if v.Channel.ID == fixed {
				return v
			}
		}
	}
	// quota-first deliberately looks across priority tiers: its whole point
	// is "spend observed free/prepaid balance before touching the channels I
	// would otherwise prefer". Priority remains the tie-breaker.
	if isQuotaStrategy(strategy) {
		return quotaFirst(c, h)
	}
	c = highestPriority(c)
	switch strings.ToLower(strategy) {
	case "fixed":
		return c[0]
	case "latency":
		return minBy(c, func(v Candidate) int64 {
			if h != nil {
				return h.Get(v.Channel.ID).Latency.Nanoseconds()
			}
			return int64(v.Channel.Priority)
		})
	case "success-rate":
		return maxBy(c, func(v Candidate) int64 {
			if h != nil {
				return int64(h.Get(v.Channel.ID).SuccessRate * 1000000)
			}
			return int64(v.Channel.Priority)
		})
	case "weighted", "weighted-round-robin", "round-robin", "random":
		total := 0
		for _, v := range c {
			total += v.Weight
		}
		if total > 0 {
			n := source
			if rand != nil {
				n = rand.Uint64()
			}
			target := int(n % uint64(total))
			for _, v := range c {
				target -= v.Weight
				if target < 0 {
					return v
				}
			}
		}
	}
	return c[0]
}

// quotaFirst orders candidates by observed balance: channels with a known,
// positive balance first (larger USD balance first, then larger native
// remaining), then channels whose quota was never observed; priority and
// ProviderModel ID break ties. Exhausted channels never reach this function
// because health.Available already excluded them.
func quotaFirst(c []Candidate, h *health.Registry) Candidate {
	type ranked struct {
		known     bool
		usd       float64
		remaining int64
	}
	rank := func(v Candidate) ranked {
		if h == nil {
			return ranked{}
		}
		s := h.Get(v.Channel.ID)
		if !s.QuotaKnown {
			return ranked{}
		}
		return ranked{known: true, usd: s.QuotaUSD, remaining: s.QuotaRemaining}
	}
	better := func(a, b Candidate) bool {
		ra, rb := rank(a), rank(b)
		if ra.known != rb.known {
			return ra.known
		}
		if ra.known {
			if ra.usd != rb.usd {
				return ra.usd > rb.usd
			}
			if ra.remaining != rb.remaining {
				return ra.remaining > rb.remaining
			}
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ProviderModel.ID < b.ProviderModel.ID
	}
	best := c[0]
	for _, v := range c[1:] {
		if better(v, best) {
			best = v
		}
	}
	return best
}

func highestPriority(c []Candidate) []Candidate {
	priority := c[0].Priority
	for _, v := range c[1:] {
		if v.Priority < priority {
			priority = v.Priority
		}
	}
	out := make([]Candidate, 0, len(c))
	for _, v := range c {
		if v.Priority == priority {
			out = append(out, v)
		}
	}
	return out
}
func minBy(c []Candidate, score func(Candidate) int64) Candidate {
	best := c[0]
	for _, v := range c[1:] {
		if score(v) < score(best) || (score(v) == score(best) && v.ProviderModel.ID < best.ProviderModel.ID) {
			best = v
		}
	}
	return best
}
func maxBy(c []Candidate, score func(Candidate) int64) Candidate {
	best := c[0]
	for _, v := range c[1:] {
		if score(v) > score(best) || (score(v) == score(best) && v.ProviderModel.ID < best.ProviderModel.ID) {
			best = v
		}
	}
	return best
}
func exclusionSummary(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+m[k])
	}
	return strings.Join(parts, "; ")
}
