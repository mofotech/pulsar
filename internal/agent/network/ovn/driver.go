package ovn

import (
	"context"
	"crypto/rand"
	"fmt"
	"os/exec"
	"strings"
)

// Driver wraps ovn-nbctl CLI calls against a specific NB DB.
type Driver struct {
	nbAddr string
}

func New(nbAddr string) *Driver {
	return &Driver{nbAddr: nbAddr}
}

// LocalChassisID returns the OVS system-id, which is the chassis name expected
// by ovn-nbctl lrp-set-gateway-chassis.
func LocalChassisID(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "ovs-vsctl", "get", "Open_vSwitch", ".", "external_ids:system-id").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("get chassis id: %w\n%s", err, string(out))
	}
	return strings.Trim(strings.TrimSpace(string(out)), "\""), nil
}

func (d *Driver) run(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"--db=" + d.nbAddr}, args...)
	n := len(args)
	if n > 3 {
		n = 3
	}
	out, err := exec.CommandContext(ctx, "ovn-nbctl", full...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ovn-nbctl %v: %w\n%s", args[:n], err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func (d *Driver) CreateLogicalSwitch(ctx context.Context, networkID string) error {
	_, err := d.run(ctx, "--may-exist", "ls-add", networkID)
	return err
}

func (d *Driver) AddLocalnetPort(ctx context.Context, networkID, physnet string, vlanID int32) error {
	portName := networkID + "-localnet"
	d.run(ctx, "--may-exist", "lsp-add", networkID, portName) //nolint:errcheck
	d.run(ctx, "lsp-set-type", portName, "localnet")          //nolint:errcheck
	d.run(ctx, "lsp-set-addresses", portName, "unknown")      //nolint:errcheck
	networkName := "network_name=" + physnet
	if _, err := d.run(ctx, "lsp-set-options", portName, networkName); err != nil {
		return err
	}
	if vlanID > 0 {
		d.run(ctx, "set", "Logical_Switch_Port", portName, fmt.Sprintf("tag=%d", vlanID)) //nolint:errcheck
	}
	return nil
}

func (d *Driver) DeleteLogicalSwitch(ctx context.Context, networkID string) error {
	_, err := d.run(ctx, "--if-exists", "ls-del", networkID)
	return err
}

// ListLogicalSwitches returns the names of all OVN logical switches.
// ovn-nbctl ls-list prints one line per switch: "<uuid> (<name>)"
func (d *Driver) ListLogicalSwitches(ctx context.Context) ([]string, error) {
	out, err := d.run(ctx, "ls-list")
	if err != nil {
		return nil, fmt.Errorf("ls-list: %w", err)
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "0a1b2c3d-... (my-switch-name)"
		start := strings.Index(line, "(")
		end := strings.LastIndex(line, ")")
		if start >= 0 && end > start {
			names = append(names, line[start+1:end])
		}
	}
	return names, nil
}

// ListLogicalPorts returns the names of all OVN logical switch ports.
// ovn-nbctl find Logical_Switch_Port prints OVSDB records; we extract the "name" field.
func (d *Driver) ListLogicalPorts(ctx context.Context) ([]string, error) {
	out, err := d.run(ctx, "find", "Logical_Switch_Port", "name!=\"\"")
	if err != nil {
		// If the NB DB is empty or returns nothing, treat as empty list.
		return nil, nil //nolint:nilerr
	}
	var ports []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// Lines look like: "name                : my-port-id"
		if strings.HasPrefix(line, "name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				name := strings.TrimSpace(parts[1])
				if name != "" {
					ports = append(ports, name)
				}
			}
		}
	}
	return ports, nil
}

// CreateDHCPOptions programs OVN DHCP options for a subnet. Returns the options UUID.
func (d *Driver) CreateDHCPOptions(ctx context.Context, cidr, gatewayIP string, dns []string) (string, error) {
	uuid, err := d.run(ctx, "create", "DHCP_Options", "cidr="+cidr)
	if err != nil {
		return "", err
	}

	serverMAC := randomServerMAC()
	// OVN put_dhcp_opts only accepts a single IP for dns_server in flow actions.
	// Use the first DNS server provided, or fall back to Google's resolver.
	dnsStr := "8.8.8.8"
	if len(dns) > 0 {
		dnsStr = dns[0]
	}

	opts := []string{
		"set", "DHCP_Options", uuid,
		fmt.Sprintf("options:server_id=%s", gatewayIP),
		fmt.Sprintf(`options:server_mac="%s"`, serverMAC),
		"options:lease_time=86400",
		fmt.Sprintf("options:router=%s", gatewayIP),
		fmt.Sprintf(`options:dns_server="%s"`, dnsStr),
	}
	if _, err := d.run(ctx, opts...); err != nil {
		return "", err
	}
	return uuid, nil
}

