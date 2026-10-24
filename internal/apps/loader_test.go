package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validManifest = `{"fabric_app":"0.1","id":"homelab","name":"Homelab","entrypoint":"main.fabric","pages":["overview"],"resources":["homelab.services"]}`

func makeBundle(t *testing.T, root, folder, manifest, script string) string {
	t.Helper()
	path := filepath.Join(root, folder)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ManifestFile), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "main.fabric"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDirectoryDiscoversBundleAndNamespacedPages(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, root, "homelab", validManifest, "def pages():\n    return {}\n")
	catalog, err := LoadDirectory(root, []string{"system", "stocks"})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Bundles) != 1 {
		t.Fatalf("bundles: %+v", catalog)
	}
	got := catalog.Bundles[0]
	if got.Manifest.ID != "homelab" || len(got.PageIDs) != 1 || got.PageIDs[0] != "homelab-overview" {
		t.Fatalf("bundle: %+v", got)
	}
	if !strings.Contains(string(got.Source), "def pages") {
		t.Fatal("source missing")
	}
	if got.EntrypointPath() != filepath.Join(root, "homelab", "main.fabric") {
		t.Fatal("wrong entrypoint path")
	}
}

func TestLoadDirectoryRejectsInvalidAppWithoutLosingValidApp(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, root, "homelab", validManifest, "not evaluated by discovery")
	makeBundle(t, root, "broken", `{"fabric_app":"0.1","id":"broken","name":"Broken","entrypoint":"main.fabric","pages":["overview"],"unknown":"value"}`, "noop")
	catalog, err := LoadDirectory(root, nil)
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("want diagnostic, got %v", err)
	}
	if len(catalog.Bundles) != 1 || catalog.Bundles[0].Manifest.ID != "homelab" {
		t.Fatalf("partial invalid bundle published: %+v", catalog)
	}
}

