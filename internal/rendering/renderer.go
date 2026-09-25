package rendering

import "github.com/n0remac/Fabric/internal/fabric"

type Renderer[T any] interface {
	Render(page fabric.Page, data map[string]any) (T, error)
}
