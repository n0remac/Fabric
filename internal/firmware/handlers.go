package firmware

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"
)

const apiPrefix = "/api/firmware/v1"
const maxMetadataBytes = 16 << 10

type Handler struct {
	Store  *Store
	Access AccessConfig
}

func (h *Handler) Mount(mux *http.ServeMux) {
	firmwareMux := http.NewServeMux()
	firmwareMux.HandleFunc("GET /api/firmware/v1/{device}/manifest", h.manifest)
	firmwareMux.HandleFunc("GET /api/firmware/v1/{device}/latest", h.latest)
	firmwareMux.HandleFunc("GET /api/firmware/v1/{device}/builds/{id}/download", h.download)
	firmwareMux.HandleFunc("POST /api/firmware/v1/{device}/builds", h.publish)
	firmwareMux.HandleFunc("PUT /api/firmware/v1/{device}/stable", h.promote)
	mux.Handle(apiPrefix+"/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Vary", "Authorization")
		values := r.Header.Values("Authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || len(values[0]) > 512 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="firmware"`)
			writeError(w, 401, "unauthorized", "firmware credentials are required")
			return
		}
		credential, ok := h.Access.Authenticate(strings.TrimPrefix(values[0], "Bearer "))
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="firmware"`)
			writeError(w, 401, "unauthorized", "invalid firmware credentials")
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, apiPrefix+"/"), "/")
		if parts[0] != "x3" {
			writeError(w, 403, "forbidden", "credential does not authorize this device")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && credential.Role != "publisher" {
			writeError(w, 403, "forbidden", "publisher credentials are required")
			return
		}
		firmwareMux.ServeHTTP(w, r)
	}))
}

func (h *Handler) manifest(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, h.Store.Manifest())
}
func (h *Handler) latest(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = "stable"
	}
	b, err := h.Store.Latest(channel)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, 200, b)
}
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	b, f, err := h.Store.Open(r.PathValue("id"))
	if err != nil {
		respondError(w, err)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.Size() != b.Size {
		writeError(w, 500, "storage_failed", "stored image is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+b.ID+`.bin"`)
	w.Header().Set("ETag", `"`+b.SHA256+`"`)
	http.ServeContent(w, r, b.ID+".bin", b.CreatedAt, f)
}

func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(5 * time.Minute))
	defer controller.SetReadDeadline(time.Time{})
	r.Body = http.MaxBytesReader(w, r.Body, MaxImageBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, 415, "unsupported_media_type", "multipart/form-data is required")
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		respondError(w, invalidUpload(err))
		return
	}
	if part.FormName() != "metadata" {
		writeError(w, 422, "invalid_firmware", "first part must be metadata")
		return
	}
	data, err := io.ReadAll(io.LimitReader(part, maxMetadataBytes+1))
	if err != nil {
		respondError(w, invalidUpload(err))
		return
	}
	if len(data) > maxMetadataBytes {
		writeError(w, 413, "upload_too_large", "metadata exceeds limit")
		return
	}
	var metadata Metadata
	if err := decodeJSON(strings.NewReader(string(data)), &metadata); err != nil {
		writeError(w, 422, "invalid_firmware", "invalid upload metadata")
		return
	}
	part, err = reader.NextPart()
	if err != nil {
		respondError(w, invalidUpload(err))
		return
	}
	if part.FormName() != "firmware" {
		writeError(w, 422, "invalid_firmware", "second part must be firmware")
		return
	}
	// Require the closing multipart boundary before committing any registry changes.
	source := &multipartImage{Reader: part, finish: func() error {
		if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
			return ErrInvalid
		}
		return nil
	}}
	b, err := h.Store.Publish(metadata, source)
	if err != nil {
		log.Printf("firmware publication rejected: %v", err)
		respondError(w, err)
		return
	}
	log.Printf("firmware published device=x3 build=%s", b.ID)
	writeJSON(w, 201, b)
}

type multipartImage struct {
	io.Reader
	finish func() error
	done   bool
}

func (m *multipartImage) Read(p []byte) (int, error) {
	n, err := m.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, invalidUpload(err)
	}
	if errors.Is(err, io.EOF) && !m.done {
		m.done = true
		if e := m.finish(); e != nil {
			return n, e
		}
	}
	return n, err
}
func invalidUpload(err error) error {
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		return ErrTooLarge
	}
	return ErrInvalid
}

func (h *Handler) promote(w http.ResponseWriter, r *http.Request) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		writeError(w, 415, "unsupported_media_type", "application/json is required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMetadataBytes)
	var request struct {
		BuildID string `json:"build_id"`
	}
	if err := decodeJSON(r.Body, &request); err != nil {
		respondError(w, invalidUpload(err))
		return
	}
	b, err := h.Store.Promote(request.BuildID)
	if err != nil {
		respondError(w, err)
		return
	}
	log.Printf("firmware promoted device=x3 build=%s", b.ID)
	writeJSON(w, 200, b)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
func respondError(w http.ResponseWriter, err error) {
	var limit *http.MaxBytesError
	switch {
	case errors.Is(err, ErrTooLarge), errors.As(err, &limit):
		writeError(w, 413, "upload_too_large", "firmware exceeds upload limit")
	case errors.Is(err, ErrInvalid):
		writeError(w, 422, "invalid_firmware", err.Error())
	case errors.Is(err, ErrNotFound):
		writeError(w, 404, "build_not_found", "firmware build is unavailable")
	default:
		log.Printf("firmware storage failed: %v", err)
		writeError(w, 500, "storage_failed", "firmware storage failed")
	}
}