func TestManifestValidation(t *testing.T) {
	cases := []struct{ name, data, want string }{
		{"version", strings.Replace(validManifest, `"0.1"`, `"9.9"`, 1), "schema"},
		{"unknown field", strings.Replace(validManifest, `"pages"`, `"badfield"`, 1), "schema"},
		{"duplicate field", strings.Replace(validManifest, `"name":"Homelab"`, `"name":"One","name":"Two"`, 1), "duplicate"},
		{"duplicate page", strings.Replace(validManifest, `["overview"]`, `["overview","overview"]`, 1), "schema"},
		{"duplicate resource", strings.Replace(validManifest, `["homelab.services"]`, `["homelab.services","homelab.services"]`, 1), "schema"},
		{"traversal", strings.Replace(validManifest, `"main.fabric"`, `"../secret.fabric"`, 1), "schema"},
		{"absolute entrypoint", strings.Replace(validManifest, `"main.fabric"`, `"/tmp/exploit.fabric"`, 1), "schema"},
		{"unrecognized extension", strings.Replace(validManifest, `"main.fabric"`, `"main.py"`, 1), "schema"},
		{"resource invalid", strings.Replace(validManifest, `"homelab.services"`, `"https://example.org"`, 1), "schema"},
		{"JSON array", `[]`, "object"},
		{"trailing", validManifest + `{}`, "one JSON object"},
		{"malformed", `{"fabric_app":`, "EOF"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeManifest([]byte(test.data))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestFileSafetyAndLimits(t *testing.T) {
	t.Run("missing entrypoint", func(t *testing.T) {
		root := t.TempDir()
		path := makeBundle(t, root, "homelab", validManifest, "ok")
		if err := os.Remove(filepath.Join(path, "main.fabric")); err != nil {
			t.Fatal(err)
		}
		_, err := loadBundle(path)
		if err == nil || !strings.Contains(err.Error(), "main.fabric") {
			t.Fatalf("expected missing script: %v", err)
		}
	})
	t.Run("script symlink", func(t *testing.T) {
		root := t.TempDir()
		path := makeBundle(t, root, "homelab", validManifest, "ok")
		outside := filepath.Join(root, "secret")
		if err := os.WriteFile(outside, []byte("secrets"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(path, "main.fabric")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(path, "main.fabric")); err != nil {
			t.Fatal(err)
		}
		if _, err := loadBundle(path); err == nil || !strings.Contains(err.Error(), "symlinks forbidden") {
			t.Fatalf("expected symlink refusal: %v", err)
		}
	})
	t.Run("manifest symlink", func(t *testing.T) {
		root := t.TempDir()
		path := makeBundle(t, root, "homelab", validManifest, "ok")
		if err := os.Remove(filepath.Join(path, ManifestFile)); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(root, "manifest-copy.json")
		if err := os.WriteFile(outside, []byte(validManifest), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(path, ManifestFile)); err != nil {
			t.Fatal(err)
		}
		if _, err := loadBundle(path); err == nil {
			t.Fatal("accepted symlink manifest")
		}
	})
	t.Run("app folder symlink", func(t *testing.T) {
		root, target := t.TempDir(), t.TempDir()
		makeBundle(t, target, "homelab", validManifest, "ok")
		if err := os.Symlink(filepath.Join(target, "homelab"), filepath.Join(root, "homelab")); err != nil {
			t.Fatal(err)
		}
		catalog, err := LoadDirectory(root, nil)
		if err == nil || len(catalog.Bundles) != 0 {
			t.Fatalf("accepted folder symlink: %+v %v", catalog, err)
		}
	})
	t.Run("oversized manifest", func(t *testing.T) {
		root := t.TempDir()
		path := makeBundle(t, root, "homelab", validManifest, "ok")
		if err := os.WriteFile(filepath.Join(path, ManifestFile), []byte(strings.Repeat(" ", int(MaxManifestBytes)+1)), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadBundle(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("no size limit: %v", err)
		}
	})
	t.Run("oversized script", func(t *testing.T) {
		root := t.TempDir()
		path := makeBundle(t, root, "homelab", validManifest, strings.Repeat("x", int(MaxScriptBytes)+1))
		if _, err := loadBundle(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("no size limit: %v", err)
		}
	})
	t.Run("empty script", func(t *testing.T) {
		root := t.TempDir()
		path := makeBundle(t, root, "homelab", validManifest, " \n ")
		if _, err := loadBundle(path); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("accepted empty script: %v", err)
		}
	})
}

func TestCollisionsAndPageIDLimit(t *testing.T) {
	t.Run("reserved page", func(t *testing.T) {
		root := t.TempDir()
		makeBundle(t, root, "homelab", validManifest, "ok")
		catalog, err := LoadDirectory(root, []string{"homelab-overview"})
		if err == nil || !strings.Contains(err.Error(), "collision") || len(catalog.Bundles) != 0 {
			t.Fatalf("collision not rejected: %+v %v", catalog, err)
		}
	})
	t.Run("invalid directory name", func(t *testing.T) {
		root := t.TempDir()
		makeBundle(t, root, "unexpected", validManifest, "ok")
		catalog, err := LoadDirectory(root, nil)
		if err == nil || !strings.Contains(err.Error(), "directory name") || len(catalog.Bundles) != 0 {
			t.Fatalf("directory mismatch allowed: %+v %v", catalog, err)
		}
	})
	t.Run("composed ID too long", func(t *testing.T) {
		root := t.TempDir()
		appID := "a" + strings.Repeat("b", 31)
		pageID := "c" + strings.Repeat("d", 31)
		manifest := fmt.Sprintf(`{"fabric_app":"0.1","id":%q,"name":"Long","entrypoint":"main.fabric","pages":[%q]}`, appID, pageID)
		makeBundle(t, root, appID, manifest, "ok")
		catalog, err := LoadDirectory(root, nil)
		if err == nil || !strings.Contains(err.Error(), "64-character") || len(catalog.Bundles) != 0 {
			t.Fatalf("long ID accepted: %+v %v", catalog, err)
		}
	})
	t.Run("empty root", func(t *testing.T) {
		catalog, err := LoadDirectory(t.TempDir(), nil)
		if err != nil || len(catalog.Bundles) != 0 {
			t.Fatalf("empty root invalid: %+v %v", catalog, err)
		}
	})
}

func TestExampleBundleMatchesRealSchema(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "apps")
	catalog, err := LoadDirectory(root, []string{"system", "stocks", "stock-detail"})
	if err != nil {
		t.Fatalf("shipped app example must pass validation: %v", err)
	}
	if len(catalog.Bundles) != 1 || catalog.Bundles[0].PageIDs[0] != "homelab-overview" {
		t.Fatalf("example bundle: %+v", catalog)
	}
}

func TestNamespacedIDsMayStillCollideAcrossApps(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, root, "a", `{"fabric_app":"0.1","id":"a","name":"A","entrypoint":"main.fabric","pages":["b-c"]}`, "ok")
	makeBundle(t, root, "a-b", `{"fabric_app":"0.1","id":"a-b","name":"A-B","entrypoint":"main.fabric","pages":["c"]}`, "ok")
	catalog, err := LoadDirectory(root, nil)
	if err == nil || !strings.Contains(err.Error(), "collision") || len(catalog.Bundles) != 1 {
		t.Fatalf("ambiguous page IDs accepted: %+v %v", catalog, err)
	}
}
