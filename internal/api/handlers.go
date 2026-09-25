package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/n0remac/Fabric/internal/actions"
	"github.com/n0remac/Fabric/internal/fabric"
	"github.com/n0remac/Fabric/internal/pages"
	"github.com/n0remac/Fabric/internal/providers"
	"github.com/n0remac/Fabric/schemas"
)

const maxActionBodyBytes = 16 << 10

type Handler struct {
	Pages      *pages.Store
	Providers  *providers.Registry
	Dispatcher *actions.Dispatcher
}

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pages", h.listPages)
	mux.HandleFunc("GET /api/pages/{id}", h.getPage)
	mux.HandleFunc("GET /api/pages/{id}/data", h.getData)
	mux.HandleFunc("POST /api/pages/{id}/actions", h.postAction)
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
