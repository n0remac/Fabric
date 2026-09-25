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
