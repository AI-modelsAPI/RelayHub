package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
)

// Model provenance (AUDIT 2026-09-24 §5 B3): an upstream model list is a
// claim, not a fact. Names that belong to the big vendors are only bound
// automatically by a channel the operator vouched for; everything else waits
// in a review queue, and every sync leaves a snapshot that can be undone.

// Held reasons, stored on the binding so the review queue survives restarts.
const (
	heldReservedName    = "reserved_model_name"
	heldServedElsewhere = "served_elsewhere"
)

// reservedFamilies are the vendor model families a relay must not be able to
// claim by simply listing them. Matching is on the family followed by a
// separator or a digit, so "claudette-3" or "openai-gpt-4o" stay automatic.
var reservedFamilies = []string{
	"claude", "gpt", "chatgpt", "gemini", "grok", "dall-e", "whisper", "sora", "text-embedding",
}

// reservedVendors are the namespaces that belong to a specific vendor: any
// model claimed under "openai/…" or "anthropic/…" is impersonating the
// vendor's own catalog.
var reservedVendors = []string{"openai", "anthropic", "google", "xai"}

// oSeries matches OpenAI's o1/o3/o4 reasoning families ("o" + digit), which
// also keeps "orion-mini" and friends out of the reserved set.
var oSeries = regexp.MustCompile(`^o[0-9]`)

// holdContext is everything the hold decision needs about one sync.
type holdContext struct {
	Channel domain.Channel
	// Previous is what this channel already had bound, by model ID; the
	// operator's decisions about those bindings stand.
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
	trimmed := strings.ToLower(strings.TrimSpace(id))
	if trimmed == "" {
		return false
	}
	parts := strings.Split(trimmed, "/")
	for _, ns := range parts[:len(parts)-1] {
		for _, vendor := range reservedVendors {
			if ns == vendor {
				return true
			}
		}
	}
	name := parts[len(parts)-1]
	for _, family := range reservedFamilies {
		if strings.HasPrefix(name, family) && familyBoundary(name[len(family):]) {
			return true
		}
	}
	return oSeries.MatchString(name)
}

// familyBoundary reports whether a vendor family match ends there -- "gpt",
// "gpt-4", "gpt4" -- instead of continuing as an unrelated word, which keeps
// "claudette-3", "openai-gpt-4o" and "opus-relay" automatic.
func familyBoundary(rest string) bool {
	if rest == "" {
		return true
	}
	if c := rest[0]; c == '-' || c == '_' || c == '.' || (c >= '0' && c <= '9') {
		return true
	}
	return false
}

// holdReasonFor decides whether a newly synced binding waits for review, and
// why. An empty result means the binding may go live: the operator already
// ruled on this model for this channel, or nothing about the claim is
// sensitive.
func holdReasonFor(model string, h holdContext) string {
	if _, known := h.Previous[model]; known {
		return ""
	}
	if reservedModelName(model) && !h.Channel.OfficialSource {
		return heldReservedName
	}
	if h.Background && h.Onboarded && h.ServedElsewhere[model] {
		return heldServedElsewhere
	}
	return ""
}

// pendingItem is one claim waiting for a decision.
type pendingItem struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name,omitempty"`
	ModelID     string `json:"model_id"`
	BindingID   string `json:"binding_id"`
	Reason      string `json:"reason"`
	Upstream    string `json:"upstream_model_name,omitempty"`
	Official    bool   `json:"official_source"`
}

