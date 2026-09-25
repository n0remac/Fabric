package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type Handler func(context.Context, map[string]any) error
type ResultHandler func(context.Context, map[string]any) (string, error)

type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	results  map[string]ResultHandler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler), results: make(map[string]ResultHandler)}
}

func (r *Registry) Register(name string, handler Handler) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("action name is required")
	}
	if handler == nil {
		return errors.New("action handler is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[name]; exists || r.results[name] != nil {
		return fmt.Errorf("action %q is already registered", name)
	}
	r.handlers[name] = handler
	return nil
}

func (r *Registry) RegisterResult(name string, handler ResultHandler) error {
	name = strings.TrimSpace(name)
	if name == "" || handler == nil {
		return errors.New("action name and handler are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handlers[name] != nil || r.results[name] != nil {
		return fmt.Errorf("action %q is already registered", name)
	}
	r.results[name] = handler
	return nil
}

func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.handlers[name] != nil || r.results[name] != nil
}

func (r *Registry) Invoke(ctx context.Context, name string, args map[string]any) error {
	_, err := r.InvokeResult(ctx, name, args)
	return err
}

func (r *Registry) InvokeResult(ctx context.Context, name string, args map[string]any) (string, error) {
	r.mu.RLock()
	handler, ok := r.handlers[name]
	resultHandler := r.results[name]
	r.mu.RUnlock()
	if resultHandler != nil {
		return resultHandler(ctx, cloneMap(args))
	}
	if !ok {
		return "", fmt.Errorf("action %q is not registered", name)
	}
	return "", handler(ctx, cloneMap(args))
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = cloneValue(value)
	}
	return output
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		output := make([]any, len(typed))
		for index, item := range typed {
			output[index] = cloneValue(item)
		}
		return output
	default:
		return value
	}
}
