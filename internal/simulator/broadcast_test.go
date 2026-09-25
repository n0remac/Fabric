package simulator

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"
	"github.com/n0remac/Fabric/internal/actions"
	"github.com/n0remac/Fabric/internal/fabric"
	"github.com/n0remac/Fabric/internal/pages"
	"github.com/n0remac/Fabric/internal/providers"
	ws "github.com/n0remac/GoDom/websocket"
)

type broadcastProvider struct{}

func (broadcastProvider) Name() string { return "system" }
func (broadcastProvider) Data(context.Context) (map[string]any, error) {
	return map[string]any{"system": map[string]any{"cpu": 18}}, nil
}

func TestPageChangeBroadcastsRenderedOOBFragment(t *testing.T) {
	directory := t.TempDir()
	pagePath := filepath.Join(directory, "system.json")
	writeBroadcastPage(t, pagePath, "Before")
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

	config := ws.DefaultConfig()
	config.AllowMissingOrigin = true
	config.ValidateRoomID = func(room string) error { return nil }
	hub, err := ws.NewHub(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go hub.Run(ctx)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if errors.Is(err, syscall.EPERM) {
		t.Skip("network listeners are not permitted in this sandbox")
	}
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(ws.Handler(hub, ws.NewRegistry(), ws.Hooks{}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	connection, response, err := gws.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?room=simulator-system", nil)
	if response != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	go store.Watch(ctx, 10*time.Millisecond, func(err error) { t.Errorf("watch: %v", err) })
	go BroadcastChanges(ctx, store, providerRegistry, NewGoDomRenderer(), hub)
	writeBroadcastPage(t, pagePath, "After")

	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, message, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	output := string(message)
	if !strings.Contains(output, "After") || !strings.Contains(output, `hx-swap-oob="outerHTML"`) || strings.Contains(output, `"fabric"`) {
		t.Fatalf("unexpected broadcast: %s", output)
	}
}

func writeBroadcastPage(t *testing.T, path, text string) {
	t.Helper()
	document := `{
      "fabric":"0.1","id":"system","title":"System",
      "layout":{"type":"text","text":"` + text + `"},
      "data":{"provider":"system","refresh":{"strategy":"manual"}}
    }`
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
}
