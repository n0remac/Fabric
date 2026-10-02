package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromoteAndRejectCredentialRedirect(t *testing.T) {
	token := strings.Repeat("a", 64)
	tokenPath := filepath.Join(t.TempDir(), "publisher.token")
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 32)
	for _, redirect := range []bool{false, true} {
		t.Run(map[bool]string{false: "promotion", true: "redirect"}[redirect], func(t *testing.T) {
			leaked := false
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; w.WriteHeader(200) }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "PUT" || r.URL.Path != "/api/firmware/v1/x3/stable" || r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("incorrect promotion request")
				}
				body, _ := io.ReadAll(r.Body)
				var request struct {
					BuildID string `json:"build_id"`
				}
				if err := json.Unmarshal(body, &request); err != nil || request.BuildID != id {
					t.Error("incorrect promotion body")
				}
				if redirect {
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{\"id\":\"" + id + "\"}"))
			}))
			defer server.Close()
			err := run([]string{"promote", "--server", server.URL, "--token-file", tokenPath, "--build-id", id})
			if redirect && err == nil {
				t.Fatal("redirect accepted")
			}
			if !redirect && err != nil {
				t.Fatal(err)
			}
			if leaked {
				t.Fatal("publisher credential request followed redirect")
			}
		})
	}
	for _, origin := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/path", "https://example.com?token=secret"} {
		if err := run([]string{"promote", "--server", origin, "--token-file", tokenPath, "--build-id", id}); err == nil {
			t.Fatalf("unsafe origin accepted: %s", origin)
		}
	}
}
