package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "network", Short: "Network resources"}
	cmd.AddCommand(
		netNetworksCmd(),
		netSubnetsCmd(),
		netPortsCmd(),
		netRoutersCmd(),
		netFloatingIPsCmd(),
		netSecurityGroupsCmd(),
	)
	return cmd
}

// ─── Networks ─────────────────────────────────────────────────────────────────

func netNetworksCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "networks", Short: "Manage networks"}
	cmd.AddCommand(
		simpleListCmd("networks", "/v1/network/networks", "ID|NAME|TYPE|VNI|STATUS",
			func(it json.RawMessage) []string {
				return []string{field(it, "id"), field(it, "name"), field(it, "type"), field(it, "vni"), field(it, "status")}
			},
		),
		simpleGetCmd("network", "/v1/network/networks"),
		func() *cobra.Command {
			var name, netType, physnet string
			var vlanID int
			c := &cobra.Command{
				Use: "create", Short: "Create a network",
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					body := map[string]interface{}{"name": name, "type": netType}
					if vlanID > 0 {
						body["vlan_id"] = vlanID
					}
					if physnet != "" {
						body["physnet"] = physnet
					}
					data, err := do("POST", apiURL(ep, "/v1/network/networks"), tok, body)
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&name, "name", "", "Network name")
			c.Flags().StringVar(&netType, "type", "vxlan", "Network type: vxlan (default), vlan, flat")
			c.Flags().IntVar(&vlanID, "vlan-id", 0, "VLAN tag (vlan type only)")
			c.Flags().StringVar(&physnet, "physnet", "", "Physical network name (vlan/flat only)")
			c.MarkFlagRequired("name") //nolint:errcheck
			return c
		}(),
		simpleDeleteCmd("network", "/v1/network/networks"),
	)
	return cmd
}

// ─── Subnets ──────────────────────────────────────────────────────────────────

func netSubnetsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "subnets", Short: "Manage subnets"}
	cmd.AddCommand(
		simpleListCmd("subnets", "/v1/network/subnets", "ID|NETWORK|CIDR|GATEWAY",
			func(it json.RawMessage) []string {
				return []string{field(it, "id"), field(it, "network_id"), field(it, "cidr"), field(it, "gateway_ip")}
			},
		),
		simpleGetCmd("subnet", "/v1/network/subnets"),
		func() *cobra.Command {
			var name, networkID, cidr, gateway string
			c := &cobra.Command{
				Use: "create", Short: "Create a subnet and program OVN DHCP",
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					body := map[string]interface{}{
						"network_id": networkID,
						"cidr":       cidr,
					}
					if name != "" {
						body["name"] = name
					}
					if gateway != "" {
						body["gateway_ip"] = gateway
					}
					data, err := do("POST", apiURL(ep, "/v1/network/subnets"), tok, body)
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&name, "name", "", "Subnet name")
			c.Flags().StringVar(&networkID, "network", "", "Network ID")
			c.Flags().StringVar(&cidr, "cidr", "", "CIDR block (e.g. 192.168.100.0/24)")
			c.Flags().StringVar(&gateway, "gateway", "", "Gateway IP (default: first host in CIDR)")
			c.MarkFlagRequired("network") //nolint:errcheck
			c.MarkFlagRequired("cidr")    //nolint:errcheck
			return c
		}(),
		simpleDeleteCmd("subnet", "/v1/network/subnets"),
	)
	return cmd
}

// ─── Ports ────────────────────────────────────────────────────────────────────

func netPortsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ports", Short: "Manage ports"}
	cmd.AddCommand(
		simpleListCmd("ports", "/v1/network/ports", "ID|NAME|NETWORK|ADDRESS|MAC|STATUS",
			func(it json.RawMessage) []string {
				return []string{
					field(it, "id"),
					field(it, "name"),
					field(it, "network_id"),
					fieldArr(it, "fixed_ips", "ip_address"),
					field(it, "mac_address"),
					field(it, "status"),
				}
			},
		),
		simpleGetCmd("port", "/v1/network/ports"),
		func() *cobra.Command {
			var name, networkID string
			c := &cobra.Command{
				Use: "create", Short: "Create a port",
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					data, err := do("POST", apiURL(ep, "/v1/network/ports"), tok,
						map[string]interface{}{"name": name, "network_id": networkID})
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&name, "name", "", "Port name")
			c.Flags().StringVar(&networkID, "network", "", "Network ID")
			c.MarkFlagRequired("network") //nolint:errcheck
			return c
		}(),
		simpleDeleteCmd("port", "/v1/network/ports"),
	)
	return cmd
}

