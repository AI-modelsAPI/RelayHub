package router

import (
	"context"
	"relayhub/internal/domain"
	"relayhub/internal/health"
	"testing"
	"time"
)

func TestRouterProtocolMismatchRed(t *testing.T) {
	r := Resolver{
		Models: map[string]domain.Model{"m": {ID: "m", Enabled: true}},
		Channels: map[string]domain.Channel{
			"c": {ID: "c", ProviderID: "p", Enabled: true, RoutingEnabled: true},
		},
		ProviderModels: []domain.ProviderModel{
			{ID: "pm1", ProviderID: "p", ModelID: "m", ChannelID: "c", Protocol: "openai-chat", Enabled: true},
		},
	}
	d, err := r.Resolve(context.Background(), Request{Protocol: "openai", Model: "m"})
	if err != nil {
		t.Fatalf("expected candidate to match but got error: %v (excluded: %#v)", err, d.Excluded)
	}
	if d.Candidate.ProviderModel.ID != "pm1" {
		t.Fatalf("expected candidate pm1, got %v", d.Candidate.ProviderModel.ID)
	}
}

func TestResolvePriority(t *testing.T) {
	r := Resolver{Models: map[string]domain.Model{"m": {ID: "m", Enabled: true}}, Channels: map[string]domain.Channel{"c1": {ID: "c1", ProviderID: "p", Enabled: true, RoutingEnabled: true}, "c2": {ID: "c2", ProviderID: "p", Enabled: true, RoutingEnabled: true}}, ProviderModels: []domain.ProviderModel{{ID: "b", ProviderID: "p", ModelID: "m", ChannelID: "c2", Priority: 2, Enabled: true}, {ID: "a", ProviderID: "p", ModelID: "m", ChannelID: "c1", Priority: 1, Enabled: true}}}
	d, e := r.Resolve(context.Background(), Request{Model: "m", Now: time.Now()})
	if e != nil || d.Candidate.ProviderModel.ID != "a" {
		t.Fatalf("%+v %v", d, e)
	}
}
func TestResolveDirectModelGroupAndWeightedMembers(t *testing.T) {
	r := Resolver{Models: map[string]domain.Model{"slow": {ID: "slow", Enabled: true}, "fast": {ID: "fast", Enabled: true}}, Groups: map[string]domain.ModelGroup{"coding": {ID: "coding", Name: "coding", Strategy: "weighted", Enabled: true}}, Members: map[string]map[string]domain.ModelGroupMember{"coding": {"slow": {GroupID: "coding", ModelID: "slow", Priority: 1, Weight: 1}, "fast": {GroupID: "coding", ModelID: "fast", Priority: 1, Weight: 3}}}, Channels: map[string]domain.Channel{"c": {ID: "c", ProviderID: "p", Enabled: true, RoutingEnabled: true}}, ProviderModels: []domain.ProviderModel{{ID: "slow-pm", ProviderID: "p", ModelID: "slow", ChannelID: "c", Enabled: true}, {ID: "fast-pm", ProviderID: "p", ModelID: "fast", ChannelID: "c", Enabled: true}}}
	d, e := r.Resolve(context.Background(), Request{Model: "coding", Source: 1})
	if e != nil || d.Model.ID != "fast" {
		t.Fatalf("%+v %v", d, e)
	}
}
func TestResolveRouteChannelFilterAndProviderMapping(t *testing.T) {
	r := Resolver{Models: map[string]domain.Model{"m": {ID: "m", Enabled: true}}, Providers: map[string]domain.Provider{"p": {ID: "p", Enabled: true}}, Channels: map[string]domain.Channel{"allowed": {ID: "allowed", ProviderID: "p", RoutingTags: "prod,blue", Enabled: true, RoutingEnabled: true}, "other": {ID: "other", ProviderID: "p", RoutingTags: "dev", Enabled: true, RoutingEnabled: true}}, ProviderModels: []domain.ProviderModel{{ID: "pm", ProviderID: "p", ModelID: "m", Enabled: true}}, Routes: []domain.Route{{ID: "route", Protocol: "openai", ModelPattern: "m", ChannelFilter: "prod", Strategy: "fixed", Enabled: true}}}
	d, e := r.Resolve(context.Background(), Request{Protocol: "openai", Model: "m"})
	if e != nil || d.Channel.ID != "allowed" {
		t.Fatalf("%+v %v", d, e)
	}
}
func TestResolveUsesHealthMetrics(t *testing.T) {
	h := health.NewRegistry()
	h.MarkHealthy("slow", 500*time.Millisecond, .99)
	h.MarkHealthy("fast", 50*time.Millisecond, .80)
	r := Resolver{Models: map[string]domain.Model{"m": {ID: "m", Enabled: true}}, Channels: map[string]domain.Channel{"slow": {ID: "slow", ProviderID: "p", Enabled: true, RoutingEnabled: true}, "fast": {ID: "fast", ProviderID: "p", Enabled: true, RoutingEnabled: true}}, ProviderModels: []domain.ProviderModel{{ID: "a", ProviderID: "p", ModelID: "m", ChannelID: "slow", Enabled: true}, {ID: "b", ProviderID: "p", ModelID: "m", ChannelID: "fast", Enabled: true}}, Health: h, Strategy: "latency"}
	d, e := r.Resolve(context.Background(), Request{Model: "m"})
	if e != nil || d.Channel.ID != "fast" {
		t.Fatalf("%+v %v", d, e)
	}
	h.Set("slow", health.State{Status: health.Healthy, SuccessRate: .99})
	h.Set("fast", health.State{Status: health.Healthy, SuccessRate: .80})
	r.Strategy = "success-rate"
	d, e = r.Resolve(context.Background(), Request{Model: "m"})
	if e != nil || d.Channel.ID != "slow" {
		t.Fatalf("%+v %v", d, e)
	}
}
func TestResolveQuotaAwareAndPriorityTiers(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	h := health.NewRegistry()
	h.Set("low", health.State{Status: health.Healthy, QuotaRemaining: 100, QuotaResetAt: now.Add(2 * time.Hour)})
	h.Set("high", health.State{Status: health.Healthy, QuotaRemaining: 10, QuotaResetAt: now.Add(time.Hour), QuotaScore: 2})
	r := Resolver{Models: map[string]domain.Model{"m": {ID: "m", Enabled: true}}, Channels: map[string]domain.Channel{"low": {ID: "low", ProviderID: "p", Enabled: true, RoutingEnabled: true}, "high": {ID: "high", ProviderID: "p", Enabled: true, RoutingEnabled: true}}, ProviderModels: []domain.ProviderModel{{ID: "low-pm", ProviderID: "p", ModelID: "m", ChannelID: "low", Priority: 1, Weight: 1, Enabled: true}, {ID: "high-pm", ProviderID: "p", ModelID: "m", ChannelID: "high", Priority: 2, Weight: 100, Enabled: true}}, Health: h, Strategy: "weighted"}
	d, err := r.Resolve(context.Background(), Request{Model: "m", Now: now, Source: 1})
	if err != nil || d.Channel.ID != "low" {
		t.Fatalf("priority decision=%+v err=%v", d, err)
	}
	r.Strategy = "quota-aware"
	d, err = r.Resolve(context.Background(), Request{Model: "m", Now: now})
	if err != nil || d.Channel.ID != "low" {
		t.Fatalf("quota decision=%+v err=%v", d, err)
	}
}
func TestResolveProviderMappingAndGroupFallback(t *testing.T) {
	h := health.NewRegistry()
	h.MarkDisabled("preferred")
	r := Resolver{Models: map[string]domain.Model{"preferred": {ID: "preferred", Enabled: true}, "available": {ID: "available", Enabled: true}}, Groups: map[string]domain.ModelGroup{"g": {ID: "g", Strategy: "weighted", Enabled: true}}, Members: map[string]map[string]domain.ModelGroupMember{"g": {"preferred": {GroupID: "g", ModelID: "preferred", Priority: 1, Weight: 100}, "available": {GroupID: "g", ModelID: "available", Priority: 1, Weight: 1}}}, Channels: map[string]domain.Channel{"a": {ID: "a", ProviderID: "p", Priority: 1, Enabled: true, RoutingEnabled: true}, "z": {ID: "z", ProviderID: "p", Priority: 2, Enabled: true, RoutingEnabled: true}}, ProviderModels: []domain.ProviderModel{{ID: "preferred-pm", ProviderID: "p", ModelID: "preferred", Enabled: true}, {ID: "available-pm", ProviderID: "p", ModelID: "available", Enabled: true}}, Health: h}
	d, err := r.Resolve(context.Background(), Request{Model: "g"})
	if err != nil || d.Model.ID != "available" || d.Channel.ID != "a" {
		t.Fatalf("decision=%+v err=%v", d, err)
	}
}

