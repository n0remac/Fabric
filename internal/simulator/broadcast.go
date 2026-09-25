package simulator

import (
	"context"
	"errors"
	"log"

	"github.com/n0remac/Fabric/internal/pages"
	"github.com/n0remac/Fabric/internal/providers"
	html "github.com/n0remac/GoDom/html"
	ws "github.com/n0remac/GoDom/websocket"
)

func BroadcastChanges(ctx context.Context, store *pages.Store, registry *providers.Registry, renderer *GoDomRenderer, hub *ws.Hub) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-store.Events():
			for _, id := range event.Changed {
				page, ok := store.Get(id)
				if !ok {
					continue
				}
				data := map[string]any{}
				if page.Data != nil {
					var err error
					data, err = registry.Data(ctx, page.Data.Provider)
					if err != nil {
						log.Printf("simulator broadcast provider failed for %s: %v", id, err)
						continue
					}
				}
				node, err := renderer.Render(page, data)
				if err != nil {
					log.Printf("simulator broadcast render failed for %s: %v", id, err)
					continue
				}
				node.Init(html.Attr("hx-swap-oob", "outerHTML"))
				if err := hub.BroadcastRoom(roomID(id), []byte(node.Render())); err != nil && !errors.Is(err, ws.ErrRoomNotFound) {
					log.Printf("simulator broadcast failed for %s: %v", id, err)
				}
			}
			for _, id := range event.Removed {
				node := html.Div(
					html.Id("fabric-display"),
					html.Attr("hx-swap-oob", "outerHTML"),
					html.Class("fabric-display fabric-error"),
					html.Text("This page was removed."),
				)
				if err := hub.BroadcastRoom(roomID(id), []byte(node.Render())); err != nil && !errors.Is(err, ws.ErrRoomNotFound) {
					log.Printf("simulator removal broadcast failed for %s: %v", id, err)
				}
			}
		}
	}
}
