package pages

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/n0remac/Fabric/internal/fabric"
)

type testNames map[string]bool

func (n testNames) Has(name string) bool { return n[name] }

func TestStoreLoadsAndRetainsLastKnownGoodSnapshot(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "system.json")
	valid := `{"fabric":"0.1","id":"system","title":"First","layout":{"type":"text","text":"ok"},"data":{"provider":"system","refresh":{"strategy":"manual"}}}`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	validator, err := fabric.NewValidator(testNames{}, testNames{"system": true})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(directory, validator)
	if err != nil {
		t.Fatal(err)
	}
	if page, ok := store.Get("system"); !ok || page.Title != "First" {
		t.Fatalf("unexpected initial page: %+v ok=%v", page, ok)
	}
	if err := os.WriteFile(path, []byte(`{"fabric":"0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reload(); err == nil {
		t.Fatal("expected invalid reload to fail")
	}
	if page, ok := store.Get("system"); !ok || page.Title != "First" {
		t.Fatalf("last-known-good page was not retained: %+v ok=%v", page, ok)
	}
}

func TestStockPagesValidateAndUseConfiguredSymbols(t *testing.T) {
	actions := testNames{"stocks.select": true, "stocks.period.1d": true, "stocks.period.5d": true, "stocks.period.1m": true, "stocks.period.1y": true}
	validator, err := fabric.NewValidator(actions, testNames{"system": true, "stocks": true})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore("../../pages", validator, StockWatchlistTransform([]string{"AAPL", "NVDA"}))
	if err != nil {
		t.Fatal(err)
	}
	watch, ok := store.Get("stocks")
	if !ok {
		t.Fatal("stocks page missing")
	}
	if _, ok := fabric.FindComponent(watch.Layout, "stock_AAPL"); !ok {
		t.Fatal("configured ticker button missing")
	}
	if _, ok := fabric.FindComponent(watch.Layout, "stock_GOOGL"); ok {
		t.Fatal("unconfigured ticker button present")
	}
	detail, ok := store.Get("stock-detail")
	if !ok {
		t.Fatal("stock detail page missing")
	}
	chart := 0
	fabric.Walk(detail.Layout, func(component fabric.Component) bool {
		if component.Type == fabric.ComponentChart {
			chart++
		}
		return true
	})
	if chart != 1 {
		t.Fatalf("want one chart, got %d", chart)
	}
}
