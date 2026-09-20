package adapter

import (
	"fmt"
	"sync"

	"relayhub/internal/domain"
)

type Registry struct {
	mu       sync.RWMutex
	adapters map[string]ProviderAdapter
}

func NewRegistry() *Registry {
	return &Registry{
		adapters: make(map[string]ProviderAdapter),
	}
}

func (r *Registry) Register(name string, adapter ProviderAdapter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		return fmt.Errorf("%w: adapter name is required", ErrInvalidConfig)
	}
	if adapter == nil {
		return fmt.Errorf("%w: adapter is nil", ErrInvalidConfig)
	}
	r.adapters[name] = adapter
	return nil
}

func (r *Registry) Resolve(p domain.Provider) (ProviderAdapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 1. If explicit adapter type registered, use it
	if a, ok := r.adapters[p.AdapterType]; ok {
		return a, nil
	}
	// 2. Or if provider ID/Name matches an adapter
	if a, ok := r.adapters[p.ID]; ok {
		return a, nil
	}
	if a, ok := r.adapters[p.Name]; ok {
		return a, nil
	}
	// 3. Fallback to generic if generic
	if p.AdapterType == "generic" || p.AdapterType == "" {
		if a, ok := r.adapters["generic"]; ok {
			return a, nil
		}
	}
	return nil, fmt.Errorf("%w: %s (%s)", ErrAdapterNotFound, p.Name, p.AdapterType)
}
