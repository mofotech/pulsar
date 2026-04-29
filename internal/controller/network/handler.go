package network

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/api"
)

// Handler exposes network REST endpoints.
type Handler struct {
	svc *Service
	log *zap.Logger
}

func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// ─── Networks ─────────────────────────────────────────────────────────────────

func (h *Handler) ListNetworks(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	nets, err := h.svc.ListNetworks(r.Context(), projectID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if nets == nil {
		nets = []*Network{}
	}
	api.WriteJSON(w, http.StatusOK, nets, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateNetwork(w http.ResponseWriter, r *http.Request) {
	var req CreateNetworkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	projectID := api.ProjectIDFromContext(r.Context())
	net, err := h.svc.CreateNetwork(r.Context(), projectID, req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusAccepted, net, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetNetwork(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	net, err := h.svc.GetNetwork(r.Context(), projectID, chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "network not found", middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, net, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteNetwork(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	if err := h.svc.DeleteNetwork(r.Context(), projectID, chi.URLParam(r, "id")); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "network not found", middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) UpdateNetwork(w http.ResponseWriter, r *http.Request) {
	var req UpdateNetworkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	projectID := api.ProjectIDFromContext(r.Context())
	net, err := h.svc.UpdateNetwork(r.Context(), projectID, chi.URLParam(r, "id"), req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, net, middleware.GetReqID(r.Context()))
}

// ─── Subnets ──────────────────────────────────────────────────────────────────

func (h *Handler) ListSubnets(w http.ResponseWriter, r *http.Request) {
	networkID := r.URL.Query().Get("network_id")
	subs, err := h.svc.ListSubnets(r.Context(), networkID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if subs == nil {
		subs = []*Subnet{}
	}
	api.WriteJSON(w, http.StatusOK, subs, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateSubnet(w http.ResponseWriter, r *http.Request) {
	var req CreateSubnetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	projectID := api.ProjectIDFromContext(r.Context())
	sub, err := h.svc.CreateSubnet(r.Context(), projectID, req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusCreated, sub, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetSubnet(w http.ResponseWriter, r *http.Request) {
	sub, err := h.svc.GetSubnet(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "subnet not found", middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, sub, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteSubnet(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteSubnet(r.Context(), chi.URLParam(r, "id")); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "subnet not found", middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Ports ────────────────────────────────────────────────────────────────────

func (h *Handler) ListPorts(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	networkID := r.URL.Query().Get("network_id")
	ports, err := h.svc.ListPorts(r.Context(), projectID, networkID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if ports == nil {
		ports = []*Port{}
	}
	api.WriteJSON(w, http.StatusOK, ports, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreatePort(w http.ResponseWriter, r *http.Request) {
	var req CreatePortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	projectID := api.ProjectIDFromContext(r.Context())
	port, err := h.svc.CreatePort(r.Context(), projectID, req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusCreated, port, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetPort(w http.ResponseWriter, r *http.Request) {
	port, err := h.svc.GetPort(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "port not found", middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, port, middleware.GetReqID(r.Context()))
}

func (h *Handler) UpdatePort(w http.ResponseWriter, r *http.Request) {
	var req UpdatePortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	port, err := h.svc.UpdatePortSecurityGroups(r.Context(), chi.URLParam(r, "id"), req.SecurityGroupIDs)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, port, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeletePort(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeletePort(r.Context(), chi.URLParam(r, "id")); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "port not found", middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Routers ──────────────────────────────────────────────────────────────────

func (h *Handler) ListRouters(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	routers, err := h.svc.ListRouters(r.Context(), projectID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if routers == nil {
		routers = []*Router{}
	}
	api.WriteJSON(w, http.StatusOK, routers, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateRouter(w http.ResponseWriter, r *http.Request) {
	var req CreateRouterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	projectID := api.ProjectIDFromContext(r.Context())
	router, err := h.svc.CreateRouter(r.Context(), projectID, req.Name)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusAccepted, router, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetRouter(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	router, err := h.svc.GetRouter(r.Context(), projectID, chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "router not found", middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, router, middleware.GetReqID(r.Context()))
}

func (h *Handler) UpdateRouter(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (h *Handler) DeleteRouter(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	if err := h.svc.DeleteRouter(r.Context(), projectID, chi.URLParam(r, "id")); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "router not found", middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AddRouterInterface(w http.ResponseWriter, r *http.Request) {
	var req AddRouterInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	projectID := api.ProjectIDFromContext(r.Context())
	router, err := h.svc.AddRouterInterface(r.Context(), projectID, chi.URLParam(r, "id"), req.SubnetID)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, router, middleware.GetReqID(r.Context()))
}

func (h *Handler) RemoveRouterInterface(w http.ResponseWriter, r *http.Request) {
	var req AddRouterInterfaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	projectID := api.ProjectIDFromContext(r.Context())
	router, err := h.svc.RemoveRouterInterface(r.Context(), projectID, chi.URLParam(r, "id"), req.SubnetID)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, router, middleware.GetReqID(r.Context()))
}

func (h *Handler) SetRouterGateway(w http.ResponseWriter, r *http.Request) {
	var req SetGatewayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	projectID := api.ProjectIDFromContext(r.Context())
	router, err := h.svc.SetRouterGateway(r.Context(), projectID, chi.URLParam(r, "id"), req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, router, middleware.GetReqID(r.Context()))
}

func (h *Handler) ClearRouterGateway(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	router, err := h.svc.ClearRouterGateway(r.Context(), projectID, chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, router, middleware.GetReqID(r.Context()))
}
func (h *Handler) ListFloatingIPs(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	fips, err := h.svc.ListFloatingIPs(r.Context(), projectID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, fips, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateFloatingIP(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	fip, err := h.svc.CreateFloatingIP(r.Context(), projectID, CreateFloatingIPRequest{})
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusCreated, fip, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetFloatingIP(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	fip, err := h.svc.GetFloatingIP(r.Context(), projectID, chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, fip, middleware.GetReqID(r.Context()))
}

func (h *Handler) UpdateFloatingIP(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	fipID := chi.URLParam(r, "id")
	var req UpdateFloatingIPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	var (
		fip *FloatingIP
		err error
	)
	if req.PortID == "" {
		fip, err = h.svc.DisassociateFloatingIP(r.Context(), projectID, fipID)
	} else {
		fip, err = h.svc.AssociateFloatingIP(r.Context(), projectID, fipID, req.PortID)
	}
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, fip, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteFloatingIP(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	if err := h.svc.DeleteFloatingIP(r.Context(), projectID, chi.URLParam(r, "id")); err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) ListSecurityGroups(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	sgs, err := h.svc.ListSecurityGroups(r.Context(), projectID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	if sgs == nil {
		sgs = []*SecurityGroup{}
	}
	api.WriteJSON(w, http.StatusOK, sgs, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateSecurityGroup(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	var req CreateSecurityGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	sg, err := h.svc.CreateSecurityGroup(r.Context(), projectID, req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusCreated, sg, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetSecurityGroup(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	sg, err := h.svc.GetSecurityGroup(r.Context(), projectID, chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, sg, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteSecurityGroup(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	if err := h.svc.DeleteSecurityGroup(r.Context(), projectID, chi.URLParam(r, "id")); err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AddSecurityGroupRule(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	var req AddSecurityGroupRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	rule, err := h.svc.AddSecurityGroupRule(r.Context(), projectID, chi.URLParam(r, "id"), req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusCreated, rule, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteSecurityGroupRule(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	if err := h.svc.DeleteSecurityGroupRule(r.Context(), projectID, chi.URLParam(r, "id"), chi.URLParam(r, "rule_id")); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── FWaaS ────────────────────────────────────────────────────────────────────

func (h *Handler) ListFirewallPolicies(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	policies, err := h.svc.ListFirewallPolicies(r.Context(), projectID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, policies, middleware.GetReqID(r.Context()))
}

func (h *Handler) CreateFirewallPolicy(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	var req CreateFirewallPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	policy, err := h.svc.CreateFirewallPolicy(r.Context(), projectID, req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusCreated, policy, middleware.GetReqID(r.Context()))
}

func (h *Handler) GetFirewallPolicy(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	policy, err := h.svc.GetFirewallPolicy(r.Context(), projectID, chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, policy, middleware.GetReqID(r.Context()))
}

func (h *Handler) UpdateFirewallPolicy(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	var req UpdateFirewallPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	policy, err := h.svc.UpdateFirewallPolicy(r.Context(), projectID, chi.URLParam(r, "id"), req)
	if err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "UNPROCESSABLE", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	api.WriteJSON(w, http.StatusOK, policy, middleware.GetReqID(r.Context()))
}

func (h *Handler) DeleteFirewallPolicy(w http.ResponseWriter, r *http.Request) {
	projectID := api.ProjectIDFromContext(r.Context())
	if err := h.svc.DeleteFirewallPolicy(r.Context(), projectID, chi.URLParam(r, "id")); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), middleware.GetReqID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