// modelPending serves the review queue: GET lists held bindings, POST decides
// one (approve / reject / approve under a different global model name).
func (s *Server) modelPending(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if s.Repo == nil {
		s.fail(w, r, unavailable("resource persistence is not configured"))
		return
	}
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		items, err := s.pendingModels(ctx)
		if err != nil {
			s.fail(w, r, internal(err))
			return
		}
		s.write(w, r, http.StatusOK, map[string]any{"pending": items, "total": len(items)})
	case http.MethodPost:
		s.decidePendingModel(w, r, ctx)
	default:
		s.methodAllowed(w, r, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) pendingModels(ctx context.Context) ([]pendingItem, error) {
	bindings, err := s.Repo.ListProviderModels(ctx, "")
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	official := map[string]bool{}
	if channels, err := s.Repo.ListChannels(ctx, ""); err == nil {
		for _, ch := range channels {
			names[ch.ID] = ch.Name
			official[ch.ID] = ch.OfficialSource
		}
	}
	out := []pendingItem{}
	for _, pm := range bindings {
		if pm.HeldReason == "" {
			continue
		}
		out = append(out, pendingItem{
			ChannelID:   pm.ChannelID,
			ChannelName: names[pm.ChannelID],
			ModelID:     pm.ModelID,
			BindingID:   pm.ID,
			Reason:      pm.HeldReason,
			Upstream:    pm.UpstreamModelName,
			Official:    official[pm.ChannelID],
		})
	}
	return out, nil
}

func (s *Server) decidePendingModel(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	var in struct {
		ChannelID string `json:"channel_id"`
		ModelID   string `json:"model_id"`
		Action    string `json:"action"`
		Target    string `json:"model_id_target"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.ChannelID = strings.TrimSpace(in.ChannelID)
	in.ModelID = strings.TrimSpace(in.ModelID)
	in.Action = strings.ToLower(strings.TrimSpace(in.Action))
	in.Target = strings.TrimSpace(in.Target)
	if in.ChannelID == "" || in.ModelID == "" {
		s.fail(w, r, badRequest("validation_error", "channel_id and model_id are required"))
		return
	}
	switch in.Action {
	case "approve", "reject":
	default:
		s.fail(w, r, badRequest("validation_error", "action must be approve or reject"))
		return
	}

	binding, err := s.heldBinding(ctx, in.ChannelID, in.ModelID)
	if err != nil {
		s.fail(w, r, mapRepoError(err, "pending model"))
		return
	}

	if in.Action == "reject" {
		if err := s.Repo.DeleteProviderModel(ctx, binding.ID); err != nil {
			s.fail(w, r, mapRepoError(err, "pending model"))
			return
		}
		// The claim is gone, so a global name only this claim invented must
		// not stay behind: the relay would still look like it serves it.
		s.reapSyncedModel(ctx, in.ChannelID, binding.ModelID)
		s.auditEvent(ctx, "reject_model_claim", r, map[string]string{"channel": in.ChannelID, "model": in.ModelID})
		s.notifyConfigChange(ctx)
		s.write(w, r, http.StatusOK, map[string]any{"channel_id": in.ChannelID, "model_id": in.ModelID, "action": "reject"})
		return
	}

	if in.Target != "" {
		if _, err := s.Repo.GetModel(ctx, in.Target); err != nil {
			s.fail(w, r, badRequest("validation_error", "model_id_target must name an existing model"))
			return
		}
	}
	rejected := binding.ModelID
	binding.Enabled = true
	binding.HeldReason = ""
	if in.Target != "" {
		binding.ModelID = in.Target
		if binding.UpstreamModelName == "" {
			binding.UpstreamModelName = rejected
		}
	}
	if err := s.Repo.UpdateProviderModel(ctx, binding); err != nil {
		s.fail(w, r, mapRepoError(err, "pending model"))
		return
	}
	// The approved claim makes its global model routable.
	modelID := binding.ModelID
	if m, err := s.Repo.GetModel(ctx, modelID); err == nil {
		if !m.Enabled {
			m.Enabled = true
			_ = s.Repo.UpdateModel(ctx, m)
		}
	} else if errors.Is(err, repository.ErrNotFound) {
		_ = s.Repo.CreateModel(ctx, domain.Model{ID: modelID, DisplayName: modelID, Enabled: true})
	}
	if in.Target != "" && rejected != modelID {
		s.reapSyncedModel(ctx, in.ChannelID, rejected)
	}
	s.auditEvent(ctx, "approve_model_claim", r, map[string]string{"channel": in.ChannelID, "model": in.ModelID, "as": modelID})
	s.notifyConfigChange(ctx)
	s.write(w, r, http.StatusOK, map[string]any{
		"channel_id": in.ChannelID, "model_id": in.ModelID, "action": "approve", "model_id_target": modelID,
	})
}

// heldBinding finds the pending claim a decision refers to.
func (s *Server) heldBinding(ctx context.Context, channelID, modelID string) (domain.ProviderModel, error) {
	bindings, err := s.Repo.ListProviderModels(ctx, modelID)
	if err != nil {
		return domain.ProviderModel{}, err
	}
	for _, pm := range bindings {
		if pm.ChannelID == channelID && pm.ModelID == modelID && pm.HeldReason != "" {
			return pm, nil
		}
	}
	return domain.ProviderModel{}, badRequest("not_pending", fmt.Sprintf("no pending claim for %s on channel %s", modelID, channelID))
}

// reapSyncedModel drops a global model a sync invented once nothing structural
// refers to it any more. Models the operator created (or that another binding
// still uses) are left alone.
func (s *Server) reapSyncedModel(ctx context.Context, channelID, modelID string) {
	if modelID == "" {
		return
	}
	if bindings, err := s.Repo.ListProviderModels(ctx, modelID); err == nil {
		for _, pm := range bindings {
			if pm.ModelID == modelID {
				return
			}
		}
	}
	snap, err := s.Repo.LastModelSyncSnapshot(ctx, channelID)
	if err != nil {
		return
	}
	invented := false
	for _, id := range snap.AddedModels {
		if id == modelID {
			invented = true
			break
		}
	}
	if !invented {
		return
	}
	_ = s.Repo.DeleteModel(ctx, modelID)
}

// modelSyncUndo rolls the newest sync of a channel back to its snapshot.
func (s *Server) modelSyncUndo(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if s.Repo == nil {
		s.fail(w, r, unavailable("resource persistence is not configured"))
		return
	}
	if r.Method != http.MethodPost {
		s.fail(w, r, badRequest("method_not_allowed", "POST is required"))
		return
	}
	var in struct {
		ChannelID string `json:"channel_id"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.ChannelID = strings.TrimSpace(in.ChannelID)
	if in.ChannelID == "" {
		s.fail(w, r, badRequest("validation_error", "channel_id is required"))
		return
	}
	ctx := r.Context()
	if _, err := s.Repo.GetChannel(ctx, in.ChannelID); err != nil {
		s.fail(w, r, mapRepoError(err, "channel"))
		return
	}
	snap, err := s.Repo.LastModelSyncSnapshot(ctx, in.ChannelID)
	if errors.Is(err, repository.ErrNotFound) {
		s.fail(w, r, notFound("this channel has no model sync to undo"))
		return
	}
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	if snap.Undone {
		s.fail(w, r, conflict("the last model sync of this channel was already undone"))
		return
	}
	if err := s.restoreBindings(ctx, in.ChannelID, snap); err != nil {
		s.fail(w, r, internal(err))
		return
	}
	for _, id := range snap.AddedModels {
		s.reapSyncedModel(ctx, in.ChannelID, id)
	}
	if err := s.Repo.MarkModelSyncSnapshotUndone(ctx, snap.ID); err != nil {
		s.fail(w, r, internal(err))
		return
	}
	s.auditEvent(ctx, "undo_model_sync", r, map[string]string{"channel": in.ChannelID, "snapshot": snap.ID})
	s.notifyConfigChange(ctx)
	s.write(w, r, http.StatusOK, map[string]any{
		"channel_id": in.ChannelID, "snapshot_id": snap.ID,
		"restored": len(snap.Bindings), "removed_models": len(snap.AddedModels),
	})
}

// restoreBindings rewrites a channel's bindings to their pre-sync state inside
// one transaction (or straight through on repositories without transactions).
func (s *Server) restoreBindings(ctx context.Context, channelID string, snap domain.ModelSyncSnapshot) error {
	apply := func(exec interface {
		DeleteProviderModelsByChannel(context.Context, string) error
		CreateProviderModel(context.Context, domain.ProviderModel) error
	}) error {
		if err := exec.DeleteProviderModelsByChannel(ctx, channelID); err != nil {
			return err
		}
		for _, pm := range snap.Bindings {
			pm.ChannelID = channelID
			if err := exec.CreateProviderModel(ctx, pm); err != nil {
				return fmt.Errorf("restore %s: %w", pm.ModelID, err)
			}
		}
		return nil
	}
	if storeWithTx, ok := s.Repo.(interface {
		WithTx(context.Context, func(*repository.Tx) error) error
	}); ok {
		return storeWithTx.WithTx(ctx, func(tx *repository.Tx) error { return apply(tx) })
	}
	return apply(s.Repo)
}

// newSnapshotID names a snapshot; UUIDs keep them unique across restarts.
func newSnapshotID() string { return uuid.NewString() }

// snapshotCreatedAt is the clock, injectable for tests.
var snapshotCreatedAt = func() time.Time { return time.Now().UTC() }
