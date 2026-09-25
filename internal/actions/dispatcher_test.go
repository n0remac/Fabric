package actions

import (
	"context"
	"errors"
	"testing"

	"github.com/n0remac/Fabric/internal/fabric"
)

type fakePages map[string]fabric.Page

func (f fakePages) Get(id string) (fabric.Page, bool) { page, ok := f[id]; return page, ok }

type fakeData struct{}

func (fakeData) Data(context.Context, string) (map[string]any, error) {
	return map[string]any{"value": 2}, nil
}

func TestDispatcherUsesOnlyDeclaredAction(t *testing.T) {
	registry := NewRegistry()
	called := false
	if err := registry.Register("demo.run", func(_ context.Context, args map[string]any) error {
		called = args["safe"] == true
		args["safe"] = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page := fabric.Page{ID: "demo", Data: &fabric.DataSpec{Provider: "demo"}, Layout: fabric.Component{
		Type: fabric.ComponentColumn,
		Children: []fabric.Component{{Type: fabric.ComponentButton, ID: "run", Label: "Run", Action: &fabric.Action{
			Type: fabric.ActionInvoke, Name: "demo.run", Args: map[string]any{"safe": true},
		}}},
	}}
	dispatcher := Dispatcher{Pages: fakePages{"demo": page}, Providers: fakeData{}, Actions: registry}
	result, err := dispatcher.Dispatch(t.Context(), "demo", "run")
	if err != nil || !called || result.Type != fabric.ActionInvoke || result.Data["value"] != 2 {
		t.Fatalf("result=%+v called=%v err=%v", result, called, err)
	}
	if page.Layout.Children[0].Action.Args["safe"] != true {
		t.Fatal("handler mutated page-declared arguments")
	}
	if _, err := dispatcher.Dispatch(t.Context(), "demo", "undeclared"); !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("expected component lookup failure, got %v", err)
	}
}

func TestDispatcherInvokeCanNavigateToDeclaredResultPage(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterResult("stocks.select", func(_ context.Context, args map[string]any) (string, error) {
		if args["symbol"] != "AAPL" {
			return "", errors.New("unconfigured ticker")
		}
		return "stock-detail", nil
	}); err != nil {
		t.Fatal(err)
	}
	watch := fabric.Page{ID: "stocks", Layout: fabric.Component{Type: fabric.ComponentButton, ID: "stock_AAPL", Label: "AAPL", Action: &fabric.Action{Type: fabric.ActionInvoke, Name: "stocks.select", Args: map[string]any{"symbol": "AAPL"}}}}
	detail := fabric.Page{ID: "stock-detail", Data: &fabric.DataSpec{Provider: "stocks"}}
	dispatcher := Dispatcher{Pages: fakePages{"stocks": watch, "stock-detail": detail}, Providers: fakeData{}, Actions: registry}
	result, err := dispatcher.Dispatch(t.Context(), "stocks", "stock_AAPL")
	if err != nil || result.PageID != "stock-detail" || result.Data["value"] != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := dispatcher.Dispatch(t.Context(), "stocks", "stock_EVIL"); !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("undeclared ticker dispatched: %v", err)
	}
}
