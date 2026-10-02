package nodes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryCredentialLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	r, err := Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	token, digest, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	n := Node{ID: "x3-pocket", Type: "xteink-x3", Name: "Pocket Reader", Enabled: true, Permissions: []string{"pages.read", "firmware.read"}, Attributes: map[string]string{"firmware_channel": "dev"}}
	if err := r.Add(n, digest); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Authenticate(token); !ok {
		t.Fatal("valid credential rejected")
	}
	if _, ok := r.Authenticate(strings.Repeat("0", 64)); ok {
		t.Fatal("unknown credential accepted")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), token) {
		t.Fatal("raw credential persisted")
	}
	r2, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r2.Get(n.ID); !ok || got.Name != n.Name || !Has(got, "firmware.read") {
		t.Fatal("node did not survive reload")
	}
	if err := r2.SetEnabled(n.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Authenticate(token); ok {
		t.Fatal("other registry instance did not observe disable")
	}
	if _, ok := r2.Authenticate(token); ok {
		t.Fatal("disabled node authenticated")
	}
	if err := r2.SetEnabled(n.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := r2.Revoke(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := r2.Authenticate(token); ok {
		t.Fatal("revoked node authenticated")
	}
}

func TestStateIsScopedByNodeAndSession(t *testing.T) {
	s := NewState()
	s.Set("x3-pocket", "stocks", "selected", "NVDA")
	s.Set("laptop", "stocks", "selected", "VOO")
	s.SetSession("x3-pocket", "session-a", "page", "stocks")
	if got, _ := s.Get("x3-pocket", "stocks", "selected"); got != "NVDA" {
		t.Fatal(got)
	}
	if got, _ := s.Get("laptop", "stocks", "selected"); got != "VOO" {
		t.Fatal(got)
	}
	if _, ok := s.GetSession("laptop", "session-a", "page"); ok {
		t.Fatal("session crossed node boundary")
	}
	if _, ok := s.GetSession("x3-pocket", "session-b", "page"); ok {
		t.Fatal("session crossed session boundary")
	}
}
