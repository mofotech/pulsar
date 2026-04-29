package compute

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/config"
	networkhandler "github.com/agomez/pulsar/internal/controller/network"
	"github.com/agomez/pulsar/internal/controller/registry"
	storagehandler "github.com/agomez/pulsar/internal/controller/storage"
	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/internal/store/postgres"
	"github.com/agomez/pulsar/pkg/id"
)

// consoleToken is a short-lived, single-use token granting WebSocket access to a VNC port.
type consoleToken struct {
	vncAddr   string
	expiresAt time.Time
}

// Handler exposes compute REST endpoints.
type Handler struct {
	svc          *Service
	log          *zap.Logger
	reg          *registry.AgentRegistry
	vncProxyHost string
	cmu          sync.Mutex
	consoleToks  map[string]consoleToken
}

func NewHandler(
	cfg *config.Config,
	store *etcd.Client,
	db *postgres.DB,
	reg *registry.AgentRegistry,
	netSvc *networkhandler.Service,
	storageSvc *storagehandler.Service,
	log *zap.Logger,
) *Handler {
	svc := NewService(store, reg, netSvc, storageSvc, log)
	return NewHandlerWithService(cfg, svc, reg, log)
}

// NewHandlerWithService constructs a Handler using a pre-built Service, registering
// the task-result handler on the registry.
func NewHandlerWithService(cfg *config.Config, svc *Service, reg *registry.AgentRegistry, log *zap.Logger) *Handler {
	reg.RegisterTaskResultHandler("compute", svc.HandleTaskResult)
	vncHost := cfg.Controller.ConsoleVNCHost
	if vncHost == "" {
		vncHost = "host.docker.internal"
	}
	return &Handler{
		svc:          svc,
		log:          log,
		reg:          reg,
		vncProxyHost: vncHost,
		consoleToks:  make(map[string]consoleToken),
	}
}

// ─── Instances ────────────────────────────────────────────────────────────────

func (h *Handler) ListInstances(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	instances, err := h.svc.ListInstances(r.Context(), projectID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, instances, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	var req CreateInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}

	projectID := api.ProjectIDFromContext(r.Context())
	userID := api.UserIDFromContext(r.Context())
	inst, err := h.svc.CreateInstance(r.Context(), projectID, userID, req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusAccepted, inst, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetInstance(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	projectID := api.ProjectIDFromContext(r.Context())
	inst, err := h.svc.GetInstance(r.Context(), projectID, instanceID)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "instance not found", middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, inst, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	projectID := api.ProjectIDFromContext(r.Context())
	if err := h.svc.DeleteInstance(r.Context(), projectID, instanceID); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "instance not found", middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) InstanceAction(w http.ResponseWriter, r *http.Request) {
	var req InstanceActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	instanceID := chi.URLParam(r, "id")
	projectID := api.ProjectIDFromContext(r.Context())
	if err := h.svc.PerformAction(r.Context(), projectID, instanceID, req.Action); err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "ACTION_FAILED", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusAccepted, map[string]string{"action": req.Action, "status": "accepted"}, middleware.GetReqID(r.Context()))
}

func (h *Handler) ResetInstance(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	projectID := api.ProjectIDFromContext(r.Context())
	inst, err := h.svc.ResetInstance(r.Context(), projectID, instanceID)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "RESET_FAILED", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, inst, middleware.GetReqID(r.Context()))
}

// ─── Instance Metadata ────────────────────────────────────────────────────────

func (h *Handler) UpdateMetadata(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	projectID := api.ProjectIDFromContext(r.Context())
	var metadata map[string]string
	if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	inst, err := h.svc.UpdateMetadata(r.Context(), projectID, instanceID, metadata)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, inst, middleware.GetReqID(r.Context()))
}

func (h *Handler) SetMetadataKey(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")
	projectID := api.ProjectIDFromContext(r.Context())
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	inst, err := h.svc.SetMetadataKey(r.Context(), projectID, instanceID, key, body.Value)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, inst, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteMetadataKey(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")
	projectID := api.ProjectIDFromContext(r.Context())
	inst, err := h.svc.DeleteMetadataKey(r.Context(), projectID, instanceID, key)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, inst, middleware.GetReqID(r.Context()))
}

func (h *Handler) MigrateInstance(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	projectID := api.ProjectIDFromContext(r.Context())
	var req struct {
		TargetHost string `json:"target_host"`
		Live       bool   `json:"live"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if req.TargetHost == "" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "target_host required", middleware.GetReqID(r.Context()))
		return
	}
	if err := h.svc.MigrateInstance(r.Context(), projectID, instanceID, req.TargetHost, req.Live); err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "MIGRATE_FAILED", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "migrating"}, middleware.GetReqID(r.Context()))
}

func (h *Handler) ResizeInstance(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	projectID := api.ProjectIDFromContext(r.Context())
	var req ResizeInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if req.FlavorID == "" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "flavor_id required", middleware.GetReqID(r.Context()))
		return
	}
	inst, err := h.svc.ResizeInstance(r.Context(), projectID, instanceID, req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "RESIZE_FAILED", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusAccepted, inst, middleware.GetReqID(r.Context()))
}

// ─── Network interfaces ───────────────────────────────────────────────────────

func (h *Handler) AttachInterface(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	projectID := api.ProjectIDFromContext(r.Context())
	var req struct {
		NetworkID string `json:"network_id"`
		PortID    string `json:"port_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if req.NetworkID == "" && req.PortID == "" {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "network_id or port_id required", middleware.GetReqID(r.Context()))
		return
	}
	port, err := h.svc.AttachInterface(r.Context(), projectID, instanceID, req.NetworkID, req.PortID)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "ATTACH_FAILED", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusCreated, port, middleware.GetReqID(r.Context()))
}