// ─── Routers ──────────────────────────────────────────────────────────────────

func netRoutersCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "routers", Short: "Manage routers"}
	cmd.AddCommand(
		simpleListCmd("routers", "/v1/network/routers", "ID|NAME|STATUS",
			func(it json.RawMessage) []string {
				return []string{field(it, "id"), field(it, "name"), field(it, "status")}
			},
		),
		simpleGetCmd("router", "/v1/network/routers"),
		func() *cobra.Command {
			var name string
			c := &cobra.Command{
				Use: "create", Short: "Create a router",
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					data, err := do("POST", apiURL(ep, "/v1/network/routers"), tok,
						map[string]interface{}{"name": name})
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&name, "name", "", "Router name")
			c.MarkFlagRequired("name") //nolint:errcheck
			return c
		}(),
		simpleDeleteCmd("router", "/v1/network/routers"),
		func() *cobra.Command {
			var subnetID string
			c := &cobra.Command{
				Use: "add-interface <router-id>", Short: "Add a subnet interface to a router",
				Args: cobra.ExactArgs(1),
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					data, err := do("PUT", apiURL(ep, "/v1/network/routers/"+args[0]+"/interfaces"), tok,
						map[string]string{"subnet_id": subnetID})
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&subnetID, "subnet", "", "Subnet ID")
			c.MarkFlagRequired("subnet") //nolint:errcheck
			return c
		}(),
		func() *cobra.Command {
			var subnetID string
			c := &cobra.Command{
				Use: "remove-interface <router-id>", Short: "Remove a subnet interface from a router",
				Args: cobra.ExactArgs(1),
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					if _, err := do("DELETE", apiURL(ep, "/v1/network/routers/"+args[0]+"/interfaces"), tok,
						map[string]string{"subnet_id": subnetID}); err != nil {
						return err
					}
					fmt.Println("Interface removed.")
					return nil
				},
			}
			c.Flags().StringVar(&subnetID, "subnet", "", "Subnet ID")
			c.MarkFlagRequired("subnet") //nolint:errcheck
			return c
		}(),
		func() *cobra.Command {
			var extNetwork, extIP string
			c := &cobra.Command{
				Use:   "set-gateway <router-id>",
				Short: "Set external gateway and enable SNAT",
				Args:  cobra.ExactArgs(1),
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					body := map[string]interface{}{"external_network_id": extNetwork}
					if extIP != "" {
						body["external_ip"] = extIP
					}
					data, err := do("PUT", apiURL(ep, "/v1/network/routers/"+args[0]+"/gateway"), tok, body)
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&extNetwork, "external-network", "", "External network ID")
			c.Flags().StringVar(&extIP, "external-ip", "", "External IP (auto-allocated if omitted)")
			c.MarkFlagRequired("external-network") //nolint:errcheck
			return c
		}(),
		func() *cobra.Command {
			return &cobra.Command{
				Use:   "clear-gateway <router-id>",
				Short: "Remove external gateway",
				Args:  cobra.ExactArgs(1),
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					if _, err := do("DELETE", apiURL(ep, "/v1/network/routers/"+args[0]+"/gateway"), tok, nil); err != nil {
						return err
					}
					fmt.Println("Gateway cleared.")
					return nil
				},
			}
		}(),
	)
	return cmd
}

// ─── Floating IPs ─────────────────────────────────────────────────────────────

func netFloatingIPsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "floatingips", Short: "Manage floating IPs"}
	cmd.AddCommand(
		simpleListCmd("floatingips", "/v1/network/floatingips", "ID|FLOATING IP|FIXED IP|STATUS",
			func(it json.RawMessage) []string {
				return []string{field(it, "id"), field(it, "floating_ip_address"), field(it, "fixed_ip_address"), field(it, "status")}
			},
		),
		simpleGetCmd("floatingip", "/v1/network/floatingips"),
		func() *cobra.Command {
			var networkID, portID string
			c := &cobra.Command{
				Use: "create", Short: "Allocate a floating IP",
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					body := map[string]interface{}{"floating_network_id": networkID}
					if portID != "" {
						body["port_id"] = portID
					}
					data, err := do("POST", apiURL(ep, "/v1/network/floatingips"), tok, body)
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&networkID, "network", "", "External network ID")
			c.Flags().StringVar(&portID, "port", "", "Port ID to associate immediately")
			c.MarkFlagRequired("network") //nolint:errcheck
			return c
		}(),
		func() *cobra.Command {
			var portID string
			c := &cobra.Command{
				Use: "associate <floatingip-id>", Short: "Associate a floating IP with a port",
				Args: cobra.ExactArgs(1),
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					data, err := do("PATCH", apiURL(ep, "/v1/network/floatingips/"+args[0]), tok,
						map[string]string{"port_id": portID})
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&portID, "port", "", "Port ID")
			c.MarkFlagRequired("port") //nolint:errcheck
			return c
		}(),
		simpleDeleteCmd("floatingip", "/v1/network/floatingips"),
	)
	return cmd
}

