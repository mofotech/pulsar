package network

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	"github.com/agomez/pulsar/internal/agent/network/ovn"
	"github.com/agomez/pulsar/internal/config"
)

// Executor handles network tasks dispatched by the controller.
type Executor struct {
	ovn       *ovn.Driver
	cfg       config.NetworkAgent
	log       *zap.Logger
	chassisID string // OVS system-id, used for lrp-set-gateway-chassis
}

func NewExecutor(cfg config.NetworkAgent, log *zap.Logger) *Executor {
	e := &Executor{
		ovn: ovn.New(cfg.OVNNBAddr),
		cfg: cfg,
		log: log,
	}
	ctx := context.Background()
	if id, err := ovn.LocalChassisID(ctx); err == nil {
		e.chassisID = id
		log.Info("detected OVN chassis ID", zap.String("chassis_id", id))
	} else {
		log.Warn("could not detect OVN chassis ID, falling back to ovn_chassis_name", zap.Error(err))
		e.chassisID = cfg.OVNChassisName
	}
	return e
}

func (e *Executor) Execute(ctx context.Context, task *agentpb.TaskAssignment) *agentpb.TaskResult {
	result := &agentpb.TaskResult{TaskId: task.TaskId}
	var err error
	switch task.TaskType {
	case "network.create":
		err = e.createNetwork(ctx, task.Payload)
	case "network.delete":
		err = e.deleteNetwork(ctx, task.Payload)
	case "network.subnet.create":
		result.Result, err = e.createSubnet(ctx, task.Payload)
	case "network.subnet.delete":
		err = e.deleteSubnet(ctx, task.Payload)
	case "network.port.create":
		err = e.createPort(ctx, task.Payload)
	case "network.port.delete":
		err = e.deletePort(ctx, task.Payload)
	case "network.port.bind":
		err = e.bindPort(ctx, task.Payload)
	case "network.port.unbind":
		err = e.unbindPort(ctx, task.Payload)
	case "network.router.create":
		err = e.createRouter(ctx, task.Payload)
	case "network.router.delete":
		err = e.deleteRouter(ctx, task.Payload)
	case "network.router.interface.add":
		err = e.addRouterInterface(ctx, task.Payload)
	case "network.router.interface.remove":
		err = e.removeRouterInterface(ctx, task.Payload)
	case "network.router.gateway.set":
		err = e.setRouterGateway(ctx, task.Payload)
	case "network.router.gateway.clear":
		err = e.clearRouterGateway(ctx, task.Payload)
	case "network.router.nat.add":
		err = e.addRouterNAT(ctx, task.Payload)
	case "network.floatingip.associate":
		err = e.associateFloatingIP(ctx, task.Payload)
	case "network.floatingip.disassociate":
		err = e.disassociateFloatingIP(ctx, task.Payload)
	case "network.securitygroup.create":
		err = e.createSecurityGroup(ctx, task.Payload)
	case "network.securitygroup.delete":
		err = e.deleteSecurityGroup(ctx, task.Payload)
	case "network.securitygroup.rules.sync":
		err = e.syncSecurityGroupRules(ctx, task.Payload)
	case "network.securitygroup.port.add":
		err = e.addPortToSecurityGroup(ctx, task.Payload)
	case "network.securitygroup.port.remove":
		err = e.removePortFromSecurityGroup(ctx, task.Payload)
	case "network.list":
		result.Result, err = e.listNetworkResources(ctx)
	case "network.router.firewall.sync":
		err = e.syncRouterFirewall(ctx, task.Payload)
	default:
		err = fmt.Errorf("unknown task type: %s", task.TaskType)
	}
	if err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
		e.log.Error("network task failed",
			zap.String("task_id", task.TaskId),
			zap.String("task_type", task.TaskType),
			zap.Error(err),
		)
	} else {
		result.Success = true
	}
	return result
}

