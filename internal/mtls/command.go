package mtls

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// NewCommand returns `pulsar certs`: a minimal CA for the agent gateway.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "certs",
		Short: "Create a CA and issue certificates for controller ↔ agent mTLS",
		Long: `Create a private CA and issue certificates for the agent gateway.

  pulsar certs ca --dir /etc/pulsar/pki
  pulsar certs controller --dir /etc/pulsar/pki --dns pulsar.example.com --ip 10.0.0.5
  pulsar certs agent --dir /etc/pulsar/pki --name compute-1     # name = the agent's node_id

Then configure:
  controller.grpc_tls: {ca_cert: ca.crt, cert: controller.crt, key: controller.key}
  agent.tls:           {ca_cert: ca.crt, cert: <node_id>.crt, key: <node_id>.key}

Keep ca.key offline once certificates are issued.`,
	}
	var dir string
	cmd.PersistentFlags().StringVar(&dir, "dir", ".", "Directory holding ca.crt/ca.key and issued certificates")

	var years int
	caCmd := &cobra.Command{
		Use:   "ca",
		Short: "Create ca.crt and ca.key (refuses to overwrite an existing CA)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := InitCA(dir, "Pulsar agent gateway CA", time.Duration(years)*365*24*time.Hour); err != nil {
				return err
			}
			fmt.Printf("wrote %s and %s\n", filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"))
			return nil
		},
	}
	caCmd.Flags().IntVar(&years, "years", 10, "CA validity in years")

	var dnsNames, ipStrs []string
	var days int
	controllerCmd := &cobra.Command{
		Use:   "controller",
		Short: "Issue controller.crt/.key (server cert; list every name and IP agents dial)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ips := make([]net.IP, 0, len(ipStrs))
			for _, s := range ipStrs {
				ip := net.ParseIP(strings.TrimSpace(s))
				if ip == nil {
					return fmt.Errorf("invalid --ip %q", s)
				}
				ips = append(ips, ip)
			}
			if len(dnsNames) == 0 && len(ips) == 0 {
				return fmt.Errorf("give at least one --dns or --ip that agents use to reach the controller")
			}
			if err := Issue(dir, dir, "controller", ServerAndClient, dnsNames, ips, time.Duration(days)*24*time.Hour); err != nil {
				return err
			}
			fmt.Printf("wrote %s and %s\n", filepath.Join(dir, "controller.crt"), filepath.Join(dir, "controller.key"))
			return nil
		},
	}
	controllerCmd.Flags().StringSliceVar(&dnsNames, "dns", nil, "DNS names of the controller (repeatable)")
	controllerCmd.Flags().StringSliceVar(&ipStrs, "ip", nil, "IP addresses of the controller (repeatable)")
	controllerCmd.Flags().IntVar(&days, "days", 825, "Validity in days")

	var name string
	var agentDays int
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Issue <name>.crt/.key for an agent; name must equal the agent's node_id",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" || strings.ContainsAny(name, `/\`) || name == "ca" || name == "controller" {
				return fmt.Errorf("--name must be the agent's node_id (not empty, 'ca' or 'controller')")
			}
			if err := Issue(dir, dir, name, ClientOnly, nil, nil, time.Duration(agentDays)*24*time.Hour); err != nil {
				return err
			}
			fmt.Printf("wrote %s and %s\n", filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key"))
			return nil
		},
	}
	agentCmd.Flags().StringVar(&name, "name", "", "Agent node_id (the certificate Common Name)")
	agentCmd.Flags().IntVar(&agentDays, "days", 825, "Validity in days")

	for _, c := range []*cobra.Command{caCmd, controllerCmd, agentCmd} {
		c.SilenceUsage = true // runtime errors (e.g. existing CA) aren't usage errors
	}
	cmd.AddCommand(caCmd, controllerCmd, agentCmd)
	return cmd
}