func (d *Driver) DeleteDHCPOptions(ctx context.Context, uuid string) error {
	if uuid == "" {
		return nil
	}
	_, err := d.run(ctx, "destroy", "DHCP_Options", uuid)
	return err
}

func (d *Driver) CreateLogicalPort(ctx context.Context, networkID, portID, macAddr, fixedIP, dhcpUUID string) error {
	if _, err := d.run(ctx, "--may-exist", "lsp-add", networkID, portID); err != nil {
		return err
	}
	if _, err := d.run(ctx, "lsp-set-addresses", portID, macAddr+" "+fixedIP); err != nil {
		return err
	}
	if dhcpUUID != "" {
		if _, err := d.run(ctx, "lsp-set-dhcpv4-options", portID, dhcpUUID); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) DeleteLogicalPort(ctx context.Context, portID string) error {
	_, err := d.run(ctx, "--if-exists", "lsp-del", portID)
	return err
}

func (d *Driver) BindPort(ctx context.Context, portID, chassisID string) error {
	_, err := d.run(ctx, "lsp-set-options", portID, "requested-chassis="+chassisID)
	return err
}

func (d *Driver) UnbindPort(ctx context.Context, portID string) error {
	_, err := d.run(ctx, "lsp-set-options", portID, "requested-chassis=")
	return err
}

// ─── Router ───────────────────────────────────────────────────────────────────

func (d *Driver) CreateLogicalRouter(ctx context.Context, routerID string) error {
	_, err := d.run(ctx, "--may-exist", "lr-add", routerID)
	return err
}

func (d *Driver) DeleteLogicalRouter(ctx context.Context, routerID string) error {
	_, err := d.run(ctx, "--if-exists", "lr-del", routerID)
	return err
}

// AddRouterInterface creates an LRP on the router and an LSP of type "router" on the switch.
func (d *Driver) AddRouterInterface(ctx context.Context, routerID, networkID, routerPortName, switchPortName, mac, ipPrefix string) error {
	if _, err := d.run(ctx, "--may-exist", "lrp-add", routerID, routerPortName, mac, ipPrefix); err != nil {
		return err
	}
	d.run(ctx, "--may-exist", "lsp-add", networkID, switchPortName) //nolint:errcheck
	d.run(ctx, "lsp-set-type", switchPortName, "router")            //nolint:errcheck
	d.run(ctx, "lsp-set-addresses", switchPortName, "router")       //nolint:errcheck
	_, err := d.run(ctx, "lsp-set-options", switchPortName, "router-port="+routerPortName)
	return err
}

func (d *Driver) RemoveRouterInterface(ctx context.Context, routerID, networkID, routerPortName, switchPortName string) error {
	d.run(ctx, "--if-exists", "lsp-del", switchPortName) //nolint:errcheck
	_, err := d.run(ctx, "--if-exists", "lrp-del", routerPortName)
	return err
}

// SetRouterGateway connects a router to an external switch and programs the default route,
// SNAT rules, and designates the gateway chassis for L3 forwarding.
func (d *Driver) SetRouterGateway(ctx context.Context, routerID, extNetworkID, extRouterPortName, extSwitchPortName, extMAC, extIPPrefix, nextHopIP, snatExtIP string, snatCIDRs []string, chassisName string) error {
	if _, err := d.run(ctx, "--may-exist", "lrp-add", routerID, extRouterPortName, extMAC, extIPPrefix); err != nil {
		return err
	}
	d.run(ctx, "--may-exist", "lsp-add", extNetworkID, extSwitchPortName)              //nolint:errcheck
	d.run(ctx, "lsp-set-type", extSwitchPortName, "router")                            //nolint:errcheck
	d.run(ctx, "lsp-set-addresses", extSwitchPortName, "router")                       //nolint:errcheck
	d.run(ctx, "lsp-set-options", extSwitchPortName, "router-port="+extRouterPortName) //nolint:errcheck
	// Delete any stale default route before re-adding so that a changed next-hop
	// (e.g. gateway re-set) takes effect immediately. Also pin the output port so
	// OVN can resolve the next-hop through the correct LRP.
	d.run(ctx, "--if-exists", "lr-route-del", routerID, "0.0.0.0/0") //nolint:errcheck
	if _, err := d.run(ctx, "lr-route-add", routerID, "0.0.0.0/0", nextHopIP, extRouterPortName); err != nil {
		return err
	}
	for _, cidr := range snatCIDRs {
		d.run(ctx, "--may-exist", "lr-nat-add", routerID, "snat", snatExtIP, cidr) //nolint:errcheck
	}
	if chassisName != "" {
		// Priority must be >= 1 for ovn-controller to elect this chassis as the
		// active gateway.  Priority 0 causes the chassisredirect port to remain
		// unbound (up=false) and SNAT/DNAT never fires.
		d.run(ctx, "lrp-set-gateway-chassis", extRouterPortName, chassisName, "1") //nolint:errcheck
	}
	return nil
}

func (d *Driver) ClearRouterGateway(ctx context.Context, routerID, extNetworkID, extRouterPortName, extSwitchPortName, snatExtIP string) error {
	d.run(ctx, "--if-exists", "lsp-del", extSwitchPortName)          //nolint:errcheck
	d.run(ctx, "--if-exists", "lrp-del", extRouterPortName)          //nolint:errcheck
	d.run(ctx, "--if-exists", "lr-route-del", routerID, "0.0.0.0/0") //nolint:errcheck
	if snatExtIP != "" {
		d.run(ctx, "--if-exists", "lr-nat-del", routerID, "snat", snatExtIP) //nolint:errcheck
	}
	return nil
}

func (d *Driver) AddRouterNAT(ctx context.Context, routerID, snatExtIP, logicalCIDR string) error {
	_, err := d.run(ctx, "--may-exist", "lr-nat-add", routerID, "snat", snatExtIP, logicalCIDR)
	return err
}

// AddFloatingIP installs a dnat_and_snat rule mapping floatingIP ↔ fixedIP on the router.
// AddFloatingIP installs a dnat_and_snat rule mapping floatingIP ↔ fixedIP on the router.
// Any pre-existing dnat_and_snat rule for this floatingIP is removed first so
// that re-association (moving a FIP to a different port) and retry-after-failure
// both work without hitting OVN's "NAT with this type/external_ip already exists" error.
func (d *Driver) AddFloatingIP(ctx context.Context, routerID, floatingIP, fixedIP string) error {
	// Remove any stale rule for this external IP before (re-)adding. --if-exists
	// makes this a no-op when there is nothing to remove.
	d.run(ctx, "--if-exists", "lr-nat-del", routerID, "dnat_and_snat", floatingIP) //nolint:errcheck
	_, err := d.run(ctx, "lr-nat-add", routerID, "dnat_and_snat", floatingIP, fixedIP)
	return err
}

// RemoveFloatingIP deletes the dnat_and_snat rule for floatingIP on the router.
func (d *Driver) RemoveFloatingIP(ctx context.Context, routerID, floatingIP string) error {
	_, err := d.run(ctx, "--if-exists", "lr-nat-del", routerID, "dnat_and_snat", floatingIP)
	return err
}

// ─── Security Groups (Port Groups + ACLs) ─────────────────────────────────────

// SGRule is the driver-level representation of a security group rule.
type SGRule struct {
	Direction      string `json:"direction"`
	Protocol       string `json:"protocol,omitempty"`
	PortMin        int    `json:"port_min,omitempty"`
	PortMax        int    `json:"port_max,omitempty"`
	RemoteCIDR     string `json:"remote_cidr,omitempty"`
	RemoteSGPGName string `json:"remote_sg_pg_name,omitempty"` // OVN port group whose address set to match
	Ethertype      string `json:"ethertype,omitempty"`
}

// CreatePortGroup creates an OVN port group for a security group.
func (d *Driver) CreatePortGroup(ctx context.Context, pgName string) error {
	_, err := d.run(ctx, "pg-add", pgName)
	return err
}

// DeletePortGroup deletes an OVN port group and all its ACLs.
func (d *Driver) DeletePortGroup(ctx context.Context, pgName string) error {
	_, err := d.run(ctx, "--if-exists", "pg-del", pgName)
	return err
}

// SyncPortGroupACLs replaces all ACLs on a port group with the given rules.
// Defaults are always applied: allow ARP, allow-all egress, deny-all ingress.
// Note: acl-add/del/list use the port group name directly; @pgName is only
// used inside match expressions.
func (d *Driver) SyncPortGroupACLs(ctx context.Context, pgName string, rules []SGRule) error {
	// Clear existing ACLs on the port group.
	d.run(ctx, "acl-del", pgName) //nolint:errcheck

	defaults := [][]string{
		{"acl-add", pgName, "to-lport", "200", "outport == @" + pgName + " && arp", "allow"},
		{"acl-add", pgName, "from-lport", "200", "inport == @" + pgName + " && arp", "allow"},
		{"acl-add", pgName, "from-lport", "100", "inport == @" + pgName + " && ip4", "allow-related"},
		{"acl-add", pgName, "from-lport", "100", "inport == @" + pgName + " && ip6", "allow-related"},
		{"acl-add", pgName, "to-lport", "50", "outport == @" + pgName + " && ip4", "drop"},
		{"acl-add", pgName, "to-lport", "50", "outport == @" + pgName + " && ip6", "drop"},
	}
	for _, args := range defaults {
		if _, err := d.run(ctx, args...); err != nil {
			return fmt.Errorf("default ACL %v: %w", args[3:5], err)
		}
	}

	// Apply user-defined rules at p1000
	for _, rule := range rules {
		ovnDir, match := buildACLMatch(pgName, rule)
		if _, err := d.run(ctx, "acl-add", pgName, ovnDir, "1000", match, "allow-related"); err != nil {
			return err
		}
	}
	return nil
}

// buildACLMatch converts a SGRule into (ovn-direction, match-string).
func buildACLMatch(pgName string, rule SGRule) (string, string) {
	ethertype := rule.Ethertype
	if ethertype == "" {
		ethertype = "IPv4"
	}
	ipVer := "ip4"
	if ethertype == "IPv6" {
		ipVer = "ip6"
	}

	var ovnDir, portRef, cidrField string
	if rule.Direction == "ingress" {
		ovnDir = "to-lport"
		portRef = "outport"
		cidrField = ipVer + ".src"
	} else {
		ovnDir = "from-lport"
		portRef = "inport"
		cidrField = ipVer + ".dst"
	}

	parts := []string{portRef + " == @" + pgName, ipVer}

	switch rule.Protocol {
	case "tcp":
		parts = append(parts, "tcp")
		if rule.PortMin > 0 && rule.PortMax > 0 {
			if rule.PortMin == rule.PortMax {
				parts = append(parts, fmt.Sprintf("tcp.dst == %d", rule.PortMin))
			} else {
				parts = append(parts, fmt.Sprintf("tcp.dst >= %d && tcp.dst <= %d", rule.PortMin, rule.PortMax))
			}
		}
	case "udp":
		parts = append(parts, "udp")
		if rule.PortMin > 0 && rule.PortMax > 0 {
			if rule.PortMin == rule.PortMax {
				parts = append(parts, fmt.Sprintf("udp.dst == %d", rule.PortMin))
			} else {
				parts = append(parts, fmt.Sprintf("udp.dst >= %d && udp.dst <= %d", rule.PortMin, rule.PortMax))
			}
		}
	case "icmp":
		parts = append(parts, "icmp4")
	case "icmp6":
		parts = append(parts, "icmp6")
	}

	switch {
	case rule.RemoteSGPGName != "":
		// Match against the OVN address set automatically maintained for the remote port group.
		// OVN creates an address set named identically to the port group (referenced with $ prefix).
		parts = append(parts, cidrField+" == $"+rule.RemoteSGPGName)
	case rule.RemoteCIDR != "" && rule.RemoteCIDR != "0.0.0.0/0" && rule.RemoteCIDR != "::/0":
		parts = append(parts, cidrField+" == "+rule.RemoteCIDR)
	}

	return ovnDir, strings.Join(parts, " && ")
}

// ─── FWaaS (Router-level policies) ───────────────────────────────────────────

// FWRule is the driver-level representation of a firewall policy rule.
type FWRule struct {
	Direction string `json:"direction"` // "ingress" | "egress"
	Protocol  string `json:"protocol,omitempty"`
	PortMin   int    `json:"port_min,omitempty"`
	PortMax   int    `json:"port_max,omitempty"`
	SrcCIDR   string `json:"src_cidr,omitempty"`
	DstCIDR   string `json:"dst_cidr,omitempty"`
	Action    string `json:"action"` // "allow" | "drop"
	Priority  int    `json:"priority"`
}

// SyncRouterFirewall replaces all Pulsar-managed lr-policy entries on the OVN
// logical router with the supplied rules.
//
// Implementation uses OVN router policies (lr-policy-add / lr-policy-del) rather
// than switch ACLs.  Switch ACLs on the external switch are bypassed for localnet
// traffic (OVN adds a priority=110 "next;" rule in ls_in_pre_acl that skips
// ct_next for the router patch port and the localnet port), so ct.est / ct.new
// conditions never fire for packets from the physical network.  Router policies
// run AFTER DNAT (table 15, lr_in_policy), so they see the post-DNAT destination
// IP (the VM's internal IP) and work correctly for all traffic paths.
//
// dnatTargets is the list of internal (post-DNAT) IP addresses of VMs that are
// reachable via the router's floating IPs.  For each target a set of allow rules
// is installed at priority 1001+rule.Priority, and a catch-all drop is installed
// at priority 1000.
//
// If rules is nil (not just empty), no new policies are installed — the caller
// wants a full clear (used by DeleteFirewallPolicy).
func (d *Driver) SyncRouterFirewall(ctx context.Context, routerID string, dnatTargets []string, rules []FWRule) error {
	if routerID == "" {
		return fmt.Errorf("SyncRouterFirewall: router_id is required")
	}

	// Remove all existing Pulsar-managed lr-policy entries for this router by
	// deleting every policy whose match references one of the dnatTargets, then
	// re-adding the full desired set.  Since we always own the entire 1000–1999
	// priority band we can simply wipe and repopulate.
	if err := d.deleteRouterPolicies(ctx, routerID, dnatTargets); err != nil {
		return fmt.Errorf("SyncRouterFirewall cleanup: %w", err)
	}

	// If rules is nil, this is a "clear all" operation — stop here.
	if rules == nil || len(dnatTargets) == 0 {
		return nil
	}

	// For each DNAT target install the policy set.
	for _, internalIP := range dnatTargets {
		if internalIP == "" {
			continue
		}

		// Priority 1999: always allow established/related packets.
		// ct_dnat (table 7) runs before lr_in_policy (table 15), so reply
		// packets (TCP responses, SNAT/DNAT return traffic, ICMP replies) arrive
		// at lr_in_policy with ct.est or ct.rel set.  Without this rule the
		// catch-all drop below would block all return traffic, breaking outbound
		// connections from the VM and inbound TCP sessions whose replies use
		// ephemeral destination ports.
		ctMatch := fmt.Sprintf("ip4.dst == %s && (ct.est || ct.rel)", internalIP)
		if _, err := d.run(ctx, "lr-policy-add", routerID, "1999", ctMatch, "allow"); err != nil {
			return fmt.Errorf("fw lr-policy-add ct.est %s: %w", internalIP, err)
		}

		// User-defined allow/drop rules at priority 1001+rule.Priority.
		// Only ingress rules are relevant here (traffic toward the VM).
		// Egress rules (traffic leaving the VM) are not filtered via FWaaS at
		// the router level — they exit through lr_out_snat unchanged.
		for _, rule := range rules {
			if rule.Direction != "ingress" {
				continue
			}
			action := "allow"
			if rule.Action == "drop" || rule.Action == "deny" {
				action = "drop"
			}
			match := buildFWPolicyMatch(internalIP, rule)
			prio := fmt.Sprintf("%d", 1001+rule.Priority)
			if _, err := d.run(ctx, "lr-policy-add", routerID, prio, match, action); err != nil {
				return fmt.Errorf("fw lr-policy-add p%s %s: %w", prio, match, err)
			}
		}

		// Default catch-all drop at priority 1000 — anything not explicitly allowed
		// by user rules above.  New connections only; established/related traffic is
		// already handled by the priority-1999 ct.est rule above.
		dropMatch := fmt.Sprintf("ip4.dst == %s", internalIP)
		if _, err := d.run(ctx, "lr-policy-add", routerID, "1000", dropMatch, "drop"); err != nil {
			return fmt.Errorf("fw lr-policy-add default drop %s: %w", internalIP, err)
		}
	}
	return nil
}

// deleteRouterPolicies removes all lr-policy entries in the 1000–1999 priority
// band whose match references one of the given internalIPs.  This covers both
// per-rule entries (priority 1001+) and the catch-all drop (priority 1000).
func (d *Driver) deleteRouterPolicies(ctx context.Context, routerID string, internalIPs []string) error {
	out, err := d.run(ctx, "lr-policy-list", routerID)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}

	// lr-policy-list output (one line per policy):
	//   "      1001          ip4.dst == 192.168.139.2 && tcp.dst == 22           allow"
	//   "      1000                           ip4.dst == 192.168.139.2            drop"
	//
	// We parse: priority (first token), match (everything between priority and action),
	// action (last token).
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "Routing Policies" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		prio := fields[0]
		action := fields[len(fields)-1]
		match := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, prio), action))
		match = strings.TrimSpace(match)

		// Only touch our managed priority band (1000–1999).
		prioInt := 0
		if _, err := fmt.Sscanf(prio, "%d", &prioInt); err != nil {
			continue
		}
		if prioInt < 1000 || prioInt > 1999 {
			continue
		}

		// Only delete if the match references one of our DNAT targets.
		for _, ip := range internalIPs {
			if ip != "" && strings.Contains(match, ip) {
				d.run(ctx, "lr-policy-del", routerID, prio, match) //nolint:errcheck
				break
			}
		}
	}
	return nil
}

