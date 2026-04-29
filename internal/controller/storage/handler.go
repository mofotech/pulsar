package storage

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/api"
)

// Handler exposes storage REST endpoints backed by Service.
type Handler struct {
	svc *Service
	log *zap.Logger
}

func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// ─── Volumes ──────────────────────────────────────────────────────────────────

func (h *Handler) ListVolumes(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	vols, err := h.svc.ListVolumes(r.Context(), projectID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if vols == nil {
		vols = []*Volume{}
	}
	api.WriteJSON(w, http.StatusOK, vols, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateVolume(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	var req CreateVolumeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body", middleware.GetReqID(r.Context()))
		return
	}
	vol, err := h.svc.CreateVolume(r.Context(), projectID, req)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusAccepted, vol, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetVolume(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	volumeID := chi.URLParam(r, "id")
	vol, err := h.svc.GetVolume(r.Context(), projectID, volumeID)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, vol, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteVolume(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	volumeID := chi.URLParam(r, "id")
	if err := h.svc.DeleteVolume(r.Context(), projectID, volumeID); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) VolumeAction(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	volumeID := chi.URLParam(r, "id")
	var req VolumeActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body", middleware.GetReqID(r.Context()))
		return
	}
	vol, err := h.svc.VolumeAction(r.Context(), projectID, volumeID, req)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, vol, middleware.GetReqID(r.Context()))
}

// ─── Snapshots ────────────────────────────────────────────────────────────────

func (h *Handler) ListSnapshots(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	snaps, err := h.svc.ListSnapshots(r.Context(), projectID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if snaps == nil {
		snaps = []*Snapshot{}
	}
	api.WriteJSON(w, http.StatusOK, snaps, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateSnapshot(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	var req CreateSnapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body", middleware.GetReqID(r.Context()))
		return
	}
	snap, err := h.svc.CreateSnapshot(r.Context(), projectID, req)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusAccepted, snap, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetSnapshot(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	snapID := chi.URLParam(r, "id")
	snap, err := h.svc.GetSnapshot(r.Context(), projectID, snapID)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, snap, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	snapID := chi.URLParam(r, "id")
	if err := h.svc.DeleteSnapshot(r.Context(), projectID, snapID); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Volume Types ─────────────────────────────────────────────────────────────

func (h *Handler) ListVolumeTypes(w http.ResponseWriter, r *http.Request) {
	vts, err := h.svc.ListVolumeTypes(r.Context())
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if vts == nil {
		vts = []*VolumeType{}
	}
	api.WriteJSON(w, http.StatusOK, vts, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateVolumeType(w http.ResponseWriter, r *http.Request) {
	var req CreateVolumeTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body", middleware.GetReqID(r.Context()))
		return
	}
	vt, err := h.svc.CreateVolumeType(r.Context(), req)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusCreated, vt, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetVolumeType(w http.ResponseWriter, r *http.Request) {
	vtID := chi.URLParam(r, "id")
	vt, err := h.svc.GetVolumeType(r.Context(), vtID)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, vt, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteVolumeType(w http.ResponseWriter, r *http.Request) {
	vtID := chi.URLParam(r, "id")
	if err := h.svc.DeleteVolumeType(r.Context(), vtID); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
