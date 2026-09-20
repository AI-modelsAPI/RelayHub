package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"relayhub/internal/domain"
	"relayhub/internal/repository"
)

var (
	ErrDuplicate       = errors.New("duplicate resource")
	ErrMissingProvider = errors.New("provider not found")
	ErrMissingChannel  = errors.New("channel not found")
	ErrMissingModel    = errors.New("model not found")
	ErrMissingGroup    = errors.New("model group not found")
	ErrInvalidResource = errors.New("invalid resource")
)

type Persister interface{ repository.ResourceRepository }

type Service struct {
	mu             sync.RWMutex
	repo           Persister
	Providers      map[string]domain.Provider
	Channels       map[string]domain.Channel
	Models         map[string]domain.Model
	ProviderModels map[string]domain.ProviderModel
	Groups         map[string]domain.ModelGroup
	Members        map[string]map[string]domain.ModelGroupMember
	Routes         map[string]domain.Route
}

func NewService(repos ...Persister) *Service {
	s := &Service{Providers: map[string]domain.Provider{}, Channels: map[string]domain.Channel{}, Models: map[string]domain.Model{}, ProviderModels: map[string]domain.ProviderModel{}, Groups: map[string]domain.ModelGroup{}, Members: map[string]map[string]domain.ModelGroupMember{}, Routes: map[string]domain.Route{}}
	if len(repos) > 0 {
		s.repo = repos[0]
	}
	return s
}

func required(kind, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: %s ID is required", ErrInvalidResource, kind)
	}
	return nil
}
func validStrategy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "priority", "fixed", "weighted", "weighted-round-robin", "round-robin", "latency", "success-rate", "quota-aware", "random":
		return true
	}
	return false
}
func validGroupStrategy(s string) bool { return validStrategy(s) }

