package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrProviderNotFound = errors.New("provider was not found")

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func (r *Registry) Register(provider Provider) error {
	if provider == nil {
		return errors.New("provider is required")
	}
	name := strings.TrimSpace(provider.Name())
	if name == "" {
		return errors.New("provider name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("provider %q is already registered", name)
	}
	r.providers[name] = provider
	return nil
}

func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[name]
	return ok
}

func (r *Registry) Data(ctx context.Context, name string) (map[string]any, error) {
	r.mu.RLock()
	provider, ok := r.providers[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
	}
	data, err := provider.Data(ctx)
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", name, err)
	}
	if data == nil {
		data = map[string]any{}
	}
	return data, nil
}
