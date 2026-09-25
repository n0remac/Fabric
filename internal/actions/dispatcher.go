package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/n0remac/Fabric/internal/fabric"
)

var (
	ErrPageNotFound      = errors.New("page was not found")
	ErrComponentNotFound = errors.New("component was not found")
	ErrNotActionable     = errors.New("component is not actionable")
)

type PageLookup interface {
	Get(string) (fabric.Page, bool)
}

type DataLookup interface {
	Data(context.Context, string) (map[string]any, error)
}

type Result struct {
	Type   fabric.ActionType `json:"type"`
	PageID string            `json:"page_id,omitempty"`
	Data   map[string]any    `json:"data,omitempty"`
}

type Dispatcher struct {
	Pages     PageLookup
	Providers DataLookup
	Actions   *Registry
}

func (d *Dispatcher) Dispatch(ctx context.Context, pageID, componentID string) (Result, error) {
	page, ok := d.Pages.Get(pageID)
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrPageNotFound, pageID)
	}
	component, ok := fabric.FindComponent(page.Layout, componentID)
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrComponentNotFound, componentID)
	}
	if component.Action == nil {
		return Result{}, fmt.Errorf("%w: %s", ErrNotActionable, componentID)
	}
	action := component.Action
	result := Result{Type: action.Type}
	switch action.Type {
	case fabric.ActionNavigate:
		if _, ok := d.Pages.Get(action.Page); !ok {
			return Result{}, fmt.Errorf("%w: %s", ErrPageNotFound, action.Page)
		}
		result.PageID = action.Page
		return result, nil
	case fabric.ActionBack:
		return result, nil
	case fabric.ActionInvoke:
		if d.Actions == nil {
			return Result{}, errors.New("action registry is unavailable")
		}
		if err := d.Actions.Invoke(ctx, action.Name, action.Args); err != nil {
			return Result{}, err
		}
	case fabric.ActionRefresh:
	default:
		return Result{}, fmt.Errorf("unsupported action type %q", action.Type)
	}
	data, err := pageData(ctx, page, d.Providers)
	if err != nil {
		return Result{}, err
	}
	result.PageID = page.ID
	result.Data = data
	return result, nil
}

func pageData(ctx context.Context, page fabric.Page, providers DataLookup) (map[string]any, error) {
	if page.Data == nil {
		return map[string]any{}, nil
	}
	if providers == nil {
		return nil, errors.New("provider registry is unavailable")
	}
	return providers.Data(ctx, page.Data.Provider)
}
