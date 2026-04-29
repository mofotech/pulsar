package image

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/api"
)

const maxUploadSize = 20 << 30 // 20 GiB

// Handler exposes image REST endpoints.
type Handler struct {
	svc       *Service
	publicURL string
	log       *zap.Logger
}

func NewHandler(svc *Service, publicURL string, log *zap.Logger) *Handler {
	return &Handler{svc: svc, publicURL: publicURL, log: log}
}

// List handles GET /v1/images
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	images, err := h.svc.List(r.Context())
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if images == nil {
		images = []*Image{}
	}
	api.WriteJSON(w, http.StatusOK, images, middleware.GetReqID(r.Context()))
}

// Get handles GET /v1/images/{id}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	img, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "image not found", middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, img, middleware.GetReqID(r.Context()))
}

// Create handles POST /v1/images
// Accepts either:
//   - multipart/form-data with fields: name, format, file
//   - application/json with fields: name, format, url  (URL registration)
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")

	// Detect multipart upload
	isMultipart := len(ct) >= 9 && ct[:9] == "multipart"
	if isMultipart {
		h.createFromUpload(w, r)
		return
	}
	h.createFromURL(w, r)
}

func (h *Handler) createFromUpload(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid multipart form", reqID)
		return
	}

	name := r.FormValue("name")
	format := r.FormValue("format")
	if name == "" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required", reqID)
		return
	}
	if format == "" {
		format = "qcow2"
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", fmt.Sprintf("file field missing: %v", err), reqID)
		return
	}
	defer file.Close()

	img := &Image{
		Name:      name,
		Format:    format,
		Status:    "queued",
		CreatedAt: time.Now().UTC(),
	}
	if err := h.svc.Create(r.Context(), img); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}

	// Stream to disk
	n, err := h.svc.StoreFile(img.ID, format, io.LimitReader(file, maxUploadSize))
	if err != nil {
		img.Status = "error"
		h.svc.Update(r.Context(), img) //nolint:errcheck
		api.WriteError(w, http.StatusInternalServerError, "STORAGE_ERROR", err.Error(), reqID)
		return
	}

	img.Status = "active"
	img.SizeBytes = n
	img.URL = fmt.Sprintf("%s/v1/images/%s/content", h.publicURL, img.ID)
	if err := h.svc.Update(r.Context(), img); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}

	h.log.Info("image uploaded", zap.String("id", img.ID), zap.String("name", img.Name), zap.Int64("bytes", n))
	api.WriteJSON(w, http.StatusCreated, img, reqID)
}

func (h *Handler) createFromURL(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.GetReqID(r.Context())
	var req struct {
		Name   string `json:"name"`
		Format string `json:"format"`
		URL    string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}
	if req.Name == "" || req.URL == "" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name and url are required", reqID)
		return
	}
	if req.Format == "" {
		req.Format = "qcow2"
	}
	img := &Image{
		Name:   req.Name,
		Format: req.Format,
		Status: "active",
		URL:    req.URL,
	}
	if err := h.svc.Create(r.Context(), img); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), reqID)
		return
	}
	api.WriteJSON(w, http.StatusCreated, img, reqID)
}

// Delete handles DELETE /v1/images/{id}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "image not found", middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Content handles GET /v1/images/{id}/content — streams the image file.
// No authentication required: agents need to fetch images without a user JWT.
func (h *Handler) Content(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	img, err := h.svc.Get(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if img.Status != "active" {
		http.Error(w, "image not active", http.StatusConflict)
		return
	}

	rc, size, err := h.svc.OpenFile(id, img.Format)
	if err != nil {
		h.log.Error("serve image file", zap.String("id", id), zap.Error(err))
		http.Error(w, "image file not found", http.StatusNotFound)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, img.Name, img.Format))
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}
	io.Copy(w, rc) //nolint:errcheck
}
