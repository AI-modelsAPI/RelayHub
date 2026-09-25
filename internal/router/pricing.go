package router

import "strings"

// PriceSource reports the observed price of a channel in USD per million
// tokens, as measured by the billing reconciler from the site's own
// consumption log (AUDIT §5 B2). billing.Registry implements it. A channel
// whose price is unknown is not ranked by cost: an unmeasured channel must not
// look cheaper than a measured one.
type PriceSource interface {
	EffectiveUSDPerMTok(channelID string) (float64, bool)
}

// normalizeStrategy lowercases and trims a strategy name.
func normalizeStrategy(strategy string) string {
	return strings.ToLower(strings.TrimSpace(strategy))
}

// isPriceStrategy reports whether the strategy orders by observed cost.
// "cheapest" is the documented name; "cost", "price" and "effective-price" are
// accepted spellings.
func isPriceStrategy(strategy string) bool {
	switch normalizeStrategy(strategy) {
	case "cheapest", "cost", "price", "effective-price", "effective_price":
		return true
	}
	return false
}

// cheapFirst picks the candidate with the lowest observed effective price.
// Like quota-first it deliberately looks across priority tiers: choosing the
// strategy means the user wants the money to decide, with priority and model
// ID as the tie-breakers (the candidate list already arrives in that order).
// Channels without a measured price come last; when none has one the first
// candidate wins, so the strategy degrades into "priority" instead of picking
// at random.
func cheapFirst(c []Candidate, p PriceSource) Candidate {
	if p == nil || len(c) == 0 {
		if len(c) == 0 {
			return Candidate{}
		}
		return c[0]
	}
	best, bestPrice, known := c[0], 0.0, false
	for _, v := range c {
		price, ok := p.EffectiveUSDPerMTok(v.Channel.ID)
		if !ok {
			continue
		}
		if !known || price < bestPrice {
			best, bestPrice, known = v, price, true
		}
	}
	return best
}
