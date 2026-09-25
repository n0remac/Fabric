package providers

import "context"

type Provider interface {
	Name() string
	Data(context.Context) (map[string]any, error)
}
