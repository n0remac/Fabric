package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0remac/Fabric/internal/actions"
	"github.com/n0remac/Fabric/internal/auth"
	"github.com/n0remac/Fabric/internal/fabric"
	"github.com/n0remac/Fabric/internal/nodes"
	"github.com/n0remac/Fabric/internal/pages"
	"github.com/n0remac/Fabric/internal/providers"
)

func TestAuthenticatedPageAndActionRoutes(t *testing.T) {
	directory := t.TempDir()
	page := `{"fabric":"0.1","id":"system","title":"System","layout":{"type":"column","children":[{"type":"text","text":"Hello"},{"type":"button","id":"refresh","label":"Refresh","action":{"type":"refresh"}}]},"data":{"provider":"system","refresh":{"strategy":"manual"}}}`
	if err := os.WriteFile(filepath.Join(directory, "system.json"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	actionsRegistry := actions.NewRegistry()
	providerRegistry := providers.NewRegistry()
	if err := providerRegistry.Register(testProvider{}); err != nil {
		t.Fatal(err)
	}
	validator, err := fabric.NewValidator(actionsRegistry, providerRegistry)
	if err != nil {
		t.Fatal(err)
	}
	store, err := pages.NewStore(directory, validator)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := nodes.Open(filepath.Join(t.TempDir(), "nodes.json"), true)
	if err != nil {
		t.Fatal(err)
	}
	token, digest, err := nodes.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Add(nodes.Node{ID: "x3-pocket", Type: "xteink-x3", Name: "Pocket", Enabled: true, Permissions: []string{"pages.read", "actions.invoke"}}, digest); err != nil {
		t.Fatal(err)
	}
	readonly, readonlyDigest, err := nodes.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Add(nodes.Node{ID: "laptop", Type: "desktop", Name: "Laptop", Enabled: true, Permissions: []string{"pages.read"}}, readonlyDigest); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&Handler{Auth: &auth.Middleware{Registry: registry}, Pages: store, Providers: providerRegistry, Dispatcher: &actions.Dispatcher{Pages: store, Providers: providerRegistry, Actions: actionsRegistry}}).Mount(mux)
	call := func(method, path, body, credential string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	if got := call("GET", "/api/pages", "", ""); got.Code != 401 {
		t.Fatal(got.Code)
	}
	if got := call("GET", "/api/pages/system", "", token); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := call("GET", "/api/pages/system/data", "", token); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := call("POST", "/api/pages/system/actions", `{"component_id":"refresh","node_id":"laptop"}`, token); got.Code != 400 {
		t.Fatal("spoofed node_id accepted", got.Code)
	}
	if got := call("POST", "/api/pages/system/actions", `{"component_id":"refresh"}`, readonly); got.Code != 403 {
		t.Fatal(got.Code)
	}
	if got := call("POST", "/api/pages/system/actions", `{"component_id":"refresh"}`, token); got.Code != 200 {
		t.Fatal(got.Code)
	}
}