// quota-first must route by observed balance (USD, cross-site comparable) and
// fall back to priority when nothing is known; an exhausted channel must be
// excluded with a readable reason and traffic must move to the next one.
func TestResolveQuotaFirstUsesObservedBalance(t *testing.T) {
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	h := health.NewRegistry()
	r := Resolver{Models: map[string]domain.Model{"m": {ID: "m", Enabled: true}},
		Channels: map[string]domain.Channel{
			"free": {ID: "free", ProviderID: "p", Enabled: true, RoutingEnabled: true},
			"paid": {ID: "paid", ProviderID: "p", Enabled: true, RoutingEnabled: true},
		},
		ProviderModels: []domain.ProviderModel{
			// paid sorts first by ID and by priority: quota must still win.
			{ID: "a-paid", ProviderID: "p", ModelID: "m", ChannelID: "paid", Priority: 1, Enabled: true},
			{ID: "b-free", ProviderID: "p", ModelID: "m", ChannelID: "free", Priority: 2, Enabled: true},
		}, Health: h, Strategy: "quota-first"}

	// Nothing observed yet: fall back to priority.
	d, err := r.Resolve(context.Background(), Request{Model: "m", Now: now})
	if err != nil || d.Channel.ID != "paid" {
		t.Fatalf("unknown quota should fall back to priority: %+v %v", d, err)
	}
	h.SetQuota("free", health.QuotaUpdate{USD: 2, Remaining: 1000000, Known: true}, now)
	for _, strategy := range []string{"quota-first", "quota_first", "quota-aware"} {
		r.Strategy = strategy
		d, err = r.Resolve(context.Background(), Request{Model: "m", Now: now})
		if err != nil || d.Channel.ID != "free" {
			t.Fatalf("%s: funded channel must win: %+v %v", strategy, d, err)
		}
	}
	// Two funded channels: larger USD balance first.
	h.SetQuota("paid", health.QuotaUpdate{USD: 5, Remaining: 10, Known: true}, now)
	d, err = r.Resolve(context.Background(), Request{Model: "m", Now: now})
	if err != nil || d.Channel.ID != "paid" {
		t.Fatalf("higher USD balance must win regardless of native units: %+v %v", d, err)
	}
	// Exhausted: excluded with reason, traffic moves on.
	h.SetQuota("paid", health.QuotaUpdate{USD: 0, Remaining: 0, Known: true}, now)
	d, err = r.Resolve(context.Background(), Request{Model: "m", Now: now})
	if err != nil || d.Channel.ID != "free" {
		t.Fatalf("exhausted channel must be skipped: %+v %v", d, err)
	}
	if d.Excluded["a-paid/paid"] != "quota exhausted" {
		t.Fatalf("exclusion reason must be explainable: %#v", d.Excluded)
	}
}
