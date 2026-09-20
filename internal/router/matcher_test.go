package router

import (
	"relayhub/internal/domain"
	"testing"
)

func TestMatcherExactWins(t *testing.T) {
	m := Matcher{Routes: []domain.Route{{ID: "wild", Protocol: "openai", ModelPattern: "*", Enabled: true}, {ID: "exact", Protocol: "openai", ModelPattern: "m", Enabled: true}}}
	r, ok := m.Match("openai", "m")
	if !ok || r.ID != "exact" {
		t.Fatalf("%+v %v", r, ok)
	}
}
func TestMatcherGroupRouteMatchesOnlyRequestedGroup(t *testing.T) {
	routes := []domain.Route{{ID: "group", Protocol: "openai", GroupID: "coding", ModelPattern: "*", Enabled: true}}
	groups := map[string]domain.ModelGroup{"coding": {ID: "coding", Enabled: true}}
	if _, ok := MatchRoute(routes, "openai", "other", groups); ok {
		t.Fatal("group route captured unrelated model")
	}
	if got, ok := MatchRoute(routes, "openai", "coding", groups); !ok || got.ID != "group" {
		t.Fatalf("got=%+v matched=%v", got, ok)
	}
}
func TestMatcherFacadeUsesItsGroupCatalog(t *testing.T) {
	m := Matcher{Routes: []domain.Route{{ID: "group", Protocol: "openai", GroupID: "coding", ModelPattern: "*", Enabled: true}}, Groups: map[string]domain.ModelGroup{"coding": {ID: "coding", Enabled: true}}}
	if _, ok := m.Match("openai", "other"); ok {
		t.Fatal("facade group route captured unrelated model")
	}
	if got, ok := m.Match("openai", "coding"); !ok || got.ID != "group" {
		t.Fatalf("got=%+v matched=%v", got, ok)
	}
}
