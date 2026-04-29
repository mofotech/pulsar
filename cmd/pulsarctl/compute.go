package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newComputeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compute",
		Short: "Compute resources (instances, flavors, nodes)",
	}
	cmd.AddCommand(
		newInstanceCmd(),
		newFlavorCmd(),
		newNodeCmd(),
	)
	return cmd
}

// ─── Instances ────────────────────────────────────────────────────────────────

func newInstanceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "instances", Short: "Manage instances", Aliases: []string{"instance"}}
	cmd.AddCommand(
		instanceListCmd(),
		instanceGetCmd(),
		instanceCreateCmd(),
		instanceDeleteCmd(),
		instanceActionCmd(),
	)
	return cmd
}

func instanceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/compute/instances"), tok, nil)
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
			printTable("ID|NAME|STATUS|IPs|NODE", func() [][]string {
				rows := make([][]string, len(items))
				for i, it := range items {
					var ips []string
					var m map[string]json.RawMessage
					if json.Unmarshal(it, &m) == nil {
						var addrs []json.RawMessage
						if json.Unmarshal(m["addresses"], &addrs) == nil {
							for _, a := range addrs {
								var addr struct {
									IPAddress string `json:"ip_address"`
								}
								if json.Unmarshal(a, &addr) == nil && addr.IPAddress != "" {
									ips = append(ips, addr.IPAddress)
								}
							}
						}
					}
					ipStr := strings.Join(ips, ", ")
					if ipStr == "" {
						ipStr = "-"
					}
					rows[i] = []string{
						field(it, "id"), field(it, "name"), field(it, "status"),
						ipStr, field(it, "node_id"),
					}
				}
				return rows
			}())
			return nil
		},
	}
}

func instanceGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show instance details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/compute/instances/"+args[0]), tok, nil)
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
}

func instanceCreateCmd() *cobra.Command {
	var name, flavorID, imageID, hypervisor, networks, userData string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			type netReq struct {
				NetworkID string `json:"network_id"`
			}
			var nets []netReq
			for _, n := range strings.Split(networks, ",") {
				n = strings.TrimSpace(n)
				if n != "" {
					nets = append(nets, netReq{NetworkID: n})
				}
			}
			body := map[string]interface{}{
				"name":            name,
				"flavor_id":       flavorID,
				"image_id":        imageID,
				"networks":        nets,
				"user_data":       userData,
				"hypervisor_type": hypervisor,
			}
			data, err := do("POST", apiURL(ep, "/v1/compute/instances"), tok, body)
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Instance name")
	cmd.Flags().StringVar(&flavorID, "flavor", "", "Flavor ID")
	cmd.Flags().StringVar(&imageID, "image", "", "Image ID")
	cmd.Flags().StringVar(&hypervisor, "hypervisor", "kvm", "Hypervisor type (kvm|lxd|lxc|containerd)")
	cmd.Flags().StringVar(&networks, "networks", "", "Comma-separated network IDs")
	cmd.Flags().StringVar(&userData, "user-data", "", "Cloud-init user data")
	cmd.MarkFlagRequired("name")   //nolint:errcheck
	cmd.MarkFlagRequired("flavor") //nolint:errcheck
	cmd.MarkFlagRequired("image")  //nolint:errcheck
	return cmd
}

func instanceDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			if _, err := do("DELETE", apiURL(ep, "/v1/compute/instances/"+args[0]), tok, nil); err != nil {
				return err
			}
			fmt.Printf("Instance %s deleted.\n", args[0])
			return nil
		},
	}
}

func instanceActionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "action <id> <start|stop|reboot|hard-reboot|console>",
		Short: "Perform an action on an instance",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("POST", apiURL(ep, "/v1/compute/instances/"+args[0]+"/action"), tok,
				map[string]string{"action": args[1]})
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
}

// ─── Flavors ──────────────────────────────────────────────────────────────────

func newFlavorCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "flavors", Short: "Manage flavors", Aliases: []string{"flavor"}}
	cmd.AddCommand(flavorListCmd(), flavorGetCmd(), flavorCreateCmd(), flavorDeleteCmd())
	return cmd
}

func flavorListCmd() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List flavors",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/compute/flavors"), tok, nil)
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
			printTable("ID|NAME|VCPUs|RAM (MB)|DISK (GB)", func() [][]string {
				rows := make([][]string, len(items))
				for i, it := range items {
					rows[i] = []string{
						field(it, "id"), field(it, "name"),
						field(it, "vcpus"), field(it, "ram_mb"), field(it, "disk_gb"),
					}
				}
				return rows
			}())
			return nil
		},
	}
}

func flavorGetCmd() *cobra.Command {
	return &cobra.Command{
		Use: "get <id>", Short: "Show flavor details", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/compute/flavors/"+args[0]), tok, nil)
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
}

func flavorCreateCmd() *cobra.Command {
	var name string
	var vcpus, ramMB, diskGB int
	cmd := &cobra.Command{
		Use: "create", Short: "Create a flavor",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("POST", apiURL(ep, "/v1/compute/flavors"), tok, map[string]interface{}{
				"name": name, "vcpus": vcpus, "ram_mb": ramMB, "disk_gb": diskGB,
			})
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Flavor name")
	cmd.Flags().IntVar(&vcpus, "vcpus", 1, "vCPU count")
	cmd.Flags().IntVar(&ramMB, "ram", 512, "RAM in MB")
	cmd.Flags().IntVar(&diskGB, "disk", 10, "Root disk in GB")
	cmd.MarkFlagRequired("name") //nolint:errcheck
	return cmd
}

func flavorDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use: "delete <id>", Short: "Delete a flavor", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			if _, err := do("DELETE", apiURL(ep, "/v1/compute/flavors/"+args[0]), tok, nil); err != nil {
				return err
			}
			fmt.Printf("Flavor %s deleted.\n", args[0])
			return nil
		},
	}
}

// ─── Nodes ────────────────────────────────────────────────────────────────────

func newNodeCmd() *cobra.Command {
	return &cobra.Command{
		Use: "nodes", Short: "List compute nodes",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/compute/nodes"), tok, nil)
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
			// capabilities is a nested object — extract fields manually
			printTable("AGENT ID|HYPERVISORS|VCPUs USED/TOTAL|RAM USED/TOTAL", func() [][]string {
				rows := make([][]string, len(items))
				for i, it := range items {
					var m map[string]json.RawMessage
					json.Unmarshal(it, &m) //nolint:errcheck
					agentID := field(it, "agent_id")
					var caps map[string]json.RawMessage
					hvTypes := ""
					vcpuUsed, vcpuTotal, ramUsed, ramTotal := "", "", "", ""
					if c, ok := m["capabilities"]; ok {
						json.Unmarshal(c, &caps) //nolint:errcheck
						if h, ok := caps["hypervisor_types"]; ok {
							hvTypes = strings.Trim(string(h), "[]\"")
						}
						vcpuUsed = strings.Trim(string(caps["vcpus_used"]), `"`)
						vcpuTotal = strings.Trim(string(caps["vcpus_total"]), `"`)
						ramUsed = strings.Trim(string(caps["ram_mb_used"]), `"`)
						ramTotal = strings.Trim(string(caps["ram_mb_total"]), `"`)
					}
					rows[i] = []string{
						agentID, hvTypes,
						vcpuUsed + "/" + vcpuTotal,
						ramUsed + "/" + ramTotal,
					}
				}
				return rows
			}())
			return nil
		},
	}
}
