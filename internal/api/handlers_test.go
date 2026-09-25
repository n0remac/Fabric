package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0remac/Fabric/internal/actions"
	"github.com/n0remac/Fabric/internal/fabric"
	"github.com/n0remac/Fabric/internal/pages"
	"github.com/n0remac/Fabric/internal/providers"
)

type testProvider struct{}

func (testProvider) Name() string { return "system" }
func (testProvider) Data(context.Context) (map[string]any, error) {
	return map[string]any{"system": map[string]any{"cpu": 18}}, nil
}

func TestPageDataAndActionAPIAreSeparated(t *testing.T) {
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
	if err := providerRegistry.Register(testProvider{}); err != nil {
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
	(&Handler{Pages: store, Providers: providerRegistry, Dispatcher: dispatcher}).Mount(mux)

	pageResponse := request(t, mux, http.MethodGet, "/api/pages/system", "", "")
	if pageResponse.Code != http.StatusOK || pageResponse.Header().Get("ETag") == "" || !strings.Contains(pageResponse.Body.String(), `"fabric":"0.1"`) {
		t.Fatalf("unexpected page response: status=%d headers=%v body=%s", pageResponse.Code, pageResponse.Header(), pageResponse.Body.String())
	}
	dataResponse := request(t, mux, http.MethodGet, "/api/pages/system/data", "", "")
	if dataResponse.Code != http.StatusOK || !strings.Contains(dataResponse.Body.String(), `"cpu":18`) || strings.Contains(dataResponse.Body.String(), `"fabric"`) {
		t.Fatalf("unexpected data response: %d %s", dataResponse.Code, dataResponse.Body.String())
	}
	actionResponse := request(t, mux, http.MethodPost, "/api/pages/system/actions", `{"component_id":"refresh"}`, "application/json")
	if actionResponse.Code != http.StatusOK || !strings.Contains(actionResponse.Body.String(), `"type":"refresh"`) || strings.Contains(actionResponse.Body.String(), `"fabric"`) {
		t.Fatalf("unexpected action response: %d %s", actionResponse.Code, actionResponse.Body.String())
	}
	unknownResponse := request(t, mux, http.MethodPost, "/api/pages/system/actions", `{"component_id":"refresh","name":"untrusted"}`, "application/json")
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown client action field to fail, got %d %s", unknownResponse.Code, unknownResponse.Body.String())
	}
}

func request(t *testing.T, handler http.Handler, method, target, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}
