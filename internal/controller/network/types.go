package network

import "time"

type Network struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // "vxlan" | "vlan" | "flat"
	VNI       int       `json:"vni,omitempty"`
	VlanID    int       `json:"vlan_id,omitempty"`
	External  bool      `json:"external"`
	Status    string    `json:"status"` // "active" | "building" | "error"
	CreatedAt time.Time `json:"created_at"`
}

type Subnet struct {
	ID              string          `json:"id"`
	ProjectID       string          `json:"project_id"`
	NetworkID       string          `json:"network_id"`
	Name            string          `json:"name,omitempty"`
	CIDR            string          `json:"cidr"`
	GatewayIP       string          `json:"gateway_ip,omitempty"`
	AllocationPool  *AllocationPool `json:"allocation_pool,omitempty"`
	DNSNameservers  []string        `json:"dns_nameservers,omitempty"`
	DHCPOptionsUUID string          `json:"dhcp_options_uuid,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

// AllocationPool restricts IP allocation to the inclusive range [Start, End].
type AllocationPool struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type Port struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	NetworkID        string    `json:"network_id"`
	SubnetID         string    `json:"subnet_id,omitempty"`
	Name             string    `json:"name,omitempty"`
	MACAddress       string    `json:"mac_address"`
	FixedIPs         []FixedIP `json:"fixed_ips"`
	SecurityGroupIDs []string  `json:"security_group_ids,omitempty"`
	Status           string    `json:"status"`              // "active" | "down" | "build"
	DeviceID         string    `json:"device_id,omitempty"` // instance ID when bound
	CreatedAt        time.Time `json:"created_at"`
}

type FixedIP struct {
	SubnetID  string `json:"subnet_id"`
	IPAddress string `json:"ip_address"`
}

type Router struct {
	ID                string    `json:"id"`
	ProjectID         string    `json:"project_id"`
	Name              string    `json:"name"`
	Status            string    `json:"status"`
	ExternalNetworkID string    `json:"external_network_id,omitempty"`
	ExternalIP        string    `json:"external_ip,omitempty"`
	InterfaceSubnets  []string  `json:"interface_subnets,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type FloatingIP struct {
	ID                string `json:"id"`
	ProjectID         string `json:"project_id"`
	ExternalNetworkID string `json:"floating_network_id"`
	FloatingIPAddress string `json:"floating_ip_address"`
	FixedIPAddress    string `json:"fixed_ip_address,omitempty"`
	PortID            string `json:"port_id,omitempty"`
	RouterID          string `json:"router_id,omitempty"`
	Status            string `json:"status"`
}

type SecurityGroup struct {
	ID          string              `json:"id"`
	ProjectID   string              `json:"project_id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Rules       []SecurityGroupRule `json:"rules,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
}

type SecurityGroupRule struct {
	ID              string `json:"id"`
	SecurityGroupID string `json:"security_group_id"`
	Direction       string `json:"direction"`          // "ingress" | "egress"
	Protocol        string `json:"protocol,omitempty"` // "tcp" | "udp" | "icmp" | "icmp6" | "" (any)
	PortMin         int    `json:"port_range_min,omitempty"`
	PortMax         int    `json:"port_range_max,omitempty"`
	RemoteCIDR      string `json:"remote_ip_prefix,omitempty"`
	RemoteSGID      string `json:"remote_group_id,omitempty"` // mutually exclusive with RemoteCIDR
	Ethertype       string `json:"ethertype"`                 // "IPv4" | "IPv6"
}

// Request types
type CreateNetworkRequest struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "vxlan" (default) | "vlan" | "flat"
	VlanID   int    `json:"vlan_id,omitempty"`
	External bool   `json:"external"`
	PhysNet  string `json:"physnet,omitempty"`
}

// UpdateNetworkRequest carries the fields that can be changed after creation.
// Pointer fields are optional — only non-nil fields are applied.
type UpdateNetworkRequest struct {
	Name     *string `json:"name,omitempty"`
	External *bool   `json:"external,omitempty"`
}

type CreateSubnetRequest struct {
	Name           string          `json:"name,omitempty"`
	NetworkID      string          `json:"network_id"`
	CIDR           string          `json:"cidr"`
	GatewayIP      string          `json:"gateway_ip,omitempty"`
	AllocationPool *AllocationPool `json:"allocation_pool,omitempty"`
	DNSNameservers []string        `json:"dns_nameservers,omitempty"`
}

type CreatePortRequest struct {
	Name      string `json:"name,omitempty"`
	NetworkID string `json:"network_id"`
	SubnetID  string `json:"subnet_id,omitempty"` // picks first subnet if empty
}

type CreateRouterRequest struct {
	Name string `json:"name"`
}

type SetGatewayRequest struct {
	ExternalNetworkID string `json:"external_network_id"`
	ExternalIP        string `json:"external_ip,omitempty"`
}

type CreateSecurityGroupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type AddSecurityGroupRuleRequest struct {
	Direction  string `json:"direction"`          // "ingress" | "egress"
	Protocol   string `json:"protocol,omitempty"` // "tcp" | "udp" | "icmp" | "icmp6" | "" (any)
	PortMin    int    `json:"port_range_min,omitempty"`
	PortMax    int    `json:"port_range_max,omitempty"`
	RemoteCIDR string `json:"remote_ip_prefix,omitempty"` // "" = 0.0.0.0/0
	RemoteSGID string `json:"remote_group_id,omitempty"`  // mutually exclusive with RemoteCIDR
	Ethertype  string `json:"ethertype,omitempty"`        // "IPv4" (default) | "IPv6"
}

type UpdatePortRequest struct {
	SecurityGroupIDs []string `json:"security_group_ids"`
}

type CreateFloatingIPRequest struct{}

type UpdateFloatingIPRequest struct {
	PortID string `json:"port_id"` // empty = disassociate
}

type AddRouterInterfaceRequest struct {
	SubnetID string `json:"subnet_id"`
}

// ─── FWaaS ────────────────────────────────────────────────────────────────────

// FirewallPolicy is a stateful, ordered set of firewall rules applied to a router's
// gateway port (the external-facing LRP). Rules are evaluated highest-priority-first;
// traffic not matched by any rule is dropped by a default deny ACL.
type FirewallPolicy struct {
	ID        string         `json:"id"`
	ProjectID string         `json:"project_id"`
	RouterID  string         `json:"router_id"`
	Name      string         `json:"name"`
	Rules     []FirewallRule `json:"rules"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// FirewallRule is a single ACL entry within a FirewallPolicy.
// Unset optional fields match all values (wildcard).
type FirewallRule struct {
	ID        string `json:"id"`
	Direction string `json:"direction"` // "ingress" | "egress"
	Protocol  string `json:"protocol,omitempty"`
	PortMin   int    `json:"port_min,omitempty"`
	PortMax   int    `json:"port_max,omitempty"`
	SrcCIDR   string `json:"src_cidr,omitempty"`
	DstCIDR   string `json:"dst_cidr,omitempty"`
	Action    string `json:"action"`   // "allow" | "deny" (also accepts "drop" as alias for "deny")
	Priority  int    `json:"priority"` // 0–999; higher wins
}

type CreateFirewallPolicyRequest struct {
	Name     string         `json:"name"`
	RouterID string         `json:"router_id"`
	Rules    []FirewallRule `json:"rules,omitempty"`
}

type UpdateFirewallPolicyRequest struct {
	Name  string         `json:"name,omitempty"`
	Rules []FirewallRule `json:"rules,omitempty"`
}
