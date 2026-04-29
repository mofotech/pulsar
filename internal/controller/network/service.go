package network

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	networkpb "github.com/agomez/pulsar/gen/proto/network"
	"github.com/agomez/pulsar/internal/controller/registry"
	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/pkg/id"
)

const (
	networkKeyPrefix       = "/pulsar/network/networks/"
	sharedNetworkKeyPrefix = "/pulsar/network/networks/shared/" // external networks visible to all projects
	subnetKeyPrefix        = "/pulsar/network/subnets/"
	portKeyPrefix          = "/pulsar/network/ports/"
	portIndexKeyPrefix     = "/pulsar/network/ports/idx/" // portID → projectID index
	routerKeyPrefix        = "/pulsar/network/routers/"
	floatingIPKeyPrefix    = "/pulsar/network/floatingips/"
	sgKeyPrefix            = "/pulsar/network/securitygroups/"
	defaultSGKeyPrefix     = "/pulsar/network/defaultsg/" // project_id → sg_id
	vniSeqKey              = "/pulsar/network/vni_seq"
	vniStart               = int64(100)
)

// pgName converts a security group UUID to an OVN port group name.
// OVN port group names must be alphanumeric + underscores.
func pgName(sgID string) string {
	return "pg_" + strings.ReplaceAll(sgID, "-", "_")
}

// pendingNetTask tracks an in-flight network task.
type pendingNetTask struct {
	taskType string
	done     chan taskResult
}

type taskResult struct {
	err    error
	result []byte // raw result payload from agent
}

// Service manages network resource lifecycle: etcd state + OVN task dispatch.
type Service struct {
	store    *etcd.Client
	registry *registry.AgentRegistry
	log      *zap.Logger
	mu       sync.Mutex
	pending  map[string]pendingNetTask // "network:<uuid>" → task
}

func NewService(store *etcd.Client, reg *registry.AgentRegistry, log *zap.Logger) *Service {
	return &Service{
		store:    store,
		registry: reg,
		log:      log,
		pending:  make(map[string]pendingNetTask),
	}
}

// HandleAgentLost is called by the registry when a network agent goes stale.
func (s *Service) HandleAgentLost(rec *registry.AgentRecord) {
	s.log.Warn("network agent lost",
		zap.String("agent_id", rec.AgentID),
	)
}

// HandleTaskResult is registered with the agent registry for the "network" namespace.
func (s *Service) HandleTaskResult(result *agentpb.TaskResult) {
	s.mu.Lock()
	task, ok := s.pending[result.TaskId]
	if ok {
		delete(s.pending, result.TaskId)
	}
	s.mu.Unlock()

	if !ok {
		return
	}
	var err error
	if !result.Success {
		err = fmt.Errorf("network task %s failed: %s", task.taskType, result.ErrorMessage)
	}
	select {
	case task.done <- taskResult{err: err, result: result.Result}:
	default:
	}
}

