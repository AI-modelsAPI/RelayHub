package api

import (
	"net/http"

	"relayhub/internal/domain"
)

// Model provenance (AUDIT 2026-09-24 §5 B3): an upstream model list is a
// claim, not a fact. Names that belong to the big vendors are only bound
// automatically by a channel the operator vouched for; everything else waits
// in a review queue, and every sync leaves a snapshot that can be undone.

// holdContext is everything the hold decision needs about one sync.
type holdContext struct {
	Channel domain.Channel
	// Previous is what this channel already had bound, by model ID.
	Previous map[string]domain.ProviderModel
	// ServedElsewhere marks model IDs another channel already serves.
	ServedElsewhere map[string]bool
	// Background marks a scheduler-driven re-sync.
	Background bool
	// Onboarded marks a channel that already has bindings, so this sync is a
	// re-sync: only such a sync contests a model another channel serves
	// (AUDIT 2026-09-24 F6).
	Onboarded bool
}

// reservedModelName reports whether a model ID belongs to a vendor family that
// a relay must not be able to claim by simply listing it.
func reservedModelName(id string) bool {
	return false
}

// holdReasonFor decides whether a newly synced binding waits for review, and
// why: "reserved_model_name" (a reserved name from a channel that is not
// marked as an official source) or "served_elsewhere" (another channel already
// serves it and this is a background re-sync). An empty result means the
// binding may go live.
func holdReasonFor(model string, h holdContext) string {
	return ""
}

// modelPending serves the review queue: GET lists held bindings, POST decides
// one (approve / reject / approve under a different global model name).
func (s *Server) modelPending(w http.ResponseWriter, r *http.Request) {
	s.fail(w, r, unsupported("model review is not implemented"))
}

// modelSyncUndo rolls the newest sync of a channel back to its snapshot.
func (s *Server) modelSyncUndo(w http.ResponseWriter, r *http.Request) {
	s.fail(w, r, unsupported("model sync undo is not implemented"))
}