func (e *Executor) createNetwork(ctx context.Context, payload []byte) error {
	var task struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		VlanId int32  `json:"vlan_id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	if err := e.ovn.CreateLogicalSwitch(ctx, task.ID); err != nil {
		return err
	}
	if task.Type == "vlan" || task.Type == "flat" {
		return e.ovn.AddLocalnetPort(ctx, task.ID, e.cfg.PhysnetName, task.VlanId)
	}
	return nil
}

func (e *Executor) deleteNetwork(ctx context.Context, payload []byte) error {
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.DeleteLogicalSwitch(ctx, task.ID)
}

func (e *Executor) createSubnet(ctx context.Context, payload []byte) ([]byte, error) {
	var task struct {
		SubnetID       string   `json:"subnet_id"`
		CIDR           string   `json:"cidr"`
		GatewayIP      string   `json:"gateway_ip"`
		DNSNameservers []string `json:"dns_nameservers"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return nil, err
	}
	uuid, err := e.ovn.CreateDHCPOptions(ctx, task.CIDR, task.GatewayIP, task.DNSNameservers)
	if err != nil {
		return nil, err
	}
	e.log.Info("created DHCP options", zap.String("subnet_id", task.SubnetID), zap.String("uuid", uuid))
	res, _ := json.Marshal(map[string]string{"dhcp_options_uuid": uuid})
	return res, nil
}

func (e *Executor) deleteSubnet(ctx context.Context, payload []byte) error {
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	// DHCP options UUID would need to be tracked separately for cleanup
	return nil
}