// buildFWPolicyMatch constructs an OVN lr-policy match string for an ingress
// firewall rule targeting a specific internal IP (post-DNAT destination).
func buildFWPolicyMatch(internalIP string, rule FWRule) string {
	parts := []string{fmt.Sprintf("ip4.dst == %s", internalIP), "ip4"}

	switch rule.Protocol {
	case "tcp":
		parts = append(parts, "tcp")
		if rule.PortMin > 0 {
			if rule.PortMin == rule.PortMax || rule.PortMax == 0 {
				parts = append(parts, fmt.Sprintf("tcp.dst == %d", rule.PortMin))
			} else {
				parts = append(parts, fmt.Sprintf("tcp.dst >= %d && tcp.dst <= %d", rule.PortMin, rule.PortMax))
			}
		}
	case "udp":
		parts = append(parts, "udp")
		if rule.PortMin > 0 {
			if rule.PortMin == rule.PortMax || rule.PortMax == 0 {
				parts = append(parts, fmt.Sprintf("udp.dst == %d", rule.PortMin))
			} else {
				parts = append(parts, fmt.Sprintf("udp.dst >= %d && udp.dst <= %d", rule.PortMin, rule.PortMax))
			}
		}
	case "icmp":
		parts = append(parts, "icmp4")
	}

	if rule.SrcCIDR != "" && rule.SrcCIDR != "0.0.0.0/0" {
		parts = append(parts, "ip4.src == "+rule.SrcCIDR)
	}

	return strings.Join(parts, " && ")
}

// AddPortToGroup adds a logical switch port to an OVN port group.
// Uses the OVSDB shorthand: get LSP by name, add its UUID to the Port_Group ports column.
func (d *Driver) AddPortToGroup(ctx context.Context, pgName, portID string) error {
	_, err := d.run(ctx,
		"--", "--id=@lsp", "get", "Logical_Switch_Port", portID,
		"--", "add", "Port_Group", pgName, "ports", "@lsp",
	)
	return err
}

// RemovePortFromGroup removes a logical switch port from an OVN port group.
func (d *Driver) RemovePortFromGroup(ctx context.Context, pgName, portID string) error {
	_, err := d.run(ctx,
		"--", "--id=@lsp", "get", "Logical_Switch_Port", portID,
		"--", "remove", "Port_Group", pgName, "ports", "@lsp",
	)
	return err
}

func randomServerMAC() string {
	b := make([]byte, 3)
	rand.Read(b) //nolint:errcheck
	return fmt.Sprintf("c0:ff:ee:%02x:%02x:%02x", b[0], b[1], b[2])
}
