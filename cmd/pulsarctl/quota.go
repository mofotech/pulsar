package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func projectQuotaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "quota <project-id>",
		Short: "Show a project's quota limits and current usage",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/projects/"+args[0]+"/quotas"), tok, nil)
			if err != nil {
				return err
			}
			return printQuotaReport(data)
		},
	}
}

func projectSetQuotaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-quota <project-id> <resource>=<limit|unlimited|default>...",
		Short: "Change a project's quota limits (platform admin)",
		Long: "Set per-project quota limits. Resources: instances, vcpus, ram_mb, volumes,\n" +
			"storage_gb, floating_ips, networks, routers.\n\n" +
			"  pulsarctl projects set-quota <id> vcpus=40 ram_mb=102400\n" +
			"  pulsarctl projects set-quota <id> instances=unlimited   # or -1\n" +
			"  pulsarctl projects set-quota <id> vcpus=default         # back to the default",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			changes, err := parseQuotaArgs(args[1:])
			if err != nil {
				return err
			}
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("PUT", apiURL(ep, "/v1/projects/"+args[0]+"/quotas"), tok, changes)
			if err != nil {
				return err
			}
			return printQuotaReport(data)
		},
	}
}

// parseQuotaArgs turns ["vcpus=40", "volumes=default"] into the PUT body,
// where null resets a resource to the default.
func parseQuotaArgs(args []string) (map[string]*int, error) {
	out := make(map[string]*int, len(args))
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("invalid argument %q: want <resource>=<limit|unlimited|default>", a)
		}
		switch strings.ToLower(v) {
		case "default":
			out[k] = nil
		case "unlimited":
			n := -1
			out[k] = &n
		default:
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("invalid limit %q for %s", v, k)
			}
			out[k] = &n
		}
	}
	return out, nil
}

func printQuotaReport(data json.RawMessage) error {
	if globalJSON {
		printJSON(data)
		return nil
	}
	var report struct {
		Quotas []struct {
			Resource string `json:"resource"`
			Limit    int    `json:"limit"`
			Used     int    `json:"used"`
			Default  bool   `json:"default"`
		} `json:"quotas"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return err
	}
	var rows [][]string
	for _, q := range report.Quotas {
		limit := strconv.Itoa(q.Limit)
		if q.Limit < 0 {
			limit = "unlimited"
		}
		source := "override"
		if q.Default {
			source = "default"
		}
		rows = append(rows, []string{q.Resource, strconv.Itoa(q.Used), limit, source})
	}
	printTable("RESOURCE|USED|LIMIT|SOURCE", rows)
	return nil
}
