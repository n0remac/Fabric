package simulator

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0remac/Fabric/internal/actions"
	"github.com/n0remac/Fabric/internal/fabric"
	"github.com/n0remac/Fabric/internal/pages"
	"github.com/n0remac/Fabric/internal/providers"
)

func TestSimulatorRefreshReturnsOnlyPreviewFragment(t *testing.T) {
	directory := t.TempDir()
	document := `{
      "fabric":"0.1","id":"system","title":"System",
      "layout":{"type":"column","children":[
        {"type":"metric","label":"CPU","bind":"system.cpu","suffix":"%"},
        {"type":"button","id":"refresh","label":"Refresh","action":{"type":"refresh"}}
      ]},
      "data":{"provider":"system","refresh":{"strategy":"manual"}}
    }`
	if err := os.WriteFile(filepath.Join(directory, "system.json"), []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	actionRegistry := actions.NewRegistry()
	providerRegistry := providers.NewRegistry()
	if err := providerRegistry.Register(broadcastProvider{}); err != nil {
		t.Fatal(err)
	}
	validator, err := fabric.NewValidator(actionRegistry, providerRegistry)
	if err != nil {
		t.Fatal(err)
	}
	store, err := pages.NewStore(directory, validator)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &actions.Dispatcher{Pages: store, Providers: providerRegistry, Actions: actionRegistry}
	mux := http.NewServeMux()
	(&Handler{Pages: store, Providers: providerRegistry, Dispatcher: dispatcher, Renderer: NewGoDomRenderer()}).Mount(mux)

	form := url.Values{"component_id": {"refresh"}}
	request := httptest.NewRequest(http.MethodPost, "/simulator/system/actions", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `id="fabric-display"`) || !strings.Contains(body, "18%") || strings.Contains(body, `"fabric"`) || strings.Contains(body, "<html") {
		t.Fatalf("unexpected refresh fragment: %s", body)
	}
}
