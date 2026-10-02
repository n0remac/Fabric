package auth

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0remac/Fabric/internal/nodes"
)

func TestBearerAuthenticationAndCapability(t *testing.T) {
	r, err := nodes.Open(filepath.Join(t.TempDir(), "nodes.json"), true)
	if err != nil {
		t.Fatal(err)
	}
	token, digest, err := nodes.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Add(nodes.Node{ID: "x3-pocket", Type: "xteink-x3", Name: "Pocket", Enabled: true, Permissions: []string{"pages.read"}}, digest); err != nil {
		t.Fatal(err)
	}
	m := &Middleware{Registry: r}
	handler := m.Require(Fixed("pages.read"), http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n, ok := nodes.FromContext(req.Context())
		if !ok || n.NodeID != "x3-pocket" || n.SessionID != "session-a" {
			t.Errorf("wrong context: %+v", n)
		}
		w.Write([]byte(n.NodeID))
	}))
	for _, tc := range []struct {
		auth   string
		status int
	}{
		{"", 401}, {"Bearer invalid", 401}, {"Basic abc", 401}, {"Bearer " + token, 200},
	} {
		req := httptest.NewRequest("GET", "/api/pages?node_id=laptop", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		req.Header.Set("X-Fabric-Session", "session-a")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%q: %d", tc.auth, w.Code)
		}
		if strings.Contains(w.Body.String(), token) {
			t.Fatal("raw credential in response")
		}
	}
	denied := m.Require(Fixed("firmware.publish"), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler ran") }))
	req := httptest.NewRequest("POST", "/builds", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	denied.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	if err := r.SetEnabled("x3-pocket", false); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestBrowserSessionRemainsBoundToNode(t *testing.T) {
	r, err := nodes.Open(filepath.Join(t.TempDir(), "nodes.json"), true)
	if err != nil {
		t.Fatal(err)
	}
	token, digest, err := nodes.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Add(nodes.Node{ID: "simulator-browser", Type: "browser", Name: "Browser", Enabled: true, Permissions: []string{"pages.read"}}, digest); err != nil {
		t.Fatal(err)
	}
	m := &Middleware{Registry: r}
	handler := m.RequireBrowser(Fixed("pages.read"), http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n, ok := nodes.FromContext(req.Context())
		if !ok || n.NodeID != "simulator-browser" || n.SessionID == "" {
			t.Fatalf("wrong browser session %+v", n)
		}
		w.WriteHeader(200)
	}))
	initial := httptest.NewRequest("GET", "/simulator", nil)
	initial.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("ignored:"+token)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, initial)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("browser login %d", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	next := httptest.NewRequest("GET", "/simulator", nil)
	next.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, next)
	if w.Code != 200 {
		t.Fatalf("cookie auth %d", w.Code)
	}
	if err := r.SetEnabled("simulator-browser", false); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, next)
	if w.Code != 401 {
		t.Fatalf("disabled browser session %d", w.Code)
	}
}
