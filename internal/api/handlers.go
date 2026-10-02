package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/n0remac/Fabric/internal/actions"
	"github.com/n0remac/Fabric/internal/auth"
	"github.com/n0remac/Fabric/internal/fabric"
	"github.com/n0remac/Fabric/internal/nodes"
	"github.com/n0remac/Fabric/internal/pages"
	"github.com/n0remac/Fabric/internal/providers"
	"github.com/n0remac/Fabric/schemas"
)

const maxActionBodyBytes = 16 << 10

type Handler struct {
	Auth       *auth.Middleware
	Pages      *pages.Store
	Providers  *providers.Registry
	Dispatcher *actions.Dispatcher
}

func (h *Handler) Mount(mux *http.ServeMux) {
	protect := func(permission string, handler http.HandlerFunc) http.Handler {
		if h.Auth == nil {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "node authentication is not configured", http.StatusServiceUnavailable)
			})
		}
		return h.Auth.Require(auth.Fixed(permission), handler)
	}
	mux.Handle("GET /api/pages", protect("pages.read", h.listPages))
	mux.Handle("GET /api/pages/{id}", protect("pages.read", h.getPage))
	mux.Handle("GET /api/pages/{id}/data", protect("pages.read", h.getData))
	mux.Handle("POST /api/pages/{id}/actions", protect("actions.invoke", h.postAction))
	mux.HandleFunc("GET /schemas/fabric-page-v0.1.json", serveSchema)
	mux.HandleFunc("GET /schemas/fabric-page-v0.2.json", serveSchemaV02)
}

func (h *Handler) listPages(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{"pages": h.Pages.List()})
}

func (h *Handler) getPage(w http.ResponseWriter, r *http.Request) {
	page, ok := h.Pages.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "page_not_found", "page was not found")
		return
	}
	body, err := json.Marshal(page)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode_failed", "page could not be encoded")
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(body, '\n'))
}

func (h *Handler) getData(w http.ResponseWriter, r *http.Request) {
	page, ok := h.Pages.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "page_not_found", "page was not found")
		return
	}
	data, err := h.pageData(r, page)
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_failed", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, data)
}

func (h *Handler) postAction(w http.ResponseWriter, r *http.Request) {
	if !isJSON(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	var request struct {
		ComponentID string `json:"component_id"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	request.ComponentID = strings.TrimSpace(request.ComponentID)
	if request.ComponentID == "" {
		writeError(w, http.StatusUnprocessableEntity, "component_required", "component_id is required")
		return
	}
	if node, ok := nodes.FromContext(r.Context()); ok {
		resource := r.PathValue("id") + "." + request.ComponentID
		if page, exists := h.Pages.Get(r.PathValue("id")); exists {
			if component, found := fabric.FindComponent(page.Layout, request.ComponentID); found && component.Action != nil && component.Action.Name != "" {
				resource = component.Action.Name
			}
		}
		log.Printf("node=%q action=actions.invoke resource=%q outcome=attempt", node.NodeID, resource)
	}
	result, err := h.Dispatcher.Dispatch(r.Context(), r.PathValue("id"), request.ComponentID)
	if err != nil {
		status, code := http.StatusInternalServerError, "action_failed"
		switch {
		case errors.Is(err, actions.ErrPageNotFound), errors.Is(err, actions.ErrComponentNotFound):
			status, code = http.StatusNotFound, "not_found"
		case errors.Is(err, actions.ErrNotActionable):
			status, code = http.StatusUnprocessableEntity, "not_actionable"
		}
		writeError(w, status, code, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) pageData(r *http.Request, page fabric.Page) (map[string]any, error) {
	if page.Data == nil {
		return map[string]any{}, nil
	}
	return h.Providers.Data(r.Context(), page.Data.Provider)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxActionBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

func serveSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/schema+json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, "fabric-page-v0.1.json", time.Time{}, strings.NewReader(string(schemas.FabricPageV01)))
}

func serveSchemaV02(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/schema+json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, "fabric-page-v0.2.json", time.Time{}, strings.NewReader(string(schemas.FabricPageV02)))
}
