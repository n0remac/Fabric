package simulator

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/n0remac/Fabric/internal/actions"
	"github.com/n0remac/Fabric/internal/auth"
	"github.com/n0remac/Fabric/internal/fabric"
	"github.com/n0remac/Fabric/internal/pages"
	"github.com/n0remac/Fabric/internal/providers"
	html "github.com/n0remac/GoDom/html"
)

type Handler struct {
	Auth       *auth.Middleware
	Pages      *pages.Store
	Providers  *providers.Registry
	Dispatcher *actions.Dispatcher
	Renderer   *GoDomRenderer
}

func (h *Handler) Mount(mux *http.ServeMux) {
	protect := func(permission string, handler http.HandlerFunc) http.Handler {
		if h.Auth == nil {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "node authentication is not configured", http.StatusServiceUnavailable)
			})
		}
		return h.Auth.RequireBrowser(auth.Fixed(permission), handler)
	}
	mux.Handle("GET /simulator", protect("pages.read", h.index))
	mux.Handle("GET /simulator/{id}", protect("pages.read", h.page))
	mux.Handle("GET /simulator/{id}/render", protect("pages.read", h.render))
	mux.Handle("POST /simulator/{id}/actions", protect("actions.invoke", h.action))
	mux.Handle("GET /assets/fabric/{path...}", assetHandler())
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	list := h.Pages.List()
	if len(list) == 0 {
		http.Error(w, "no pages are available", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/simulator/"+list[0].ID, http.StatusTemporaryRedirect)
}

func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	page, data, ok := h.loadPage(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	preview, err := h.Renderer.Render(page, data)
	if err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	serveNode(w, simulatorPage(page, h.Pages.List(), preview))
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request) {
	page, data, ok := h.loadPage(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	preview, err := h.Renderer.Render(page, data)
	if err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	serveNode(w, preview)
}

func (h *Handler) action(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	pageID := r.PathValue("id")
	componentID := strings.TrimSpace(r.FormValue("component_id"))
	if pageID == "" || componentID == "" {
		http.Error(w, "page and component are required", http.StatusUnprocessableEntity)
		return
	}
	result, err := h.Dispatcher.Dispatch(r.Context(), pageID, componentID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, actions.ErrPageNotFound) || errors.Is(err, actions.ErrComponentNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, actions.ErrNotActionable) {
			status = http.StatusUnprocessableEntity
		}
		http.Error(w, err.Error(), status)
		return
	}
	if result.PageID != "" && result.PageID != pageID {
		h.redirect(w, r, "/simulator/"+result.PageID)
		return
	}
	switch result.Type {
	case fabric.ActionNavigate:
		h.redirect(w, r, "/simulator/"+result.PageID)
		return
	case fabric.ActionBack:
		h.redirect(w, r, "/simulator")
		return
	}
	page, ok := h.Pages.Get(pageID)
	if !ok {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	preview, err := h.Renderer.Render(page, result.Data)
	if err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	serveNode(w, preview)
}

func (h *Handler) loadPage(w http.ResponseWriter, r *http.Request, id string) (fabric.Page, map[string]any, bool) {
	page, ok := h.Pages.Get(id)
	if !ok {
		http.NotFound(w, r)
		return fabric.Page{}, nil, false
	}
	data := map[string]any{}
	if page.Data != nil {
		var err error
		data, err = h.Providers.Data(r.Context(), page.Data.Provider)
		if err != nil {
			http.Error(w, fmt.Sprintf("provider failed: %v", err), http.StatusBadGateway)
			return fabric.Page{}, nil, false
		}
	}
	return page, data, true
}

func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, target string) {
	if strings.EqualFold(r.Header.Get("HX-Request"), "true") {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func serveNode(w http.ResponseWriter, node *html.Node) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(node.Render()))
}