// dispatchAndWait sends a task to one network agent and blocks until result or timeout.
func (s *Service) dispatchAndWait(ctx context.Context, taskType string, payload interface{}) ([]byte, error) {
	agents := s.registry.ListByPillar("network")
	if len(agents) == 0 {
		return nil, fmt.Errorf("no network agents available")
	}
	raw, _ := json.Marshal(payload)
	taskID := "network:" + id.New()

	done := make(chan taskResult, 1)
	s.mu.Lock()
	s.pending[taskID] = pendingNetTask{
		taskType: taskType,
		done:     done,
	}
	s.mu.Unlock()

	msg := &agentpb.ControllerMessage{
		Payload: &agentpb.ControllerMessage_TaskAssignment{
			TaskAssignment: &agentpb.TaskAssignment{
				TaskId:         taskID,
				TaskType:       taskType,
				Payload:        raw,
				TimeoutSeconds: 30,
			},
		},
	}
	// Send to first available network agent
	if !s.registry.Send("network", agents[0].AgentID, msg) {
		s.mu.Lock()
		delete(s.pending, taskID)
		s.mu.Unlock()
		return nil, fmt.Errorf("failed to send task to network agent %s", agents[0].AgentID)
	}

	select {
	case res := <-done:
		return res.result, res.err
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, taskID)
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

// ─── Network CRUD ─────────────────────────────────────────────────────────────

func (s *Service) CreateNetwork(ctx context.Context, projectID string, req CreateNetworkRequest) (*Network, error) {
	if req.Type == "" {
		req.Type = "vxlan"
	}
	// External networks require a localnet (flat) uplink — force the type.
	if req.External && req.Type == "vxlan" {
		req.Type = "flat"
	}
	n := &Network{
		ID:        id.New(),
		ProjectID: projectID,
		Name:      req.Name,
		Type:      req.Type,
		External:  req.External,
		Status:    "building",
		CreatedAt: time.Now().UTC(),
	}

	if req.Type == "vxlan" {
		vni, err := s.store.IncrCounter(ctx, vniSeqKey, vniStart)
		if err != nil {
			return nil, fmt.Errorf("allocate vni: %w", err)
		}
		n.VNI = int(vni)
	} else if req.Type == "vlan" {
		n.VlanID = req.VlanID
	}

	if err := s.saveNetwork(ctx, n); err != nil {
		return nil, err
	}

	// Dispatch network.create — fire-and-forget (OVN central owns the truth)
	go func() {
		payload := networkpb.CreateNetworkTask{
			Id:       n.ID,
			Name:     n.Name,
			Type:     n.Type,
			Vni:      int32(n.VNI),
			VlanId:   int32(n.VlanID),
			External: n.External,
		}
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.dispatchAndWait(bgCtx, "network.create", &payload); err != nil {
			s.log.Error("network.create failed", zap.String("id", n.ID), zap.Error(err))
			n.Status = "error"
		} else {
			n.Status = "active"
		}
		if err := s.saveNetwork(context.Background(), n); err != nil {
			s.log.Error("save network status", zap.Error(err))
		}
	}()

	return n, nil
}

func (s *Service) GetNetwork(ctx context.Context, projectID, networkID string) (*Network, error) {
	// Try project-scoped key first.
	val, err := s.store.Get(ctx, networkKeyPrefix+projectID+"/"+networkID)
	if err == nil && val != "" {
		var n Network
		return &n, json.Unmarshal([]byte(val), &n)
	}
	// Fall back to shared external networks.
	val, err = s.store.Get(ctx, sharedNetworkKeyPrefix+networkID)
	if err == nil && val != "" {
		var n Network
		return &n, json.Unmarshal([]byte(val), &n)
	}
	return nil, fmt.Errorf("network %s not found", networkID)
}

func (s *Service) ListNetworks(ctx context.Context, projectID string) ([]*Network, error) {
	vals, err := s.store.GetPrefix(ctx, networkKeyPrefix+projectID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]*Network, 0)
	for _, v := range vals {
		var n Network
		if err := json.Unmarshal([]byte(v), &n); err == nil {
			out = append(out, &n)
		}
	}

	// Always include shared external networks (visible to all projects).
	sharedVals, err := s.store.GetPrefix(ctx, sharedNetworkKeyPrefix)
	if err == nil {
		for _, v := range sharedVals {
			var n Network
			if err := json.Unmarshal([]byte(v), &n); err == nil {
				out = append(out, &n)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) DeleteNetwork(ctx context.Context, projectID, networkID string) error {
	// Determine the correct key (external networks are stored under the shared prefix).
	n, err := s.GetNetwork(ctx, projectID, networkID)
	if err != nil {
		return err
	}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		type deleteNetTask struct {
			ID string `json:"id"`
		}
		s.dispatchAndWait(bgCtx, "network.delete", deleteNetTask{ID: networkID}) //nolint:errcheck
	}()
	if n.External {
		return s.store.Delete(ctx, sharedNetworkKeyPrefix+networkID)
	}
	return s.store.Delete(ctx, networkKeyPrefix+projectID+"/"+networkID)
}

func (s *Service) saveNetwork(ctx context.Context, n *Network) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	key := networkKeyPrefix + n.ProjectID + "/" + n.ID
	if n.External {
		key = sharedNetworkKeyPrefix + n.ID
	}
	return s.store.Put(ctx, key, string(data))
}

// UpdateNetwork applies a partial update (name and/or external flag) to a network.
// When the external flag changes the record is moved between the project-scoped and
// shared key prefixes so visibility is immediately consistent.
func (s *Service) UpdateNetwork(ctx context.Context, projectID, networkID string, req UpdateNetworkRequest) (*Network, error) {
	n, err := s.GetNetwork(ctx, projectID, networkID)
	if err != nil {
		return nil, err
	}

	wasExternal := n.External

	if req.Name != nil {
		n.Name = *req.Name
	}
	if req.External != nil {
		n.External = *req.External
	}
	// If the network is now external but was created as vxlan, upgrade it to flat
	// so the OVN localnet port will be present on the next network.create dispatch.
	if n.External && n.Type == "vxlan" {
		n.Type = "flat"
	}

	// If the external flag changed we must delete the old key before saving
	// under the new prefix, otherwise stale records accumulate.
	if req.External != nil && wasExternal != n.External {
		var oldKey string
		if wasExternal {
			oldKey = sharedNetworkKeyPrefix + networkID
		} else {
			oldKey = networkKeyPrefix + projectID + "/" + networkID
		}
		if err := s.store.Delete(ctx, oldKey); err != nil {
			return nil, fmt.Errorf("remove old network key: %w", err)
		}
	}

	if err := s.saveNetwork(ctx, n); err != nil {
		return nil, err
	}

	// If the network just became external, ensure the OVN localnet port exists
	// so the logical switch has a physical uplink.
	if req.External != nil && !wasExternal && n.External {
		go func() {
			type localnetPortTask struct {
				NetworkID string `json:"id"`
				Type      string `json:"type"`
				VlanID    int32  `json:"vlan_id"`
			}
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s.dispatchAndWait(bgCtx, "network.create", &localnetPortTask{ //nolint:errcheck
				NetworkID: n.ID,
				Type:      n.Type,
				VlanID:    int32(n.VlanID),
			})
		}()
	}

	return n, nil
}

// ─── Subnet CRUD ──────────────────────────────────────────────────────────────

func (s *Service) CreateSubnet(ctx context.Context, projectID string, req CreateSubnetRequest) (*Subnet, error) {
	// Look up parent network
	net, err := s.getNetworkByID(ctx, req.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("network not found: %w", err)
	}

	gw := req.GatewayIP
	if gw == "" {
		gw, err = defaultGateway(req.CIDR)
		if err != nil {
			return nil, err
		}
	}

	sub := &Subnet{
		ID:             id.New(),
		ProjectID:      projectID,
		NetworkID:      req.NetworkID,
		Name:           req.Name,
		CIDR:           req.CIDR,
		GatewayIP:      gw,
		AllocationPool: req.AllocationPool,
		DNSNameservers: req.DNSNameservers,
		CreatedAt:      time.Now().UTC(),
	}
	if len(sub.DNSNameservers) == 0 {
		sub.DNSNameservers = []string{"8.8.8.8", "8.8.4.4"}
	}

	// Dispatch subnet.create synchronously — we need the DHCP options UUID before returning
	type createSubnetTask struct {
		NetworkID      string   `json:"network_id"`
		SubnetID       string   `json:"subnet_id"`
		CIDR           string   `json:"cidr"`
		GatewayIP      string   `json:"gateway_ip"`
		DNSNameservers []string `json:"dns_nameservers"`
	}
	raw, err := s.dispatchAndWait(ctx, "network.subnet.create", createSubnetTask{
		NetworkID:      net.ID,
		SubnetID:       sub.ID,
		CIDR:           sub.CIDR,
		GatewayIP:      gw,
		DNSNameservers: sub.DNSNameservers,
	})
	if err != nil {
		return nil, fmt.Errorf("OVN subnet create: %w", err)
	}

	// Extract DHCP options UUID from result
	if len(raw) > 0 {
		var res struct {
			DHCPOptionsUUID string `json:"dhcp_options_uuid"`
		}
		if err := json.Unmarshal(raw, &res); err == nil {
			sub.DHCPOptionsUUID = res.DHCPOptionsUUID
		}
	}

	if err := s.saveSubnet(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

func (s *Service) GetSubnet(ctx context.Context, subnetID string) (*Subnet, error) {
	val, err := s.store.Get(ctx, subnetKeyPrefix+subnetID)
	if err != nil || val == "" {
		return nil, fmt.Errorf("subnet %s not found", subnetID)
	}
	var sub Subnet
	return &sub, json.Unmarshal([]byte(val), &sub)
}

func (s *Service) ListSubnets(ctx context.Context, networkID string) ([]*Subnet, error) {
	vals, err := s.store.GetPrefix(ctx, subnetKeyPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]*Subnet, 0)
	for _, v := range vals {
		var sub Subnet
		if err := json.Unmarshal([]byte(v), &sub); err == nil {
			if networkID == "" || sub.NetworkID == networkID {
				out = append(out, &sub)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) DeleteSubnet(ctx context.Context, subnetID string) error {
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		type deleteSubnetTask struct {
			ID string `json:"id"`
		}
		s.dispatchAndWait(bgCtx, "network.subnet.delete", deleteSubnetTask{ID: subnetID}) //nolint:errcheck
	}()
	return s.store.Delete(ctx, subnetKeyPrefix+subnetID)
}

func (s *Service) saveSubnet(ctx context.Context, sub *Subnet) error {
	data, _ := json.Marshal(sub)
	return s.store.Put(ctx, subnetKeyPrefix+sub.ID, string(data))
}

// ─── Port CRUD ────────────────────────────────────────────────────────────────

// AutoCreatePort creates a new port on networkID for an instance. Called by compute service.
// It picks the first subnet of the network if no subnetID given.
func (s *Service) AutoCreatePort(ctx context.Context, projectID, networkID string) (*PortInfo, error) {
	// Find subnet for this network
	subs, err := s.ListSubnets(ctx, networkID)
	if err != nil || len(subs) == 0 {
		return nil, fmt.Errorf("no subnets on network %s", networkID)
	}
	sub := subs[0]
	port, err := s.createPortInternal(ctx, projectID, networkID, sub)
	if err != nil {
		return nil, err
	}
	ip := ""
	if len(port.FixedIPs) > 0 {
		ip = port.FixedIPs[0].IPAddress
	}
	return &PortInfo{
		ID:         port.ID,
		MACAddress: port.MACAddress,
		FixedIP:    ip,
	}, nil
}

func (s *Service) CreatePort(ctx context.Context, projectID string, req CreatePortRequest) (*Port, error) {
	netObj, err := s.getNetworkByID(ctx, req.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("network not found: %w", err)
	}
	subnetID := req.SubnetID
	var sub *Subnet
	if subnetID != "" {
		sub, err = s.GetSubnet(ctx, subnetID)
		if err != nil {
			return nil, fmt.Errorf("subnet not found: %w", err)
		}
	} else {
		subs, err := s.ListSubnets(ctx, netObj.ID)
		if err != nil || len(subs) == 0 {
			return nil, fmt.Errorf("no subnets on network %s", netObj.ID)
		}
		sub = subs[0]
	}
	return s.createPortInternal(ctx, projectID, req.NetworkID, sub)
}

func (s *Service) createPortInternal(ctx context.Context, projectID, networkID string, sub *Subnet) (*Port, error) {
	// Allocate MAC
	mac, err := GenerateMAC()
	if err != nil {
		return nil, err
	}

	// Load allocated IPs for this subnet
	allocated, err := s.loadAllocatedIPs(ctx, sub.ID)
	if err != nil {
		return nil, err
	}

	ip, err := allocateIP(sub.CIDR, sub.GatewayIP, allocated, sub.AllocationPool)
	if err != nil {
		return nil, err
	}

	port := &Port{
		ID:         id.New(),
		ProjectID:  projectID,
		NetworkID:  networkID,
		SubnetID:   sub.ID,
		MACAddress: mac,
		FixedIPs:   []FixedIP{{SubnetID: sub.ID, IPAddress: ip}},
		Status:     "build",
		CreatedAt:  time.Now().UTC(),
	}

	// Save port first (claims the IP)
	if err := s.savePort(ctx, port); err != nil {
		return nil, err
	}

	// Dispatch port.create synchronously
	type createPortTask struct {
		ID              string `json:"id"`
		NetworkID       string `json:"network_id"`
		MACAddr         string `json:"mac_addr"`
		FixedIP         string `json:"fixed_ip"`
		SubnetID        string `json:"subnet_id"`
		GatewayIP       string `json:"gateway_ip"`
		CIDR            string `json:"cidr"`
		DHCPOptionsUUID string `json:"dhcp_options_uuid"`
	}
	_, err = s.dispatchAndWait(ctx, "network.port.create", createPortTask{
		ID:              port.ID,
		NetworkID:       networkID,
		MACAddr:         mac,
		FixedIP:         ip,
		SubnetID:        sub.ID,
		GatewayIP:       sub.GatewayIP,
		CIDR:            sub.CIDR,
		DHCPOptionsUUID: sub.DHCPOptionsUUID,
	})
	if err != nil {
		// Clean up etcd record on OVN failure
		s.store.Delete(ctx, portKeyPrefix+projectID+"/"+port.ID) //nolint:errcheck
		return nil, fmt.Errorf("OVN port create: %w", err)
	}

	port.Status = "down"  // up when instance boots
	s.savePort(ctx, port) //nolint:errcheck

	// Ensure the project's default security group exists and add this port to it.
	defaultSGID, err := s.EnsureDefaultSecurityGroup(ctx, projectID)
	if err != nil {
		s.log.Warn("could not ensure default security group",
			zap.String("port_id", port.ID),
			zap.String("project_id", projectID),
			zap.Error(err),
		)
	} else {
		if _, err := s.UpdatePortSecurityGroups(ctx, port.ID, []string{defaultSGID}); err != nil {
			s.log.Warn("could not attach default security group to port",
				zap.String("port_id", port.ID),
				zap.String("sg_id", defaultSGID),
				zap.Error(err),
			)
		}
	}

	return port, nil
}

func (s *Service) GetPort(ctx context.Context, portID string) (*Port, error) {
	// Fast path: use the reverse index (populated for ports created after the index was added).
	if projectID, err := s.store.Get(ctx, portIndexKeyPrefix+portID); err == nil && projectID != "" {
		if val, err := s.store.Get(ctx, portKeyPrefix+projectID+"/"+portID); err == nil && val != "" {
			var p Port
			if err := json.Unmarshal([]byte(val), &p); err == nil {
				return &p, nil
			}
		}
	}

	// Slow path: full scan for ports created before the index existed; backfill on hit.
	vals, err := s.store.GetPrefix(ctx, portKeyPrefix)
	if err != nil {
		return nil, err
	}
	for _, v := range vals {
		var p Port
		if err := json.Unmarshal([]byte(v), &p); err == nil && p.ID == portID {
			// Backfill the index so subsequent lookups are fast.
			s.store.Put(ctx, portIndexKeyPrefix+p.ID, p.ProjectID) //nolint:errcheck
			return &p, nil
		}
	}
	return nil, fmt.Errorf("port %s not found", portID)
}

func (s *Service) ListPorts(ctx context.Context, projectID, networkID string) ([]*Port, error) {
	vals, err := s.store.GetPrefix(ctx, portKeyPrefix+projectID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]*Port, 0)
	for _, v := range vals {
		var p Port
		if err := json.Unmarshal([]byte(v), &p); err == nil {
			if networkID == "" || p.NetworkID == networkID {
				out = append(out, &p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) DeletePort(ctx context.Context, portID string) error {
	port, err := s.GetPort(ctx, portID)
	if err != nil {
		return err
	}

	// Disassociate any floating IPs that reference this port before deleting it.
	if fips, err := s.store.GetPrefix(ctx, floatingIPKeyPrefix); err == nil {
		for _, v := range fips {
			var fip FloatingIP
			if err := json.Unmarshal([]byte(v), &fip); err == nil && fip.PortID == portID {
				s.disassociateFloatingIP(ctx, &fip) //nolint:errcheck
			}
		}
	}

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		type deletePortTask struct {
			ID string `json:"id"`
		}
		s.dispatchAndWait(bgCtx, "network.port.delete", deletePortTask{ID: portID}) //nolint:errcheck
	}()
	s.store.Delete(ctx, portIndexKeyPrefix+portID) //nolint:errcheck
	return s.store.Delete(ctx, portKeyPrefix+port.ProjectID+"/"+portID)
}

// BindPort sets requested-chassis so OVN wires the port to the right compute node.
func (s *Service) BindPort(ctx context.Context, portID, chassisID string) error {
	type bindPortTask struct {
		PortID    string `json:"port_id"`
		ChassisID string `json:"chassis_id"`
	}
	_, err := s.dispatchAndWait(ctx, "network.port.bind", bindPortTask{
		PortID:    portID,
		ChassisID: chassisID,
	})
	if err != nil {
		s.log.Warn("port bind failed", zap.String("port", portID), zap.String("chassis", chassisID), zap.Error(err))
	}
	// Update port status
	port, _ := s.GetPort(ctx, portID)
	if port != nil {
		port.Status = "active"
		port.DeviceID = chassisID
		s.savePort(ctx, port) //nolint:errcheck
	}
	return nil // non-fatal
}

// ─── PortInfo (implements compute.NetworkService interface) ───────────────────

// PortInfo is the minimal port data the compute service needs.
type PortInfo struct {
	ID         string
	MACAddress string
	FixedIP    string
}

// GetPortInfo returns minimal port info for compute scheduling.
func (s *Service) GetPortInfo(ctx context.Context, portID string) (*PortInfo, error) {
	port, err := s.GetPort(ctx, portID)
	if err != nil {
		return nil, err
	}
	ip := ""
	if len(port.FixedIPs) > 0 {
		ip = port.FixedIPs[0].IPAddress
	}
	return &PortInfo{
		ID:         port.ID,
		MACAddress: port.MACAddress,
		FixedIP:    ip,
	}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (s *Service) savePort(ctx context.Context, p *Port) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal port: %w", err)
	}
	if err := s.store.Put(ctx, portKeyPrefix+p.ProjectID+"/"+p.ID, string(data)); err != nil {
		return err
	}
	// Maintain portID → projectID reverse index for O(1) GetPort lookups.
	return s.store.Put(ctx, portIndexKeyPrefix+p.ID, p.ProjectID)
}

func (s *Service) loadAllocatedIPs(ctx context.Context, subnetID string) (map[string]struct{}, error) {
	vals, err := s.store.GetPrefix(ctx, portKeyPrefix)
	if err != nil {
		return nil, err
	}
	allocated := make(map[string]struct{})
	for _, v := range vals {
		var p Port
		if err := json.Unmarshal([]byte(v), &p); err != nil {
			continue
		}
		for _, fip := range p.FixedIPs {
			if fip.SubnetID == subnetID {
				allocated[fip.IPAddress] = struct{}{}
			}
		}
	}
	return allocated, nil
}

// getNetworkByID searches all projects for a network with the given ID.
func (s *Service) getNetworkByID(ctx context.Context, networkID string) (*Network, error) {
	vals, err := s.store.GetPrefix(ctx, networkKeyPrefix)
	if err != nil {
		return nil, err
	}
	for _, v := range vals {
		var n Network
		if err := json.Unmarshal([]byte(v), &n); err == nil && n.ID == networkID {
			return &n, nil
		}
	}
	return nil, fmt.Errorf("network %s not found", networkID)
}

func (s *Service) CreateRouter(ctx context.Context, projectID, name string) (*Router, error) {
	r := &Router{
		ID:        id.New(),
		ProjectID: projectID,
		Name:      name,
		Status:    "building",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.saveRouter(ctx, r); err != nil {
		return nil, err
	}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		type createRouterTask struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if _, err := s.dispatchAndWait(bgCtx, "network.router.create", createRouterTask{ID: r.ID, Name: r.Name}); err != nil {
			s.log.Error("router.create failed", zap.String("id", r.ID), zap.Error(err))
			r.Status = "error"
		} else {
			r.Status = "active"
		}
		s.saveRouter(context.Background(), r) //nolint:errcheck
	}()
	return r, nil
}

func (s *Service) GetRouter(ctx context.Context, projectID, routerID string) (*Router, error) {
	val, err := s.store.Get(ctx, routerKeyPrefix+projectID+"/"+routerID)
	if err != nil || val == "" {
		return nil, fmt.Errorf("router %s not found", routerID)
	}
	var r Router
	return &r, json.Unmarshal([]byte(val), &r)
}

func (s *Service) ListRouters(ctx context.Context, projectID string) ([]*Router, error) {
	vals, err := s.store.GetPrefix(ctx, routerKeyPrefix+projectID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]*Router, 0)
	for _, v := range vals {
		var r Router
		if err := json.Unmarshal([]byte(v), &r); err == nil {
			out = append(out, &r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) DeleteRouter(ctx context.Context, projectID, routerID string) error {
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		type deleteRouterTask struct {
			ID string `json:"id"`
		}
		s.dispatchAndWait(bgCtx, "network.router.delete", deleteRouterTask{ID: routerID}) //nolint:errcheck
	}()
	return s.store.Delete(ctx, routerKeyPrefix+projectID+"/"+routerID)
}

// AddRouterInterface connects a subnet's network to the router as an internal interface.
// The router port takes the subnet's gateway IP. If the router already has a gateway,
// a SNAT rule is also added for the subnet CIDR.
func (s *Service) AddRouterInterface(ctx context.Context, projectID, routerID, subnetID string) (*Router, error) {
	r, err := s.GetRouter(ctx, projectID, routerID)
	if err != nil {
		return nil, err
	}
	sub, err := s.GetSubnet(ctx, subnetID)
	if err != nil {
		return nil, fmt.Errorf("subnet not found: %w", err)
	}

	mac, err := GenerateMAC()
	if err != nil {
		return nil, err
	}

	_, ipNet, err := net.ParseCIDR(sub.CIDR)
	if err != nil {
		return nil, err
	}
	ones, _ := ipNet.Mask.Size()
	ipPrefix := fmt.Sprintf("%s/%d", sub.GatewayIP, ones)

	type routerInterfaceAddTask struct {
		RouterID        string `json:"router_id"`
		NetworkID       string `json:"network_id"`
		RouterPortName  string `json:"router_port_name"`
		SwitchPortName  string `json:"switch_port_name"`
		MAC             string `json:"mac"`
		IPPrefix        string `json:"ip_prefix"`
		SNATExternalIP  string `json:"snat_external_ip,omitempty"`
		SNATLogicalCIDR string `json:"snat_logical_cidr,omitempty"`
	}
	task := routerInterfaceAddTask{
		RouterID:       routerID,
		NetworkID:      sub.NetworkID,
		RouterPortName: "lrp-int-" + sub.NetworkID,
		SwitchPortName: "rp-" + routerID,
		MAC:            mac,
		IPPrefix:       ipPrefix,
	}
	if r.ExternalIP != "" {
		task.SNATExternalIP = r.ExternalIP
		task.SNATLogicalCIDR = sub.CIDR
	}
	if _, err := s.dispatchAndWait(ctx, "network.router.interface.add", task); err != nil {
		return nil, fmt.Errorf("OVN router interface add: %w", err)
	}

	for _, sid := range r.InterfaceSubnets {
		if sid == subnetID {
			return r, nil
		}
	}
	r.InterfaceSubnets = append(r.InterfaceSubnets, subnetID)
	s.saveRouter(ctx, r) //nolint:errcheck
	return r, nil
}

// RemoveRouterInterface disconnects a subnet's network from the router.
func (s *Service) RemoveRouterInterface(ctx context.Context, projectID, routerID, subnetID string) (*Router, error) {
	r, err := s.GetRouter(ctx, projectID, routerID)
	if err != nil {
		return nil, err
	}
	sub, err := s.GetSubnet(ctx, subnetID)
	if err != nil {
		return nil, fmt.Errorf("subnet not found: %w", err)
	}

	type routerInterfaceRemoveTask struct {
		RouterID       string `json:"router_id"`
		NetworkID      string `json:"network_id"`
		RouterPortName string `json:"router_port_name"`
		SwitchPortName string `json:"switch_port_name"`
	}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.dispatchAndWait(bgCtx, "network.router.interface.remove", routerInterfaceRemoveTask{ //nolint:errcheck
			RouterID:       routerID,
			NetworkID:      sub.NetworkID,
			RouterPortName: "lrp-int-" + sub.NetworkID,
			SwitchPortName: "rp-" + routerID,
		})
	}()

	updated := r.InterfaceSubnets[:0]
	for _, sid := range r.InterfaceSubnets {
		if sid != subnetID {
			updated = append(updated, sid)
		}
	}
	r.InterfaceSubnets = updated
	s.saveRouter(ctx, r) //nolint:errcheck
	return r, nil
}

// SetRouterGateway attaches an external network to the router as the uplink.
// It allocates an external IP, creates the OVN gateway port, adds a default route,
// and programs SNAT for all currently-attached project subnets.
func (s *Service) SetRouterGateway(ctx context.Context, projectID, routerID string, req SetGatewayRequest) (*Router, error) {
	r, err := s.GetRouter(ctx, projectID, routerID)
	if err != nil {
		return nil, err
	}

	// Validate the requested network is actually external.
	extNet, err := s.getNetworkByID(ctx, req.ExternalNetworkID)
	if err != nil {
		return nil, fmt.Errorf("external network not found: %w", err)
	}
	if !extNet.External {
		return nil, fmt.Errorf("network %s is not an external network", req.ExternalNetworkID)
	}
	allSubs, err := s.ListSubnets(ctx, "")
	if err != nil {
		return nil, err
	}
	var extSub *Subnet
	for _, sub := range allSubs {
		if sub.NetworkID == req.ExternalNetworkID {
			extSub = sub
			break
		}
	}
	if extSub == nil {
		return nil, fmt.Errorf("no subnet found on external network %s", req.ExternalNetworkID)
	}

	// Determine external IP
	extIP := req.ExternalIP
	if extIP == "" {
		allocatedExtIPs, err := s.loadAllocatedIPs(ctx, extSub.ID)
		if err != nil {
			return nil, err
		}
		extIP, err = allocateIP(extSub.CIDR, extSub.GatewayIP, allocatedExtIPs, extSub.AllocationPool)
		if err != nil {
			return nil, fmt.Errorf("allocate external IP: %w", err)
		}
	}

	extMAC, err := GenerateMAC()
	if err != nil {
		return nil, err
	}

	// Claim the external IP via a router port record
	_, extIPNet, _ := net.ParseCIDR(extSub.CIDR)
	extOnes, _ := extIPNet.Mask.Size()
	extIPPrefix := fmt.Sprintf("%s/%d", extIP, extOnes)

	routerExtPort := &Port{
		ID:         id.New(),
		ProjectID:  r.ProjectID,
		NetworkID:  req.ExternalNetworkID,
		SubnetID:   extSub.ID,
		MACAddress: extMAC,
		FixedIPs:   []FixedIP{{SubnetID: extSub.ID, IPAddress: extIP}},
		Status:     "active",
		DeviceID:   "router:" + routerID,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.savePort(ctx, routerExtPort); err != nil {
		return nil, err
	}

	// Collect CIDRs of currently-attached project subnets for SNAT
	var snatCIDRs []string
	for _, sid := range r.InterfaceSubnets {
		if sub, err := s.GetSubnet(ctx, sid); err == nil {
			snatCIDRs = append(snatCIDRs, sub.CIDR)
		}
	}

	type routerGatewaySetTask struct {
		RouterID          string   `json:"router_id"`
		ExternalNetworkID string   `json:"external_network_id"`
		ExtRouterPortName string   `json:"ext_router_port_name"`
		ExtSwitchPortName string   `json:"ext_switch_port_name"`
		ExtMAC            string   `json:"ext_mac"`
		ExtIPPrefix       string   `json:"ext_ip_prefix"`
		NextHopIP         string   `json:"next_hop_ip"`
		SNATExternalIP    string   `json:"snat_external_ip"`
		SNATCIDRs         []string `json:"snat_cidrs"`
	}
	if _, err := s.dispatchAndWait(ctx, "network.router.gateway.set", routerGatewaySetTask{
		RouterID:          routerID,
		ExternalNetworkID: req.ExternalNetworkID,
		ExtRouterPortName: "lrp-ext-" + routerID,
		ExtSwitchPortName: "rp-ext-" + routerID,
		ExtMAC:            extMAC,
		ExtIPPrefix:       extIPPrefix,
		NextHopIP:         extSub.GatewayIP,
		SNATExternalIP:    extIP,
		SNATCIDRs:         snatCIDRs,
	}); err != nil {
		s.store.Delete(ctx, portKeyPrefix+r.ProjectID+"/"+routerExtPort.ID) //nolint:errcheck
		return nil, fmt.Errorf("OVN gateway set: %w", err)
	}

	r.ExternalNetworkID = req.ExternalNetworkID
	r.ExternalIP = extIP
	s.saveRouter(ctx, r) //nolint:errcheck
	return r, nil
}

// ClearRouterGateway removes the external gateway from the router.
func (s *Service) ClearRouterGateway(ctx context.Context, projectID, routerID string) (*Router, error) {
	r, err := s.GetRouter(ctx, projectID, routerID)
	if err != nil {
		return nil, err
	}
	if r.ExternalNetworkID == "" {
		return r, nil
	}

	type routerGatewayClearTask struct {
		RouterID          string `json:"router_id"`
		ExternalNetworkID string `json:"external_network_id"`
		ExtRouterPortName string `json:"ext_router_port_name"`
		ExtSwitchPortName string `json:"ext_switch_port_name"`
		SNATExternalIP    string `json:"snat_external_ip"`
	}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.dispatchAndWait(bgCtx, "network.router.gateway.clear", routerGatewayClearTask{ //nolint:errcheck
			RouterID:          routerID,
			ExternalNetworkID: r.ExternalNetworkID,
			ExtRouterPortName: "lrp-ext-" + routerID,
			ExtSwitchPortName: "rp-ext-" + routerID,
			SNATExternalIP:    r.ExternalIP,
		})
	}()

	// Delete the router's external port record (find it by DeviceID)
	ports, _ := s.ListPorts(ctx, r.ProjectID, r.ExternalNetworkID)
	for _, p := range ports {
		if p.DeviceID == "router:"+routerID {
			s.store.Delete(ctx, portKeyPrefix+r.ProjectID+"/"+p.ID) //nolint:errcheck
		}
	}

	r.ExternalNetworkID = ""
	r.ExternalIP = ""
	s.saveRouter(ctx, r) //nolint:errcheck
	return r, nil
}

func (s *Service) saveRouter(ctx context.Context, r *Router) error {
	data, _ := json.Marshal(r)
	return s.store.Put(ctx, routerKeyPrefix+r.ProjectID+"/"+r.ID, string(data))
}

// ─── Floating IP CRUD ─────────────────────────────────────────────────────────

func (s *Service) ListFloatingIPs(ctx context.Context, projectID string) ([]*FloatingIP, error) {
	vals, err := s.store.GetPrefix(ctx, floatingIPKeyPrefix+projectID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]*FloatingIP, 0)
	for _, v := range vals {
		var fip FloatingIP
		if err := json.Unmarshal([]byte(v), &fip); err == nil {
			out = append(out, &fip)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Service) GetFloatingIP(ctx context.Context, projectID, fipID string) (*FloatingIP, error) {
	val, err := s.store.Get(ctx, floatingIPKeyPrefix+projectID+"/"+fipID)
	if err != nil || val == "" {
		return nil, fmt.Errorf("floating IP %s not found", fipID)
	}
	var fip FloatingIP
	return &fip, json.Unmarshal([]byte(val), &fip)
}

// CreateFloatingIP allocates an IP from any available external network/subnet.
// No OVN DNAT rule is installed yet — that happens on association.
func (s *Service) CreateFloatingIP(ctx context.Context, projectID string, req CreateFloatingIPRequest) (*FloatingIP, error) {
	// Find an external network and subnet automatically.
	extNet, extSub, err := s.findExternalSubnet(ctx)
	if err != nil {
		return nil, err
	}
	_ = extNet

	// Allocate an IP from the external subnet.
	allocatedIPs, err := s.loadAllocatedIPs(ctx, extSub.ID)
	if err != nil {
		return nil, err
	}
	floatingIP, err := allocateIP(extSub.CIDR, extSub.GatewayIP, allocatedIPs, extSub.AllocationPool)
	if err != nil {
		return nil, fmt.Errorf("allocate floating IP: %w", err)
	}

	fipID := id.New()

	// Claim the IP by saving a port record (same pattern as router external port).
	claimMAC, err := GenerateMAC()
	if err != nil {
		return nil, err
	}
	claimPort := &Port{
		ID:         id.New(),
		ProjectID:  projectID,
		NetworkID:  extNet.ID,
		SubnetID:   extSub.ID,
		MACAddress: claimMAC,
		FixedIPs:   []FixedIP{{SubnetID: extSub.ID, IPAddress: floatingIP}},
		Status:     "active",
		DeviceID:   "floatingip:" + fipID,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.savePort(ctx, claimPort); err != nil {
		return nil, err
	}

	fip := &FloatingIP{
		ID:                fipID,
		ProjectID:         projectID,
		ExternalNetworkID: extNet.ID,
		FloatingIPAddress: floatingIP,
		Status:            "down",
	}
	if err := s.saveFloatingIP(ctx, fip); err != nil {
		s.store.Delete(ctx, portKeyPrefix+projectID+"/"+claimPort.ID) //nolint:errcheck
		return nil, err
	}
	return fip, nil
}

// AssociateFloatingIP installs a DNAT_AND_SNAT rule on the router for the given port.
func (s *Service) AssociateFloatingIP(ctx context.Context, projectID, fipID, portID string) (*FloatingIP, error) {
	fip, err := s.GetFloatingIP(ctx, projectID, fipID)
	if err != nil {
		return nil, err
	}

	// Disassociate first if already associated.
	if fip.PortID != "" && fip.PortID != portID {
		if _, err := s.disassociateFloatingIP(ctx, fip); err != nil {
			return nil, err
		}
	}

	port, err := s.GetPort(ctx, portID)
	if err != nil {
		return nil, fmt.Errorf("port %s not found: %w", portID, err)
	}
	if len(port.FixedIPs) == 0 {
		return nil, fmt.Errorf("port %s has no fixed IPs", portID)
	}
	fixedIP := port.FixedIPs[0].IPAddress
	subnetID := port.FixedIPs[0].SubnetID

	// Find a router that has a gateway and an interface on this port's subnet.
	router, err := s.findRouterForSubnet(ctx, projectID, subnetID)
	if err != nil {
		return nil, err
	}

	type floatingIPAssociateTask struct {
		RouterID   string `json:"router_id"`
		FloatingIP string `json:"floating_ip"`
		FixedIP    string `json:"fixed_ip"`
	}
	if _, err := s.dispatchAndWait(ctx, "network.floatingip.associate", floatingIPAssociateTask{
		RouterID:   router.ID,
		FloatingIP: fip.FloatingIPAddress,
		FixedIP:    fixedIP,
	}); err != nil {
		return nil, fmt.Errorf("OVN floatingip associate: %w", err)
	}

	fip.PortID = portID
	fip.FixedIPAddress = fixedIP
	fip.RouterID = router.ID
	fip.Status = "active"
	s.saveFloatingIP(ctx, fip) //nolint:errcheck
	return fip, nil
}

// DisassociateFloatingIP removes the DNAT rule and clears the association.
func (s *Service) DisassociateFloatingIP(ctx context.Context, projectID, fipID string) (*FloatingIP, error) {
	fip, err := s.GetFloatingIP(ctx, projectID, fipID)
	if err != nil {
		return nil, err
	}
	return s.disassociateFloatingIP(ctx, fip)
}

func (s *Service) disassociateFloatingIP(ctx context.Context, fip *FloatingIP) (*FloatingIP, error) {
	if fip.RouterID == "" {
		return fip, nil
	}
	type floatingIPDisassociateTask struct {
		RouterID   string `json:"router_id"`
		FloatingIP string `json:"floating_ip"`
	}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.dispatchAndWait(bgCtx, "network.floatingip.disassociate", floatingIPDisassociateTask{ //nolint:errcheck
			RouterID:   fip.RouterID,
			FloatingIP: fip.FloatingIPAddress,
		})
	}()
	fip.PortID = ""
	fip.FixedIPAddress = ""
	fip.RouterID = ""
	fip.Status = "down"
	s.saveFloatingIP(ctx, fip) //nolint:errcheck
	return fip, nil
}

// DeleteFloatingIP disassociates (if needed), releases the claimed external IP, and deletes the record.
func (s *Service) DeleteFloatingIP(ctx context.Context, projectID, fipID string) error {
	fip, err := s.GetFloatingIP(ctx, projectID, fipID)
	if err != nil {
		return err
	}
	if fip.RouterID != "" {
		if _, err := s.disassociateFloatingIP(ctx, fip); err != nil {
			return err
		}
	}
	// Release the IP claim port.
	if fip.ExternalNetworkID != "" {
		ports, _ := s.ListPorts(ctx, projectID, fip.ExternalNetworkID)
		for _, p := range ports {
			if p.DeviceID == "floatingip:"+fipID {
				s.store.Delete(ctx, portKeyPrefix+projectID+"/"+p.ID) //nolint:errcheck
			}
		}
	}
	return s.store.Delete(ctx, floatingIPKeyPrefix+projectID+"/"+fipID)
}

// findExternalSubnet finds a usable external network by looking for any router
// that has a gateway configured, then returning that network and one of its subnets.
func (s *Service) findExternalSubnet(ctx context.Context) (*Network, *Subnet, error) {
	// Scan all routers across all projects for one with a gateway.
	allRouters, err := s.store.GetPrefix(ctx, routerKeyPrefix)
	if err != nil {
		return nil, nil, err
	}
	for _, v := range allRouters {
		var r Router
		if err := json.Unmarshal([]byte(v), &r); err != nil || r.ExternalNetworkID == "" {
			continue
		}
		net, err := s.getNetworkByID(ctx, r.ExternalNetworkID)
		if err != nil {
			continue
		}
		allSubs, err := s.ListSubnets(ctx, "")
		if err != nil {
			continue
		}
		for _, sub := range allSubs {
			if sub.NetworkID == net.ID {
				return net, sub, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("no router with a configured gateway found — set a router gateway first")
}

// findRouterForSubnet returns the first project router that has a gateway set
// and has an interface on the given subnet.
func (s *Service) findRouterForSubnet(ctx context.Context, projectID, subnetID string) (*Router, error) {
	routers, err := s.ListRouters(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, r := range routers {
		if r.ExternalNetworkID == "" {
			continue
		}
		for _, sid := range r.InterfaceSubnets {
			if sid == subnetID {
				return r, nil
			}
		}
	}
	return nil, fmt.Errorf("no router with gateway found for subnet %s", subnetID)
}

func (s *Service) saveFloatingIP(ctx context.Context, fip *FloatingIP) error {
	data, _ := json.Marshal(fip)
	return s.store.Put(ctx, floatingIPKeyPrefix+fip.ProjectID+"/"+fip.ID, string(data))
}

// ─── Security Groups ───────────────────────────────────────────────────────────

func (s *Service) ListSecurityGroups(ctx context.Context, projectID string) ([]*SecurityGroup, error) {
	vals, err := s.store.GetPrefix(ctx, sgKeyPrefix+projectID+"/")
	if err != nil {
		return nil, err
	}
	sgs := make([]*SecurityGroup, 0, len(vals))
	for _, v := range vals {
		var sg SecurityGroup
		if err := json.Unmarshal([]byte(v), &sg); err != nil {
			continue
		}
		sgs = append(sgs, &sg)
	}
	sort.Slice(sgs, func(i, j int) bool { return sgs[i].CreatedAt.Before(sgs[j].CreatedAt) })
	return sgs, nil
}

func (s *Service) CreateSecurityGroup(ctx context.Context, projectID string, req CreateSecurityGroupRequest) (*SecurityGroup, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	sg := &SecurityGroup{
		ID:          id.New(),
		ProjectID:   projectID,
		Name:        req.Name,
		Description: req.Description,
		Rules:       []SecurityGroupRule{},
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.saveSecurityGroup(ctx, sg); err != nil {
		return nil, err
	}
	// Create the OVN port group with default ACLs (deny ingress, allow egress).
	if _, err := s.dispatchAndWait(ctx, "network.securitygroup.create", map[string]string{
		"pg_name": pgName(sg.ID),
	}); err != nil {
		s.log.Warn("OVN port group create failed", zap.String("sg_id", sg.ID), zap.Error(err))
	}
	return sg, nil
}

func (s *Service) GetSecurityGroup(ctx context.Context, projectID, sgID string) (*SecurityGroup, error) {
	val, err := s.store.Get(ctx, sgKeyPrefix+projectID+"/"+sgID)
	if err != nil || val == "" {
		return nil, fmt.Errorf("security group %s not found", sgID)
	}
	var sg SecurityGroup
	if err := json.Unmarshal([]byte(val), &sg); err != nil {
		return nil, err
	}
	return &sg, nil
}

func (s *Service) DeleteSecurityGroup(ctx context.Context, projectID, sgID string) error {
	if err := s.store.Delete(ctx, sgKeyPrefix+projectID+"/"+sgID); err != nil {
		return err
	}
	if _, err := s.dispatchAndWait(ctx, "network.securitygroup.delete", map[string]string{
		"pg_name": pgName(sgID),
	}); err != nil {
		s.log.Warn("OVN port group delete failed", zap.String("sg_id", sgID), zap.Error(err))
	}
	return nil
}

// validateSGRule checks that an AddSecurityGroupRuleRequest is well-formed.
func validateSGRule(req AddSecurityGroupRuleRequest) error {
	if req.Direction != "ingress" && req.Direction != "egress" {
		return fmt.Errorf("direction must be 'ingress' or 'egress', got %q", req.Direction)
	}
	if req.Ethertype != "IPv4" && req.Ethertype != "IPv6" {
		return fmt.Errorf("ethertype must be 'IPv4' or 'IPv6', got %q", req.Ethertype)
	}
	switch req.Protocol {
	case "", "tcp", "udp", "icmp", "icmp6":
		// valid
	default:
		return fmt.Errorf("protocol must be one of tcp, udp, icmp, icmp6, or empty for any; got %q", req.Protocol)
	}
	if req.Protocol == "tcp" || req.Protocol == "udp" {
		if req.PortMin < 0 || req.PortMin > 65535 {
			return fmt.Errorf("port_range_min must be 0–65535, got %d", req.PortMin)
		}
		if req.PortMax < 0 || req.PortMax > 65535 {
			return fmt.Errorf("port_range_max must be 0–65535, got %d", req.PortMax)
		}
		if req.PortMin > 0 && req.PortMax > 0 && req.PortMin > req.PortMax {
			return fmt.Errorf("port_range_min (%d) must be <= port_range_max (%d)", req.PortMin, req.PortMax)
		}
	} else if req.PortMin != 0 || req.PortMax != 0 {
		return fmt.Errorf("port ranges are only valid for tcp/udp protocols")
	}
	if req.RemoteCIDR != "" && req.RemoteSGID != "" {
		return fmt.Errorf("remote_ip_prefix and remote_group_id are mutually exclusive")
	}
	if req.RemoteCIDR != "" {
		if _, _, err := net.ParseCIDR(req.RemoteCIDR); err != nil {
			return fmt.Errorf("remote_ip_prefix %q is not a valid CIDR: %w", req.RemoteCIDR, err)
		}
	}
	return nil
}

func (s *Service) AddSecurityGroupRule(ctx context.Context, projectID, sgID string, req AddSecurityGroupRuleRequest) (*SecurityGroupRule, error) {
	sg, err := s.GetSecurityGroup(ctx, projectID, sgID)
	if err != nil {
		return nil, err
	}
	if req.Ethertype == "" {
		req.Ethertype = "IPv4"
	}
	if err := validateSGRule(req); err != nil {
		return nil, err
	}
	// If referencing a remote security group, verify it exists in this project.
	if req.RemoteSGID != "" {
		if _, err := s.GetSecurityGroup(ctx, projectID, req.RemoteSGID); err != nil {
			return nil, fmt.Errorf("remote_group_id %s not found in project", req.RemoteSGID)
		}
	}
	rule := SecurityGroupRule{
		ID:              id.New(),
		SecurityGroupID: sgID,
		Direction:       req.Direction,
		Protocol:        req.Protocol,
		PortMin:         req.PortMin,
		PortMax:         req.PortMax,
		RemoteCIDR:      req.RemoteCIDR,
		RemoteSGID:      req.RemoteSGID,
		Ethertype:       req.Ethertype,
	}
	sg.Rules = append(sg.Rules, rule)
	if err := s.saveSecurityGroup(ctx, sg); err != nil {
		return nil, err
	}
	if err := s.syncSGACLs(ctx, sg); err != nil {
		s.log.Warn("OVN ACL sync failed after rule add; etcd and OVN may be out of sync",
			zap.String("sg_id", sgID), zap.Error(err))
	}
	return &rule, nil
}

func (s *Service) DeleteSecurityGroupRule(ctx context.Context, projectID, sgID, ruleID string) error {
	sg, err := s.GetSecurityGroup(ctx, projectID, sgID)
	if err != nil {
		return err
	}
	filtered := sg.Rules[:0]
	found := false
	for _, r := range sg.Rules {
		if r.ID == ruleID {
			found = true
		} else {
			filtered = append(filtered, r)
		}
	}
	if !found {
		return fmt.Errorf("rule %s not found", ruleID)
	}
	sg.Rules = filtered
	if err := s.saveSecurityGroup(ctx, sg); err != nil {
		return err
	}
	if err := s.syncSGACLs(ctx, sg); err != nil {
		s.log.Warn("OVN ACL sync failed after rule delete; etcd and OVN may be out of sync",
			zap.String("sg_id", sgID), zap.Error(err))
	}
	return nil
}

// UpdatePortSecurityGroups atomically replaces the security group assignments on a port,
// dispatching OVN port-group membership changes for each addition/removal.
func (s *Service) UpdatePortSecurityGroups(ctx context.Context, portID string, sgIDs []string) (*Port, error) {
	port, err := s.GetPort(ctx, portID)
	if err != nil {
		return nil, fmt.Errorf("port %s not found", portID)
	}

	// Compute removals and additions.
	oldSet := make(map[string]bool, len(port.SecurityGroupIDs))
	for _, id := range port.SecurityGroupIDs {
		oldSet[id] = true
	}
	newSet := make(map[string]bool, len(sgIDs))
	for _, id := range sgIDs {
		newSet[id] = true
	}

	for sgID := range oldSet {
		if !newSet[sgID] {
			s.dispatchAndWait(ctx, "network.securitygroup.port.remove", map[string]string{ //nolint:errcheck
				"pg_name": pgName(sgID),
				"port_id": portID,
			})
		}
	}
	for sgID := range newSet {
		if !oldSet[sgID] {
			s.dispatchAndWait(ctx, "network.securitygroup.port.add", map[string]string{ //nolint:errcheck
				"pg_name": pgName(sgID),
				"port_id": portID,
			})
		}
	}

	port.SecurityGroupIDs = sgIDs
	if err := s.savePort(ctx, port); err != nil {
		return nil, err
	}
	return port, nil
}

// syncSGACLs dispatches a rules.sync task to re-apply all ACLs for a security group.
func (s *Service) syncSGACLs(ctx context.Context, sg *SecurityGroup) error {
	type sgRulePayload struct {
		Direction      string `json:"direction"`
		Protocol       string `json:"protocol,omitempty"`
		PortMin        int    `json:"port_min,omitempty"`
		PortMax        int    `json:"port_max,omitempty"`
		RemoteCIDR     string `json:"remote_cidr,omitempty"`
		RemoteSGPGName string `json:"remote_sg_pg_name,omitempty"` // OVN port group name of remote SG
		Ethertype      string `json:"ethertype,omitempty"`
	}
	rules := make([]sgRulePayload, len(sg.Rules))
	for i, r := range sg.Rules {
		var remotePG string
		if r.RemoteSGID != "" {
			remotePG = pgName(r.RemoteSGID)
		}
		rules[i] = sgRulePayload{
			Direction:      r.Direction,
			Protocol:       r.Protocol,
			PortMin:        r.PortMin,
			PortMax:        r.PortMax,
			RemoteCIDR:     r.RemoteCIDR,
			RemoteSGPGName: remotePG,
			Ethertype:      r.Ethertype,
		}
	}
	_, err := s.dispatchAndWait(ctx, "network.securitygroup.rules.sync", map[string]interface{}{
		"pg_name": pgName(sg.ID),
		"rules":   rules,
	})
	return err
}

func (s *Service) saveSecurityGroup(ctx context.Context, sg *SecurityGroup) error {
	data, err := json.Marshal(sg)
	if err != nil {
		return fmt.Errorf("marshal security group: %w", err)
	}
	return s.store.Put(ctx, sgKeyPrefix+sg.ProjectID+"/"+sg.ID, string(data))
}

// EnsureDefaultSecurityGroup returns the ID of the project's default security group,
// creating it (with standard rules) if it does not yet exist.
// The mapping is stored at defaultSGKeyPrefix+projectID so the lookup is O(1).
// PutIfAbsent is used to make creation atomic: if two goroutines race, one will
// win the CAS and the other's candidate SG will be deleted.
func (s *Service) EnsureDefaultSecurityGroup(ctx context.Context, projectID string) (string, error) {
	// Fast path: already exists.
	if sgID, err := s.store.Get(ctx, defaultSGKeyPrefix+projectID); err == nil && sgID != "" {
		return sgID, nil
	}

	// Slow path: create a candidate default SG.
	sg, err := s.CreateSecurityGroup(ctx, projectID, CreateSecurityGroupRequest{Name: "default"})
	if err != nil {
		return "", fmt.Errorf("create default security group: %w", err)
	}

	// Standard rules:
	//   egress  – allow all IPv4 & IPv6 out
	//   ingress – allow ICMP from anywhere (ping)
	//   ingress – allow TCP 22 from anywhere (SSH)
	defaultRules := []AddSecurityGroupRuleRequest{
		{Direction: "egress", Ethertype: "IPv4"},
		{Direction: "egress", Ethertype: "IPv6"},
		{Direction: "ingress", Protocol: "icmp", Ethertype: "IPv4"},
		{Direction: "ingress", Protocol: "tcp", PortMin: 22, PortMax: 22, Ethertype: "IPv4"},
	}
	for _, r := range defaultRules {
		if _, err := s.AddSecurityGroupRule(ctx, projectID, sg.ID, r); err != nil {
			s.log.Warn("failed to add default SG rule", zap.String("sg_id", sg.ID), zap.Error(err))
		}
	}

	// Atomically claim the default SG slot. If another goroutine beat us, delete our
	// candidate and return the winner's ID.
	created, err := s.store.PutIfAbsent(ctx, defaultSGKeyPrefix+projectID, sg.ID)
	if err != nil {
		return "", fmt.Errorf("persist default SG mapping: %w", err)
	}
	if !created {
		// Another goroutine won the race; clean up our candidate.
		if delErr := s.DeleteSecurityGroup(ctx, projectID, sg.ID); delErr != nil {
			s.log.Warn("failed to clean up duplicate default SG candidate",
				zap.String("sg_id", sg.ID), zap.Error(delErr))
		}
		return s.store.Get(ctx, defaultSGKeyPrefix+projectID)
	}

	s.log.Info("created default security group",
		zap.String("project_id", projectID),
		zap.String("sg_id", sg.ID),
	)
	return sg.ID, nil
}

// ─── FWaaS ────────────────────────────────────────────────────────────────────

const fwPolicyKeyPrefix = "/pulsar/network/firewall-policies/"

// validateFirewallRule returns an error if a FirewallRule is not well-formed.
func validateFirewallRule(r FirewallRule) error {
	if r.Direction != "ingress" && r.Direction != "egress" {
		return fmt.Errorf("rule direction must be 'ingress' or 'egress', got %q", r.Direction)
	}
	switch r.Protocol {
	case "", "tcp", "udp", "icmp", "icmp6":
		// valid
	default:
		return fmt.Errorf("rule protocol must be one of tcp, udp, icmp, icmp6, or empty for any; got %q", r.Protocol)
	}
	if r.Protocol == "tcp" || r.Protocol == "udp" {
		if r.PortMin < 0 || r.PortMin > 65535 {
			return fmt.Errorf("port_min must be 0–65535, got %d", r.PortMin)
		}
		if r.PortMax < 0 || r.PortMax > 65535 {
			return fmt.Errorf("port_max must be 0–65535, got %d", r.PortMax)
		}
		if r.PortMin > 0 && r.PortMax > 0 && r.PortMin > r.PortMax {
			return fmt.Errorf("port_min (%d) must be <= port_max (%d)", r.PortMin, r.PortMax)
		}
	} else if r.PortMin != 0 || r.PortMax != 0 {
		return fmt.Errorf("port ranges are only valid for tcp/udp protocols; set a specific protocol or remove the port range")
	}
	if r.Action != "allow" && r.Action != "drop" {
		return fmt.Errorf("rule action must be 'allow' or 'drop', got %q", r.Action)
	}
	return nil
}

func (s *Service) CreateFirewallPolicy(ctx context.Context, projectID string, req CreateFirewallPolicyRequest) (*FirewallPolicy, error) {
	if req.RouterID == "" {
		return nil, fmt.Errorf("router_id required")
	}
	if req.Name == "" {
		return nil, fmt.Errorf("name required")
	}
	// Enforce one policy per router: reject if a policy already exists for this router.
	existing, err := s.ListFirewallPolicies(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("checking existing policies: %w", err)
	}
	for _, p := range existing {
		if p.RouterID == req.RouterID {
			return nil, fmt.Errorf("a firewall policy already exists for this router (%s); edit or delete it instead of creating a new one", p.Name)
		}
	}
	// Validate and assign IDs to rules.
	for i := range req.Rules {
		if err := validateFirewallRule(req.Rules[i]); err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		if req.Rules[i].ID == "" {
			req.Rules[i].ID = id.New()
		}
	}
	policy := &FirewallPolicy{
		ID:        id.New(),
		ProjectID: projectID,
		RouterID:  req.RouterID,
		Name:      req.Name,
		Rules:     req.Rules,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.saveFirewallPolicy(ctx, policy); err != nil {
		return nil, err
	}
	if err := s.dispatchFirewallSync(ctx, policy); err != nil {
		s.log.Warn("firewall sync dispatch failed", zap.String("policy_id", policy.ID), zap.Error(err))
	}
	return policy, nil
}

func (s *Service) GetFirewallPolicy(ctx context.Context, projectID, policyID string) (*FirewallPolicy, error) {
	val, err := s.store.Get(ctx, fwPolicyKeyPrefix+projectID+"/"+policyID)
	if err != nil || val == "" {
		return nil, fmt.Errorf("firewall policy not found")
	}
	var p FirewallPolicy
	if err := json.Unmarshal([]byte(val), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Service) ListFirewallPolicies(ctx context.Context, projectID string) ([]*FirewallPolicy, error) {
	vals, err := s.store.GetPrefix(ctx, fwPolicyKeyPrefix+projectID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]*FirewallPolicy, 0, len(vals))
	for _, v := range vals {
		var p FirewallPolicy
		if err := json.Unmarshal([]byte(v), &p); err == nil {
			out = append(out, &p)
		}
	}
	return out, nil
}

func (s *Service) UpdateFirewallPolicy(ctx context.Context, projectID, policyID string, req UpdateFirewallPolicyRequest) (*FirewallPolicy, error) {
	policy, err := s.GetFirewallPolicy(ctx, projectID, policyID)
	if err != nil {
		return nil, err
	}
	if req.Name != "" {
		policy.Name = req.Name
	}
	if req.Rules != nil {
		for i := range req.Rules {
			if err := validateFirewallRule(req.Rules[i]); err != nil {
				return nil, fmt.Errorf("rule %d: %w", i+1, err)
			}
			if req.Rules[i].ID == "" {
				req.Rules[i].ID = id.New()
			}
		}
		policy.Rules = req.Rules
	}
	policy.UpdatedAt = time.Now().UTC()
	if err := s.saveFirewallPolicy(ctx, policy); err != nil {
		return nil, err
	}
	if err := s.dispatchFirewallSync(ctx, policy); err != nil {
		s.log.Warn("firewall sync dispatch failed", zap.String("policy_id", policy.ID), zap.Error(err))
	}
	return policy, nil
}

func (s *Service) DeleteFirewallPolicy(ctx context.Context, projectID, policyID string) error {
	policy, err := s.GetFirewallPolicy(ctx, projectID, policyID)
	if err != nil {
		return err
	}
	// Delete from store first so collectRouterRules won't see it when we re-sync.
	if err := s.store.Delete(ctx, fwPolicyKeyPrefix+projectID+"/"+policyID); err != nil {
		return err
	}
	// Re-sync remaining policies (or clear all if none left).
	if err := s.dispatchFirewallClear(ctx, policy.RouterID); err != nil {
		s.log.Warn("firewall clear dispatch failed", zap.Error(err))
	}
	return nil
}

func (s *Service) saveFirewallPolicy(ctx context.Context, p *FirewallPolicy) error {
	data, _ := json.Marshal(p)
	return s.store.Put(ctx, fwPolicyKeyPrefix+p.ProjectID+"/"+p.ID, string(data))
}

// collectRouterRules gathers all FirewallRule entries from every policy on the
// given router (across all projects), merging them into a single list for dispatch.
// This ensures that when multiple policies exist on the same router, all rules are
// applied together rather than each policy overwriting the previous one.
func (s *Service) collectRouterRules(ctx context.Context, routerID string) []FirewallRule {
	all, err := s.store.GetPrefix(ctx, fwPolicyKeyPrefix)
	if err != nil {
		return nil
	}
	var merged []FirewallRule
	for _, v := range all {
		var p FirewallPolicy
		if err := json.Unmarshal([]byte(v), &p); err != nil {
			continue
		}
		if p.RouterID != routerID {
			continue
		}
		merged = append(merged, p.Rules...)
	}
	return merged
}

// dispatchFirewallSync sends network.router.firewall.sync to the first available
// network agent. It merges ALL policies on the router before dispatching, so that
// adding or updating a single policy does not overwrite rules from other policies
// on the same router.
//
// Policies are installed as OVN router policies (lr-policy-add) rather than switch
// ACLs, because switch ACLs on the external switch are bypassed for localnet-ingress
// traffic. Router policies run after DNAT (lr_in_policy, table 15) and see the
// post-DNAT internal IP, so they work correctly for all traffic paths including
// traffic from the physical network.
func (s *Service) dispatchFirewallSync(ctx context.Context, policy *FirewallPolicy) error {
	type fwRule struct {
		Direction string `json:"direction"`
		Protocol  string `json:"protocol,omitempty"`
		PortMin   int    `json:"port_min,omitempty"`
		PortMax   int    `json:"port_max,omitempty"`
		SrcCIDR   string `json:"src_cidr,omitempty"`
		DstCIDR   string `json:"dst_cidr,omitempty"`
		Action    string `json:"action"`
		Priority  int    `json:"priority"`
	}

	// Merge all rules from every policy on this router (includes the just-saved one).
	allRules := s.collectRouterRules(ctx, policy.RouterID)
	rules := make([]fwRule, len(allRules))
	for i, r := range allRules {
		rules[i] = fwRule{
			Direction: r.Direction, Protocol: r.Protocol,
			PortMin: r.PortMin, PortMax: r.PortMax,
			SrcCIDR: r.SrcCIDR, DstCIDR: r.DstCIDR,
			Action: r.Action, Priority: r.Priority,
		}
	}

	dnatTargets := s.collectDNATTargets(ctx, policy.RouterID)

	_, err := s.dispatchAndWait(ctx, "network.router.firewall.sync", map[string]interface{}{
		"router_id":    policy.RouterID,
		"dnat_targets": dnatTargets,
		"rules":        rules,
	})
	return err
}

// dispatchFirewallClear re-syncs the router after a policy deletion. If other
// policies remain on the router, their rules are preserved. Only when no policies
// remain is rules=null sent, which clears all lr-policies from OVN.
func (s *Service) dispatchFirewallClear(ctx context.Context, routerID string) error {
	remaining := s.collectRouterRules(ctx, routerID)
	dnatTargets := s.collectDNATTargets(ctx, routerID)

	var payload map[string]interface{}
	if len(remaining) > 0 {
		// Other policies still exist — re-sync with their merged rules.
		type fwRule struct {
			Direction string `json:"direction"`
			Protocol  string `json:"protocol,omitempty"`
			PortMin   int    `json:"port_min,omitempty"`
			PortMax   int    `json:"port_max,omitempty"`
			SrcCIDR   string `json:"src_cidr,omitempty"`
			DstCIDR   string `json:"dst_cidr,omitempty"`
			Action    string `json:"action"`
			Priority  int    `json:"priority"`
		}
		rules := make([]fwRule, len(remaining))
		for i, r := range remaining {
			rules[i] = fwRule{
				Direction: r.Direction, Protocol: r.Protocol,
				PortMin: r.PortMin, PortMax: r.PortMax,
				SrcCIDR: r.SrcCIDR, DstCIDR: r.DstCIDR,
				Action: r.Action, Priority: r.Priority,
			}
		}
		payload = map[string]interface{}{
			"router_id":    routerID,
			"dnat_targets": dnatTargets,
			"rules":        rules,
		}
	} else {
		// No policies remain — clear all lr-policies from OVN.
		payload = map[string]interface{}{
			"router_id":    routerID,
			"dnat_targets": dnatTargets,
			"rules":        nil,
		}
	}

	_, err := s.dispatchAndWait(ctx, "network.router.firewall.sync", payload)
	return err
}

// collectDNATTargets returns the internal (post-DNAT) IP addresses of all VMs
// that are currently associated with floating IPs on the given router.
func (s *Service) collectDNATTargets(ctx context.Context, routerID string) []string {
	fips, err := s.store.GetPrefix(ctx, floatingIPKeyPrefix)
	if err != nil {
		return nil
	}
	var targets []string
	seen := make(map[string]bool)
	for _, v := range fips {
		var fip FloatingIP
		if err := json.Unmarshal([]byte(v), &fip); err != nil {
			continue
		}
		if fip.RouterID != routerID || fip.FixedIPAddress == "" {
			continue
		}
		if !seen[fip.FixedIPAddress] {
			seen[fip.FixedIPAddress] = true
			targets = append(targets, fip.FixedIPAddress)
		}
	}
	return targets
}