// ─── Security Groups ──────────────────────────────────────────────────────────

func netSecurityGroupsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "security-groups", Short: "Manage security groups"}
	cmd.AddCommand(
		simpleListCmd("security-groups", "/v1/network/security-groups", "ID|NAME|DESCRIPTION",
			func(it json.RawMessage) []string {
				return []string{field(it, "id"), field(it, "name"), field(it, "description")}
			},
		),
		simpleGetCmd("security-group", "/v1/network/security-groups"),
		func() *cobra.Command {
			var name, description string
			c := &cobra.Command{
				Use: "create", Short: "Create a security group",
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					data, err := do("POST", apiURL(ep, "/v1/network/security-groups"), tok,
						map[string]interface{}{"name": name, "description": description})
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&name, "name", "", "Name")
			c.Flags().StringVar(&description, "description", "", "Description")
			c.MarkFlagRequired("name") //nolint:errcheck
			return c
		}(),
		simpleDeleteCmd("security-group", "/v1/network/security-groups"),
		func() *cobra.Command {
			var proto, direction, remoteIP, remoteSG, ethertype string
			var portMin, portMax int
			c := &cobra.Command{
				Use: "add-rule <security-group-id>", Short: "Add an ingress/egress rule",
				Args: cobra.ExactArgs(1),
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					body := map[string]interface{}{
						"protocol":  proto,
						"direction": direction,
						"ethertype": ethertype,
					}
					if remoteIP != "" {
						body["remote_ip_prefix"] = remoteIP
					}
					if remoteSG != "" {
						body["remote_group_id"] = remoteSG
					}
					if portMin != 0 {
						body["port_range_min"] = portMin
					}
					if portMax != 0 {
						body["port_range_max"] = portMax
					}
					data, err := do("POST", apiURL(ep, "/v1/network/security-groups/"+args[0]+"/rules"), tok, body)
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&proto, "protocol", "", "Protocol (tcp|udp|icmp|icmp6|empty for any)")
			c.Flags().StringVar(&direction, "direction", "ingress", "Direction (ingress|egress)")
			c.Flags().StringVar(&ethertype, "ethertype", "IPv4", "Ethertype (IPv4|IPv6)")
			c.Flags().StringVar(&remoteIP, "remote-ip", "", "Remote IP prefix (CIDR); mutually exclusive with --remote-sg")
			c.Flags().StringVar(&remoteSG, "remote-sg", "", "Remote security group ID; mutually exclusive with --remote-ip")
			c.Flags().IntVar(&portMin, "port-min", 0, "Port range min (tcp/udp only)")
			c.Flags().IntVar(&portMax, "port-max", 0, "Port range max (tcp/udp only)")
			return c
		}(),
		func() *cobra.Command {
			return &cobra.Command{
				Use: "delete-rule <security-group-id> <rule-id>", Short: "Delete a rule",
				Args: cobra.ExactArgs(2),
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					if _, err := do("DELETE",
						apiURL(ep, "/v1/network/security-groups/"+args[0]+"/rules/"+args[1]),
						tok, nil); err != nil {
						return err
					}
					fmt.Printf("Rule %s deleted.\n", args[1])
					return nil
				},
			}
		}(),
	)
	return cmd
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

func simpleListCmd(use, path, header string, row func(json.RawMessage) []string) *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List " + use,
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, path), tok, nil)
			if err != nil {
				return err
			}
			if globalJSON {
				printJSON(data)
				return nil
			}
			items, err := unmarshalSlice(data)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, it := range items {
				rows = append(rows, row(it))
			}
			printTable(header, rows)
			return nil
		},
	}
}

func simpleGetCmd(use, path string) *cobra.Command {
	return &cobra.Command{
		Use: "get <id>", Short: "Show " + use + " details", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, path+"/"+args[0]), tok, nil)
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
}

func simpleDeleteCmd(use, path string) *cobra.Command {
	return &cobra.Command{
		Use: "delete <id>", Short: "Delete " + use, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			if _, err := do("DELETE", apiURL(ep, path+"/"+args[0]), tok, nil); err != nil {
				return err
			}
			fmt.Printf("%s %s deleted.\n", use, args[0])
			return nil
		},
	}
}