func (s *Service) AddProvider(p domain.Provider) error {
	return s.CreateProvider(context.Background(), p)
}
func (s *Service) CreateProvider(ctx context.Context, p domain.Provider) error {
	if err := required("provider", p.ID); err != nil {
		return err
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.AdapterType) == "" || strings.TrimSpace(p.Protocol) == "" {
		return fmt.Errorf("%w: provider name, adapter type, and protocol are required", ErrInvalidResource)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Providers[p.ID]; ok {
		return fmt.Errorf("%w: provider %q", ErrDuplicate, p.ID)
	}
	if s.repo != nil {
		if err := s.repo.CreateProvider(ctx, p); err != nil {
			return err
		}
	}
	s.Providers[p.ID] = p
	return nil
}
func (s *Service) GetProvider(ctx context.Context, id string) (domain.Provider, error) {
	if s.repo != nil {
		return s.repo.GetProvider(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.Providers[id]
	if !ok {
		return p, repository.ErrNotFound
	}
	return p, nil
}
func (s *Service) ListProviders(ctx context.Context) ([]domain.Provider, error) {
	if s.repo != nil {
		return s.repo.ListProviders(ctx)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Provider, 0, len(s.Providers))
	for _, p := range s.Providers {
		out = append(out, p)
	}
	return out, nil
}
func (s *Service) UpdateProvider(ctx context.Context, p domain.Provider) error {
	if err := required("provider", p.ID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Providers[p.ID]; !ok {
		return repository.ErrNotFound
	}
	if s.repo != nil {
		if err := s.repo.UpdateProvider(ctx, p); err != nil {
			return err
		}
	}
	s.Providers[p.ID] = p
	return nil
}
func (s *Service) DeleteProvider(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Providers[id]; !ok {
		return repository.ErrNotFound
	}
	for _, c := range s.Channels {
		if c.ProviderID == id {
			return fmt.Errorf("%w: provider has channels", ErrInvalidResource)
		}
	}
	if s.repo != nil {
		if err := s.repo.DeleteProvider(ctx, id); err != nil {
			return err
		}
	}
	delete(s.Providers, id)
	return nil
}

func (s *Service) AddChannel(c domain.Channel) error { return s.CreateChannel(context.Background(), c) }
func (s *Service) CreateChannel(ctx context.Context, c domain.Channel) error {
	if err := required("channel", c.ID); err != nil {
		return err
	}
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("%w: channel name and base URL are required", ErrInvalidResource)
	}
	if c.Weight < 0 {
		return fmt.Errorf("%w: channel weight must not be negative", ErrInvalidResource)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Channels[c.ID]; ok {
		return fmt.Errorf("%w: channel %q", ErrDuplicate, c.ID)
	}
	if _, ok := s.Providers[c.ProviderID]; !ok {
		return fmt.Errorf("%w: %q", ErrMissingProvider, c.ProviderID)
	}
	if c.Weight == 0 {
		c.Weight = 1
	}
	if s.repo != nil {
		if err := s.repo.CreateChannel(ctx, c); err != nil {
			return err
		}
	}
	s.Channels[c.ID] = c
	return nil
}
func (s *Service) GetChannel(ctx context.Context, id string) (domain.Channel, error) {
	if s.repo != nil {
		return s.repo.GetChannel(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.Channels[id]
	if !ok {
		return c, repository.ErrNotFound
	}
	return c, nil
}
func (s *Service) ListChannels(ctx context.Context, providerID string) ([]domain.Channel, error) {
	if s.repo != nil {
		return s.repo.ListChannels(ctx, providerID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Channel{}
	for _, c := range s.Channels {
		if providerID == "" || c.ProviderID == providerID {
			out = append(out, c)
		}
	}
	return out, nil
}
func (s *Service) UpdateChannel(ctx context.Context, c domain.Channel) error {
	if err := required("channel", c.ID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Channels[c.ID]; !ok {
		return repository.ErrNotFound
	}
	if _, ok := s.Providers[c.ProviderID]; !ok {
		return fmt.Errorf("%w: %q", ErrMissingProvider, c.ProviderID)
	}
	if c.Weight < 0 {
		return fmt.Errorf("%w: channel weight must not be negative", ErrInvalidResource)
	}
	if c.Weight == 0 {
		c.Weight = 1
	}
	if s.repo != nil {
		if err := s.repo.UpdateChannel(ctx, c); err != nil {
			return err
		}
	}
	s.Channels[c.ID] = c
	return nil
}
func (s *Service) DeleteChannel(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Channels[id]; !ok {
		return repository.ErrNotFound
	}
	if s.repo != nil {
		if err := s.repo.DeleteChannel(ctx, id); err != nil {
			return err
		}
	}
	delete(s.Channels, id)
	return nil
}

func (s *Service) AddModel(m domain.Model) error { return s.CreateModel(context.Background(), m) }
func (s *Service) CreateModel(ctx context.Context, m domain.Model) error {
	if err := required("model", m.ID); err != nil {
		return err
	}
	if strings.TrimSpace(m.DisplayName) == "" {
		m.DisplayName = m.ID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Models[m.ID]; ok {
		return fmt.Errorf("%w: model %q", ErrDuplicate, m.ID)
	}
	if s.repo != nil {
		if err := s.repo.CreateModel(ctx, m); err != nil {
			return err
		}
	}
	s.Models[m.ID] = m
	return nil
}
func (s *Service) GetModel(ctx context.Context, id string) (domain.Model, error) {
	if s.repo != nil {
		return s.repo.GetModel(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.Models[id]
	if !ok {
		return m, repository.ErrNotFound
	}
	return m, nil
}
func (s *Service) ListModels(ctx context.Context) ([]domain.Model, error) {
	if s.repo != nil {
		return s.repo.ListModels(ctx)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Model, 0, len(s.Models))
	for _, m := range s.Models {
		out = append(out, m)
	}
	return out, nil
}
func (s *Service) UpdateModel(ctx context.Context, m domain.Model) error {
	if err := required("model", m.ID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Models[m.ID]; !ok {
		return repository.ErrNotFound
	}
	if s.repo != nil {
		if err := s.repo.UpdateModel(ctx, m); err != nil {
			return err
		}
	}
	s.Models[m.ID] = m
	return nil
}
func (s *Service) DeleteModel(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Models[id]; !ok {
		return repository.ErrNotFound
	}
	if s.repo != nil {
		if err := s.repo.DeleteModel(ctx, id); err != nil {
			return err
		}
	}
	delete(s.Models, id)
	return nil
}

func (s *Service) AddProviderModel(m domain.ProviderModel) error {
	return s.CreateProviderModel(context.Background(), m)
}
func (s *Service) CreateProviderModel(ctx context.Context, m domain.ProviderModel) error {
	if err := required("provider model", m.ID); err != nil {
		return err
	}
	if strings.TrimSpace(m.UpstreamModelName) == "" || strings.TrimSpace(m.Protocol) == "" {
		return fmt.Errorf("%w: provider model upstream name and protocol are required", ErrInvalidResource)
	}
	if m.Weight < 0 {
		return fmt.Errorf("%w: provider model weight must not be negative", ErrInvalidResource)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ProviderModels[m.ID]; ok {
		return fmt.Errorf("%w: provider model %q", ErrDuplicate, m.ID)
	}
	if _, ok := s.Providers[m.ProviderID]; !ok {
		return fmt.Errorf("%w: %q", ErrMissingProvider, m.ProviderID)
	}
	if _, ok := s.Models[m.ModelID]; !ok {
		return fmt.Errorf("%w: %q", ErrMissingModel, m.ModelID)
	}
	if m.ChannelID != "" {
		c, ok := s.Channels[m.ChannelID]
		if !ok {
			return fmt.Errorf("%w: %q", ErrMissingChannel, m.ChannelID)
		}
		if c.ProviderID != m.ProviderID {
			return fmt.Errorf("%w: channel %q belongs to provider %q", ErrInvalidResource, m.ChannelID, c.ProviderID)
		}
	}
	if m.Weight == 0 {
		m.Weight = 1
	}
	if s.repo != nil {
		if err := s.repo.CreateProviderModel(ctx, m); err != nil {
			return err
		}
	}
	s.ProviderModels[m.ID] = m
	return nil
}
func (s *Service) GetProviderModel(ctx context.Context, id string) (domain.ProviderModel, error) {
	if s.repo != nil {
		return s.repo.GetProviderModel(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.ProviderModels[id]
	if !ok {
		return m, repository.ErrNotFound
	}
	return m, nil
}
func (s *Service) ListProviderModels(ctx context.Context, modelID string) ([]domain.ProviderModel, error) {
	if s.repo != nil {
		return s.repo.ListProviderModels(ctx, modelID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.ProviderModel{}
	for _, m := range s.ProviderModels {
		if modelID == "" || m.ModelID == modelID {
			out = append(out, m)
		}
	}
	return out, nil
}
func (s *Service) UpdateProviderModel(ctx context.Context, m domain.ProviderModel) error {
	if err := required("provider model", m.ID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ProviderModels[m.ID]; !ok {
		return repository.ErrNotFound
	}
	if m.ChannelID != "" {
		c, ok := s.Channels[m.ChannelID]
		if !ok {
			return fmt.Errorf("%w: %q", ErrMissingChannel, m.ChannelID)
		}
		if c.ProviderID != m.ProviderID {
			return fmt.Errorf("%w: provider/channel mismatch", ErrInvalidResource)
		}
	}
	if s.repo != nil {
		if err := s.repo.UpdateProviderModel(ctx, m); err != nil {
			return err
		}
	}
	s.ProviderModels[m.ID] = m
	return nil
}
func (s *Service) DeleteProviderModel(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ProviderModels[id]; !ok {
		return repository.ErrNotFound
	}
	if s.repo != nil {
		if err := s.repo.DeleteProviderModel(ctx, id); err != nil {
			return err
		}
	}
	delete(s.ProviderModels, id)
	return nil
}

func (s *Service) AddModelGroup(g domain.ModelGroup) error {
	return s.CreateModelGroup(context.Background(), g)
}
func (s *Service) CreateModelGroup(ctx context.Context, g domain.ModelGroup) error {
	if err := required("model group", g.ID); err != nil {
		return err
	}
	if strings.TrimSpace(g.Name) == "" || !validGroupStrategy(g.Strategy) {
		return fmt.Errorf("%w: invalid model group", ErrInvalidResource)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Groups[g.ID]; ok {
		return fmt.Errorf("%w: model group %q", ErrDuplicate, g.ID)
	}
	for _, existing := range s.Groups {
		if existing.Name == g.Name {
			return fmt.Errorf("%w: model group name %q", ErrDuplicate, g.Name)
		}
	}
	if g.FallbackGroupID != "" {
		if g.FallbackGroupID == g.ID {
			return fmt.Errorf("%w: fallback cycle", ErrInvalidResource)
		}
		if _, ok := s.Groups[g.FallbackGroupID]; !ok {
			return fmt.Errorf("%w: fallback group %q", ErrMissingGroup, g.FallbackGroupID)
		}
	}
	if s.repo != nil {
		if err := s.repo.CreateModelGroup(ctx, g); err != nil {
			return err
		}
	}
	s.Groups[g.ID] = g
	s.Members[g.ID] = map[string]domain.ModelGroupMember{}
	return nil
}
func (s *Service) GetModelGroup(ctx context.Context, id string) (domain.ModelGroup, error) {
	if s.repo != nil {
		return s.repo.GetModelGroup(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.Groups[id]
	if !ok {
		return g, repository.ErrNotFound
	}
	return g, nil
}
func (s *Service) ListModelGroups(ctx context.Context) ([]domain.ModelGroup, error) {
	if s.repo != nil {
		return s.repo.ListModelGroups(ctx)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.ModelGroup, 0, len(s.Groups))
	for _, g := range s.Groups {
		out = append(out, g)
	}
	return out, nil
}
func (s *Service) UpdateModelGroup(ctx context.Context, g domain.ModelGroup) error {
	if err := required("model group", g.ID); err != nil {
		return err
	}
	if strings.TrimSpace(g.Name) == "" || !validGroupStrategy(g.Strategy) {
		return fmt.Errorf("%w: invalid model group", ErrInvalidResource)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Groups[g.ID]; !ok {
		return repository.ErrNotFound
	}
	for id, existing := range s.Groups {
		if id != g.ID && existing.Name == g.Name {
			return fmt.Errorf("%w: model group name %q", ErrDuplicate, g.Name)
		}
	}
	if g.FallbackGroupID != "" {
		if g.FallbackGroupID == g.ID {
			return fmt.Errorf("%w: fallback cycle", ErrInvalidResource)
		}
		if _, ok := s.Groups[g.FallbackGroupID]; !ok {
			return fmt.Errorf("%w: fallback group %q", ErrMissingGroup, g.FallbackGroupID)
		}
		seen := map[string]bool{g.ID: true}
		for next := g.FallbackGroupID; next != ""; next = s.Groups[next].FallbackGroupID {
			if seen[next] {
				return fmt.Errorf("%w: fallback cycle", ErrInvalidResource)
			}
			seen[next] = true
		}
	}
	if s.repo != nil {
		if err := s.repo.UpdateModelGroup(ctx, g); err != nil {
			return err
		}
	}
	s.Groups[g.ID] = g
	return nil
}
func (s *Service) DeleteModelGroup(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Groups[id]; !ok {
		return repository.ErrNotFound
	}
	if s.repo != nil {
		if err := s.repo.DeleteModelGroup(ctx, id); err != nil {
			return err
		}
	}
	delete(s.Groups, id)
	delete(s.Members, id)
	return nil
}
func (s *Service) AddModelGroupMember(m domain.ModelGroupMember) error {
	return s.AddModelGroupMemberContext(context.Background(), m)
}
func (s *Service) AddModelGroupMemberContext(ctx context.Context, m domain.ModelGroupMember) error {
	if err := required("group member", m.GroupID); err != nil {
		return err
	}
	if err := required("group member", m.ModelID); err != nil {
		return err
	}
	if m.Weight < 0 {
		return fmt.Errorf("%w: member weight must not be negative", ErrInvalidResource)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Groups[m.GroupID]; !ok {
		return fmt.Errorf("%w: %q", ErrMissingGroup, m.GroupID)
	}
	if _, ok := s.Models[m.ModelID]; !ok {
		return fmt.Errorf("%w: %q", ErrMissingModel, m.ModelID)
	}
	if s.Members[m.GroupID] == nil {
		s.Members[m.GroupID] = map[string]domain.ModelGroupMember{}
	}
	if _, ok := s.Members[m.GroupID][m.ModelID]; ok {
		return fmt.Errorf("%w: group member", ErrDuplicate)
	}
	if m.Weight == 0 {
		m.Weight = 1
	}
	if s.repo != nil {
		if err := s.repo.AddModelGroupMember(ctx, m); err != nil {
			return err
		}
	}
	s.Members[m.GroupID][m.ModelID] = m
	return nil
}
func (s *Service) ListModelGroupMembers(ctx context.Context, groupID string) ([]domain.ModelGroupMember, error) {
	if s.repo != nil {
		return s.repo.ListModelGroupMembers(ctx, groupID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.ModelGroupMember{}
	for _, m := range s.Members[groupID] {
		out = append(out, m)
	}
	return out, nil
}
func (s *Service) RemoveModelGroupMember(ctx context.Context, groupID, modelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Members[groupID][modelID]; !ok {
		return repository.ErrNotFound
	}
	if s.repo != nil {
		if err := s.repo.RemoveModelGroupMember(ctx, groupID, modelID); err != nil {
			return err
		}
	}
	delete(s.Members[groupID], modelID)
	return nil
}

func (s *Service) AddRoute(r domain.Route) error { return s.CreateRoute(context.Background(), r) }
func (s *Service) CreateRoute(ctx context.Context, r domain.Route) error {
	if err := required("route", r.ID); err != nil {
		return err
	}
	if strings.TrimSpace(r.Protocol) == "" || (strings.TrimSpace(r.ModelPattern) == "" && r.GroupID == "") {
		return fmt.Errorf("%w: route protocol and target are required", ErrInvalidResource)
	}
	if !validStrategy(r.Strategy) {
		return fmt.Errorf("%w: invalid route strategy", ErrInvalidResource)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Routes[r.ID]; ok {
		return fmt.Errorf("%w: route %q", ErrDuplicate, r.ID)
	}
	if r.GroupID != "" {
		if _, ok := s.Groups[r.GroupID]; !ok {
			return fmt.Errorf("%w: %q", ErrMissingGroup, r.GroupID)
		}
		if r.ModelPattern != "" && r.ModelPattern != "*" {
			return fmt.Errorf("%w: group route cannot have model pattern %q", ErrInvalidResource, r.ModelPattern)
		}
	} else if r.ModelPattern != "" && r.ModelPattern != "*" {
		if _, ok := s.Models[r.ModelPattern]; !ok {
			return fmt.Errorf("%w: route model %q", ErrMissingModel, r.ModelPattern)
		}
	}
	for _, existing := range s.Routes {
		if existing.Enabled && r.Enabled && existing.Protocol == r.Protocol && existing.ModelPattern == r.ModelPattern && existing.GroupID == r.GroupID {
			return fmt.Errorf("%w: conflicting route %q", ErrDuplicate, existing.ID)
		}
	}
	if s.repo != nil {
		if err := s.repo.CreateRoute(ctx, r); err != nil {
			return err
		}
	}
	s.Routes[r.ID] = r
	return nil
}
func (s *Service) GetRoute(ctx context.Context, id string) (domain.Route, error) {
	if s.repo != nil {
		return s.repo.GetRoute(ctx, id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.Routes[id]
	if !ok {
		return r, repository.ErrNotFound
	}
	return r, nil
}
func (s *Service) ListRoutes(ctx context.Context) ([]domain.Route, error) {
	if s.repo != nil {
		return s.repo.ListRoutes(ctx)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Route, 0, len(s.Routes))
	for _, r := range s.Routes {
		out = append(out, r)
	}
	return out, nil
}
func (s *Service) UpdateRoute(ctx context.Context, r domain.Route) error {
	if err := required("route", r.ID); err != nil {
		return err
	}
	if strings.TrimSpace(r.Protocol) == "" || (strings.TrimSpace(r.ModelPattern) == "" && r.GroupID == "") || !validStrategy(r.Strategy) {
		return fmt.Errorf("%w: invalid route", ErrInvalidResource)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Routes[r.ID]; !ok {
		return repository.ErrNotFound
	}
	if r.GroupID != "" {
		if _, ok := s.Groups[r.GroupID]; !ok {
			return fmt.Errorf("%w: %q", ErrMissingGroup, r.GroupID)
		}
		if r.ModelPattern != "" && r.ModelPattern != "*" {
			return fmt.Errorf("%w: group route cannot have model pattern %q", ErrInvalidResource, r.ModelPattern)
		}
	} else if r.ModelPattern != "" && r.ModelPattern != "*" {
		if _, ok := s.Models[r.ModelPattern]; !ok {
			return fmt.Errorf("%w: route model %q", ErrMissingModel, r.ModelPattern)
		}
	}
	for id, existing := range s.Routes {
		if id != r.ID && existing.Enabled && r.Enabled && existing.Protocol == r.Protocol && existing.ModelPattern == r.ModelPattern && existing.GroupID == r.GroupID {
			return fmt.Errorf("%w: conflicting route %q", ErrDuplicate, existing.ID)
		}
	}
	if s.repo != nil {
		if err := s.repo.UpdateRoute(ctx, r); err != nil {
			return err
		}
	}
	s.Routes[r.ID] = r
	return nil
}
func (s *Service) DeleteRoute(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Routes[id]; !ok {
		return repository.ErrNotFound
	}
	if s.repo != nil {
		if err := s.repo.DeleteRoute(ctx, id); err != nil {
			return err
		}
	}
	delete(s.Routes, id)
	return nil
}

// Load refreshes the in-memory request snapshot from the persistent repository.
func (s *Service) Load(ctx context.Context) error {
	if s.repo == nil {
		return nil
	}
	providers, err := s.repo.ListProviders(ctx)
	if err != nil {
		return err
	}
	channels, err := s.repo.ListChannels(ctx, "")
	if err != nil {
		return err
	}
	models, err := s.repo.ListModels(ctx)
	if err != nil {
		return err
	}
	providerModels, err := s.repo.ListProviderModels(ctx, "")
	if err != nil {
		return err
	}
	groups, err := s.repo.ListModelGroups(ctx)
	if err != nil {
		return err
	}
	routes, err := s.repo.ListRoutes(ctx)
	if err != nil {
		return err
	}
	members := map[string]map[string]domain.ModelGroupMember{}
	for _, g := range groups {
		ms, e := s.repo.ListModelGroupMembers(ctx, g.ID)
		if e != nil {
			return e
		}
		members[g.ID] = map[string]domain.ModelGroupMember{}
		for _, m := range ms {
			members[g.ID][m.ModelID] = m
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Providers = map[string]domain.Provider{}
	for _, p := range providers {
		s.Providers[p.ID] = p
	}
	s.Channels = map[string]domain.Channel{}
	for _, c := range channels {
		s.Channels[c.ID] = c
	}
	s.Models = map[string]domain.Model{}
	for _, m := range models {
		s.Models[m.ID] = m
	}
	s.ProviderModels = map[string]domain.ProviderModel{}
	for _, m := range providerModels {
		s.ProviderModels[m.ID] = m
	}
	s.Groups = map[string]domain.ModelGroup{}
	for _, g := range groups {
		s.Groups[g.ID] = g
	}
	s.Members = members
	s.Routes = map[string]domain.Route{}
	for _, r := range routes {
		s.Routes[r.ID] = r
	}
	return nil
}

func (s *Service) Snapshot() (map[string]domain.Provider, map[string]domain.Channel, map[string]domain.Model, map[string]domain.ProviderModel, map[string]domain.ModelGroup, map[string]map[string]domain.ModelGroupMember, map[string]domain.Route) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := cloneProviders(s.Providers)
	c := cloneChannels(s.Channels)
	m := cloneModels(s.Models)
	pm := cloneProviderModels(s.ProviderModels)
	g := cloneGroups(s.Groups)
	r := cloneRoutes(s.Routes)
	members := make(map[string]map[string]domain.ModelGroupMember, len(s.Members))
	for gid, ms := range s.Members {
		members[gid] = map[string]domain.ModelGroupMember{}
		for id, v := range ms {
			members[gid][id] = v
		}
	}
	return p, c, m, pm, g, members, r
}
func cloneProviders(in map[string]domain.Provider) map[string]domain.Provider {
	out := make(map[string]domain.Provider, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneChannels(in map[string]domain.Channel) map[string]domain.Channel {
	out := make(map[string]domain.Channel, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneModels(in map[string]domain.Model) map[string]domain.Model {
	out := make(map[string]domain.Model, len(in))
	for k, v := range in {
		v.Aliases = append([]string(nil), v.Aliases...)
		out[k] = v
	}
	return out
}
func cloneProviderModels(in map[string]domain.ProviderModel) map[string]domain.ProviderModel {
	out := make(map[string]domain.ProviderModel, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneGroups(in map[string]domain.ModelGroup) map[string]domain.ModelGroup {
	out := make(map[string]domain.ModelGroup, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneRoutes(in map[string]domain.Route) map[string]domain.Route {
	out := make(map[string]domain.Route, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