func (e *Executor) createPort(ctx context.Context, payload []byte) error {
	var task struct {
		ID              string `json:"id"`
		NetworkID       string `json:"network_id"`
		MACAddr         string `json:"mac_addr"`
		FixedIP         string `json:"fixed_ip"`
		DHCPOptionsUUID string `json:"dhcp_options_uuid"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.CreateLogicalPort(ctx, task.NetworkID, task.ID, task.MACAddr, task.FixedIP, task.DHCPOptionsUUID)
}

func (e *Executor) deletePort(ctx context.Context, payload []byte) error {
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.DeleteLogicalPort(ctx, task.ID)
}

func (e *Executor) bindPort(ctx context.Context, payload []byte) error {
	var task struct {
		PortID    string `json:"port_id"`
		ChassisID string `json:"chassis_id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	// Prefer the configured OVN chassis name over the Pulsar node ID.
	// The node ID is the container/process hostname; the OVN chassis name
	// is the host hostname registered with ovn-controller.
	chassisName := e.cfg.OVNChassisName
	if chassisName == "" {
		chassisName = task.ChassisID
	}
	return e.ovn.BindPort(ctx, task.PortID, chassisName)
}

func (e *Executor) unbindPort(ctx context.Context, payload []byte) error {
	var task struct {
		PortID string `json:"port_id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.UnbindPort(ctx, task.PortID)
}

func (e *Executor) createRouter(ctx context.Context, payload []byte) error {
	var task struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.CreateLogicalRouter(ctx, task.ID)
}

func (e *Executor) deleteRouter(ctx context.Context, payload []byte) error {
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.DeleteLogicalRouter(ctx, task.ID)
}

func (e *Executor) addRouterInterface(ctx context.Context, payload []byte) error {
	var task struct {
		RouterID        string `json:"router_id"`
		NetworkID       string `json:"network_id"`
		RouterPortName  string `json:"router_port_name"`
		SwitchPortName  string `json:"switch_port_name"`
		MAC             string `json:"mac"`
		IPPrefix        string `json:"ip_prefix"`
		SNATExternalIP  string `json:"snat_external_ip"`
		SNATLogicalCIDR string `json:"snat_logical_cidr"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	if err := e.ovn.AddRouterInterface(ctx, task.RouterID, task.NetworkID, task.RouterPortName, task.SwitchPortName, task.MAC, task.IPPrefix); err != nil {
		return err
	}
	if task.SNATExternalIP != "" && task.SNATLogicalCIDR != "" {
		return e.ovn.AddRouterNAT(ctx, task.RouterID, task.SNATExternalIP, task.SNATLogicalCIDR)
	}
	return nil
}

func (e *Executor) removeRouterInterface(ctx context.Context, payload []byte) error {
	var task struct {
		RouterID       string `json:"router_id"`
		NetworkID      string `json:"network_id"`
		RouterPortName string `json:"router_port_name"`
		SwitchPortName string `json:"switch_port_name"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.RemoveRouterInterface(ctx, task.RouterID, task.NetworkID, task.RouterPortName, task.SwitchPortName)
}

func (e *Executor) setRouterGateway(ctx context.Context, payload []byte) error {
	var task struct {
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
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.SetRouterGateway(ctx,
		task.RouterID, task.ExternalNetworkID,
		task.ExtRouterPortName, task.ExtSwitchPortName,
		task.ExtMAC, task.ExtIPPrefix,
		task.NextHopIP, task.SNATExternalIP, task.SNATCIDRs,
		e.chassisID,
	)
}

func (e *Executor) clearRouterGateway(ctx context.Context, payload []byte) error {
	var task struct {
		RouterID          string `json:"router_id"`
		ExternalNetworkID string `json:"external_network_id"`
		ExtRouterPortName string `json:"ext_router_port_name"`
		ExtSwitchPortName string `json:"ext_switch_port_name"`
		SNATExternalIP    string `json:"snat_external_ip"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.ClearRouterGateway(ctx,
		task.RouterID, task.ExternalNetworkID,
		task.ExtRouterPortName, task.ExtSwitchPortName,
		task.SNATExternalIP,
	)
}

func (e *Executor) addRouterNAT(ctx context.Context, payload []byte) error {
	var task struct {
		RouterID        string `json:"router_id"`
		SNATExternalIP  string `json:"snat_external_ip"`
		SNATLogicalCIDR string `json:"snat_logical_cidr"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.AddRouterNAT(ctx, task.RouterID, task.SNATExternalIP, task.SNATLogicalCIDR)
}

func (e *Executor) associateFloatingIP(ctx context.Context, payload []byte) error {
	var task struct {
		RouterID   string `json:"router_id"`
		FloatingIP string `json:"floating_ip"`
		FixedIP    string `json:"fixed_ip"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.AddFloatingIP(ctx, task.RouterID, task.FloatingIP, task.FixedIP)
}

func (e *Executor) disassociateFloatingIP(ctx context.Context, payload []byte) error {
	var task struct {
		RouterID   string `json:"router_id"`
		FloatingIP string `json:"floating_ip"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.RemoveFloatingIP(ctx, task.RouterID, task.FloatingIP)
}

func (e *Executor) createSecurityGroup(ctx context.Context, payload []byte) error {
	var task struct {
		PGName string `json:"pg_name"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	if err := e.ovn.CreatePortGroup(ctx, task.PGName); err != nil {
		return err
	}
	// Apply default ACLs (no user rules yet).
	return e.ovn.SyncPortGroupACLs(ctx, task.PGName, nil)
}

func (e *Executor) deleteSecurityGroup(ctx context.Context, payload []byte) error {
	var task struct {
		PGName string `json:"pg_name"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.DeletePortGroup(ctx, task.PGName)
}

func (e *Executor) syncSecurityGroupRules(ctx context.Context, payload []byte) error {
	var task struct {
		PGName string       `json:"pg_name"`
		Rules  []ovn.SGRule `json:"rules"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.SyncPortGroupACLs(ctx, task.PGName, task.Rules)
}

func (e *Executor) addPortToSecurityGroup(ctx context.Context, payload []byte) error {
	var task struct {
		PGName string `json:"pg_name"`
		PortID string `json:"port_id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.AddPortToGroup(ctx, task.PGName, task.PortID)
}

func (e *Executor) removePortFromSecurityGroup(ctx context.Context, payload []byte) error {
	var task struct {
		PGName string `json:"pg_name"`
		PortID string `json:"port_id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.RemovePortFromGroup(ctx, task.PGName, task.PortID)
}

// listNetworkResources returns a JSON inventory of all OVN logical switches and ports
// managed by this network agent.
// Response: {"switches": ["<id>", ...], "ports": ["<id>", ...]}
func (e *Executor) listNetworkResources(ctx context.Context) ([]byte, error) {
	type response struct {
		Switches []string `json:"switches"`
		Ports    []string `json:"ports"`
	}

	switches, err := e.ovn.ListLogicalSwitches(ctx)
	if err != nil {
		return nil, fmt.Errorf("list logical switches: %w", err)
	}
	ports, err := e.ovn.ListLogicalPorts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list logical ports: %w", err)
	}

	if switches == nil {
		switches = []string{}
	}
	if ports == nil {
		ports = []string{}
	}
	return json.Marshal(response{Switches: switches, Ports: ports})
}

func (e *Executor) syncRouterFirewall(ctx context.Context, payload []byte) error {
	var task struct {
		RouterID    string       `json:"router_id"`
		DnatTargets []string     `json:"dnat_targets"`
		Rules       []ovn.FWRule `json:"rules"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	return e.ovn.SyncRouterFirewall(ctx, task.RouterID, task.DnatTargets, task.Rules)
}
