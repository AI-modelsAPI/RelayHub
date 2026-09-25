package router

import (
	"context"
	"testing"

	"relayhub/internal/domain"
)

// staticPrices is the billing registry's router-facing surface: a measured
// effective price per channel, absent when nothing has been reconciled yet.
type staticPrices map[string]float64

func (p staticPrices) EffectiveUSDPerMTok(channelID string) (float64, bool) {
	v, ok := p[channelID]
	return v, ok
}

// priceResolver builds three channels serving the same model: "dear" is the
// highest priority, "cheap" the lowest, "unknown" has never been reconciled.
func priceResolver(prices PriceSource) Resolver {
	return Resolver{
		Models: map[string]domain.Model{"m": {ID: "m", Enabled: true}},
		Channels: map[string]domain.Channel{
			"dear":    {ID: "dear", ProviderID: "p", Priority: 1, Enabled: true, RoutingEnabled: true},
			"unknown": {ID: "unknown", ProviderID: "p", Priority: 2, Enabled: true, RoutingEnabled: true},
			"cheap":   {ID: "cheap", ProviderID: "p", Priority: 3, Enabled: true, RoutingEnabled: true},
		},
		ProviderModels: []domain.ProviderModel{
			{ID: "pm-dear", ProviderID: "p", ModelID: "m", ChannelID: "dear", Enabled: true},
			{ID: "pm-unknown", ProviderID: "p", ModelID: "m", ChannelID: "unknown", Enabled: true},
			{ID: "pm-cheap", ProviderID: "p", ModelID: "m", ChannelID: "cheap", Enabled: true},
		},
		Price: prices,
	}
}

func TestCheapestStrategyOrdersByObservedPrice(t *testing.T) {
	r := priceResolver(staticPrices{"dear": 5, "cheap": 1})
	r.Strategy = "cheapest"
	d, err := r.Resolve(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Channel.ID != "cheap" {
		t.Fatalf("cheapest chose %q; want the channel with the lowest measured price", d.Channel.ID)
	}
	if d.Strategy != "cheapest" {
		t.Fatalf("strategy recorded as %q", d.Strategy)
	}
}

func TestCheapestStrategyIgnoresUnknownPrices(t *testing.T) {
	// Only "dear" has a measured price: an unmeasured channel must not win
	// by looking free. With nothing measured at all, priority decides.
	r := priceResolver(staticPrices{"dear": 5})
	r.Strategy = "cost"
	d, err := r.Resolve(context.Background(), Request{Model: "m"})
	if err != nil || d.Channel.ID != "dear" {
		t.Fatalf("chose %q (%v); want the only measured channel", d.Channel.ID, err)
	}

	r2 := priceResolver(staticPrices{})
	r2.Strategy = "price"
	d2, err := r2.Resolve(context.Background(), Request{Model: "m"})
	if err != nil || d2.Channel.ID != "dear" {
		t.Fatalf("chose %q (%v); want the highest priority channel", d2.Channel.ID, err)
	}
}

func TestCheapestWithNoPriceSourceFallsBackToPriority(t *testing.T) {
	r := priceResolver(nil)
	r.Strategy = "cheapest"
	d, err := r.Resolve(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Channel.ID != "dear" {
		t.Fatalf("chose %q; want the priority winner", d.Channel.ID)
	}
}

func TestPriorityStrategyIgnoresMeasuredPrices(t *testing.T) {
	r := priceResolver(staticPrices{"dear": 5, "cheap": 1})
	d, err := r.Resolve(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Channel.ID != "dear" {
		t.Fatalf("the default strategy chose %q; measured prices must not change it", d.Channel.ID)
	}
}

func TestCheapFirstKeepsPriorityAsTieBreaker(t *testing.T) {
	// Equal measured prices: the first candidate (priority, then model ID)
	// keeps the traffic, so the strategy is stable for a fleet of identical
	// relays.
	c := []Candidate{
		{Channel: domain.Channel{ID: "a"}, ProviderModel: domain.ProviderModel{ID: "pm-a"}, Priority: 1},
		{Channel: domain.Channel{ID: "b"}, ProviderModel: domain.ProviderModel{ID: "pm-b"}, Priority: 2},
	}
	got := cheapFirst(c, staticPrices{"a": 1, "b": 1})
	if got.Channel.ID != "a" {
		t.Fatalf("tie-break chose %q", got.Channel.ID)
	}
}
