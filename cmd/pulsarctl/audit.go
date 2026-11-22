package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

func newAuditCmd() *cobra.Command {
	var f struct {
		project, user, org, resourceType, resourceID, action, outcome, since, until string
		limit                                                                       int
		beforeID                                                                    int64
	}
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Show the audit log (org admins: their org; platform admins: all)",
		Example: "  pulsarctl audit --since 24h\n" +
			"  pulsarctl audit --outcome denied --action login\n" +
			"  pulsarctl audit --resource-type compute.instances --resource-id <id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			for k, v := range map[string]string{
				"project_id": f.project, "user_id": f.user, "org_id": f.org,
				"resource_type": f.resourceType, "resource_id": f.resourceID,
				"action": f.action, "outcome": f.outcome,
			} {
				if v != "" {
					q.Set(k, v)
				}
			}
			for k, v := range map[string]string{"since": f.since, "until": f.until} {
				if v == "" {
					continue
				}
				ts, err := parseSince(v, time.Now())
				if err != nil {
					return fmt.Errorf("--%s: %w", k, err)
				}
				q.Set(k, ts)
			}
			q.Set("limit", strconv.Itoa(f.limit))
			if f.beforeID > 0 {
				q.Set("before_id", strconv.FormatInt(f.beforeID, 10))
			}
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/audit?"+q.Encode()), tok, nil)
			if err != nil {
				return err
			}
			if globalJSON {
				printJSON(data)
				return nil
			}
			var events []struct {
				ID           int64     `json:"id"`
				OccurredAt   time.Time `json:"occurred_at"`
				ActorEmail   string    `json:"actor_email"`
				UserID       string    `json:"user_id"`
				Action       string    `json:"action"`
				ResourceType string    `json:"resource_type"`
				ResourceID   string    `json:"resource_id"`
				Outcome      string    `json:"outcome"`
				Status       int       `json:"status"`
				SourceIP     string    `json:"source_ip"`
			}
			if err := json.Unmarshal(data, &events); err != nil {
				return err
			}
			var rows [][]string
			for _, e := range events {
				actor := e.ActorEmail
				if actor == "" {
					actor = e.UserID
				}
				if actor == "" {
					actor = "-"
				}
				res := e.ResourceType
				if e.ResourceID != "" {
					res += "/" + e.ResourceID
				}
				rows = append(rows, []string{
					strconv.FormatInt(e.ID, 10), e.OccurredAt.Local().Format("2006-01-02 15:04:05"),
					actor, e.Action, res, e.Outcome, strconv.Itoa(e.Status), e.SourceIP,
				})
			}
			printTable("ID|TIME|ACTOR|ACTION|RESOURCE|OUTCOME|STATUS|SOURCE IP", rows)
			if len(events) == f.limit && len(events) > 0 {
				fmt.Printf("\nMore events: --before-id %d\n", events[len(events)-1].ID)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.project, "project", "", "Filter by project ID")
	fl.StringVar(&f.user, "user", "", "Filter by user ID")
	fl.StringVar(&f.org, "org", "", "Filter by org ID (platform admins)")
	fl.StringVar(&f.resourceType, "resource-type", "", "e.g. compute.instances, auth")
	fl.StringVar(&f.resourceID, "resource-id", "", "Filter by resource ID")
	fl.StringVar(&f.action, "action", "", "e.g. create, delete, login")
	fl.StringVar(&f.outcome, "outcome", "", "success | failure | denied")
	fl.StringVar(&f.since, "since", "", "Duration ago (24h, 7d) or RFC 3339 time")
	fl.StringVar(&f.until, "until", "", "Duration ago or RFC 3339 time")
	fl.IntVar(&f.limit, "limit", 50, "Max events (≤1000)")
	fl.Int64Var(&f.beforeID, "before-id", 0, "Page: events older than this ID")
	return cmd
}

// parseSince accepts an RFC 3339 time, or a duration ago such as 90m, 24h or 7d.
func parseSince(v string, now time.Time) (string, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if n := len(v); n > 1 && v[n-1] == 'd' {
		days, err := strconv.Atoi(v[:n-1])
		if err == nil {
			return now.Add(-time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339), nil
		}
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return "", fmt.Errorf("%q is neither a duration (24h, 7d) nor an RFC 3339 time", v)
	}
	return now.Add(-d).UTC().Format(time.RFC3339), nil
}
