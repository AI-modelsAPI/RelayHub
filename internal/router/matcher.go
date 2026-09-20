package router

import "relayhub/internal/domain"

type Matcher struct {
	Routes []domain.Route
	Groups map[string]domain.ModelGroup
}

func (m Matcher) Match(protocol, model string) (domain.Route, bool) {
	return MatchRoute(m.Routes, protocol, model, m.Groups)
}
func MatchRoute(routes []domain.Route, protocol, model string, groups map[string]domain.ModelGroup) (domain.Route, bool) {
	best := -1
	var out domain.Route
	normProto := normalizeProtocol(protocol)
	for _, r := range routes {
		if !r.Enabled || normalizeProtocol(r.Protocol) != normProto {
			continue
		}
		score := -1
		if r.GroupID != "" {
			if groups != nil {
				g, ok := groups[r.GroupID]
				if !ok || !g.Enabled {
					continue
				}
				if model != r.GroupID && model != g.Name && model != r.ModelPattern {
					continue
				}
			} else if model != r.GroupID && model != r.ModelPattern {
				continue
			}
			score = 2
		} else if r.ModelPattern == model {
			score = 3
		} else if r.ModelPattern == "*" {
			score = 1
		}
		if score > best || (score == best && r.ID < out.ID) {
			best, out = score, r
		}
	}
	return out, best >= 0
}
