package keypair

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/api"
)

// Handler exposes keypair CRUD over HTTP.
type Handler struct {
	svc *Service
	log *zap.Logger
}

func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// List   GET /v1/compute/keypairs
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID := api.UserIDFromContext(r.Context())
	reqID := middleware.GetReqID(r.Context())
	kps, err := h.svc.List(r.Context(), userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	if kps == nil {
		kps = []*KeyPair{}
	}
	api.WriteJSON(w, http.StatusOK, kps, reqID)
}

// Create   POST /v1/compute/keypairs
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID := api.UserIDFromContext(r.Context())
	reqID := middleware.GetReqID(r.Context())
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}
	resp, err := h.svc.Create(r.Context(), userID, req)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), reqID)
		return
	}
	api.WriteJSON(w, http.StatusCreated, resp, reqID)
}

// Get   GET /v1/compute/keypairs/{id}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID := api.UserIDFromContext(r.Context())
	reqID := middleware.GetReqID(r.Context())
	kpID := chi.URLParam(r, "id")
	kp, err := h.svc.Get(r.Context(), userID, kpID)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), reqID)
		return
	}
	api.WriteJSON(w, http.StatusOK, kp, reqID)
}

// Delete   DELETE /v1/compute/keypairs/{id}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := api.UserIDFromContext(r.Context())
	reqID := middleware.GetReqID(r.Context())
	kpID := chi.URLParam(r, "id")
	if err := h.svc.Delete(r.Context(), userID, kpID); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