func (h *Handler) DetachInterface(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	portID := chi.URLParam(r, "port_id")
	projectID := api.ProjectIDFromContext(r.Context())
	if err := h.svc.DetachInterface(r.Context(), projectID, instanceID, portID); err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "DETACH_FAILED", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Console ──────────────────────────────────────────────────────────────────

var wsUpgrader = websocket.Upgrader{
	CheckOrigin:  func(r *http.Request) bool { return true },
	Subprotocols: []string{"binary"},
}

// GetConsole issues a short-lived token for the WebSocket VNC proxy.
func (h *Handler) GetConsole(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	projectID := api.ProjectIDFromContext(r.Context())

	vncAddr, err := h.svc.RequestConsole(r.Context(), projectID, instanceID)
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "CONSOLE_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}

	// Replace 0.0.0.0 host (reported by agent) with the configured proxy host.
	if strings.HasPrefix(vncAddr, "0.0.0.0:") {
		vncAddr = h.vncProxyHost + ":" + strings.TrimPrefix(vncAddr, "0.0.0.0:")
	}

	tok := id.New()
	h.cmu.Lock()
	h.consoleToks[tok] = consoleToken{vncAddr: vncAddr, expiresAt: time.Now().Add(30 * time.Second)}
	h.cmu.Unlock()

	// Clean up expired tokens in the background.
	go h.sweepTokens()

	api.WriteJSON(w, http.StatusOK, map[string]string{
		"token":   tok,
		"ws_path": fmt.Sprintf("/v1/compute/console/ws?token=%s", tok),
	}, middleware.GetReqID(r.Context()))
}

// ConsoleProxy upgrades to WebSocket and relays traffic to the VNC TCP socket.
func (h *Handler) ConsoleProxy(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	if tok == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	h.cmu.Lock()
	ct, ok := h.consoleToks[tok]
	if ok {
		delete(h.consoleToks, tok) // single-use
	}
	h.cmu.Unlock()

	if !ok || time.Now().After(ct.expiresAt) {
		http.Error(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}

	ws, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Warn("console ws upgrade failed", zap.Error(err))
		return
	}
	defer ws.Close()

	// Clear HTTP server deadlines — this is now a long-lived connection.
	if nc := ws.NetConn(); nc != nil {
		nc.SetDeadline(time.Time{}) //nolint:errcheck
	}

	vnc, err := net.DialTimeout("tcp", ct.vncAddr, 10*time.Second)
	if err != nil {
		h.log.Error("console dial vnc failed", zap.String("addr", ct.vncAddr), zap.Error(err))
		ws.WriteMessage(websocket.CloseMessage, //nolint:errcheck
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "vnc unreachable"))
		return
	}
	defer vnc.Close()

	h.log.Info("console session started", zap.String("instance", tok[:8]), zap.String("vnc", ct.vncAddr))

	errc := make(chan error, 2)

	// VNC → WebSocket
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := vnc.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					errc <- werr
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()

	// WebSocket → VNC
	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			if _, err := vnc.Write(msg); err != nil {
				errc <- err
				return
			}
		}
	}()

	<-errc
	h.log.Info("console session ended", zap.String("instance", tok[:8]))
}

func (h *Handler) sweepTokens() {
	now := time.Now()
	h.cmu.Lock()
	defer h.cmu.Unlock()
	for k, v := range h.consoleToks {
		if now.After(v.expiresAt) {
			delete(h.consoleToks, k)
		}
	}
}

// ─── Flavors ─────────────────────────────────────────────────────────────────

func (h *Handler) ListFlavors(w http.ResponseWriter, r *http.Request) {
	flavors, err := h.svc.ListFlavors(r.Context())
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, flavors, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateFlavor(w http.ResponseWriter, r *http.Request) {
	var f Flavor
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if err := h.svc.CreateFlavor(r.Context(), &f); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusCreated, f, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetFlavor(w http.ResponseWriter, r *http.Request) {
	f, err := h.svc.GetFlavor(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "flavor not found", middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, f, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteFlavor(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteFlavor(r.Context(), chi.URLParam(r, "id")); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "flavor not found", middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Nodes ────────────────────────────────────────────────────────────────────

func (h *Handler) ListNodes(w http.ResponseWriter, r *http.Request) {
	nodes := h.reg.ListAll()
	type nodeView struct {
		AgentID         string   `json:"agent_id"`
		Pillar          string   `json:"pillar"`
		HypervisorTypes []string `json:"hypervisor_types"`
		VcpusTotal      int32    `json:"vcpus_total"`
		VcpusUsed       int32    `json:"vcpus_used"`
		RamMbTotal      int64    `json:"ram_mb_total"`
		RamMbUsed       int64    `json:"ram_mb_used"`
		LastSeen        string   `json:"last_seen"`
	}
	out := make([]nodeView, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeView{
			AgentID:         n.AgentID,
			Pillar:          n.Pillar,
			HypervisorTypes: n.Capabilities.HypervisorTypes,
			VcpusTotal:      n.Resources.VcpusTotal,
			VcpusUsed:       n.Resources.VcpusUsed,
			RamMbTotal:      n.Resources.RamMbTotal,
			RamMbUsed:       n.Resources.RamMbUsed,
			LastSeen:        n.LastSeen.UTC().Format(time.RFC3339),
		})
	}
	api.WriteJSON(w, http.StatusOK, out, middleware.GetReqID(r.Context()))
}
