package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

func newStorageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "storage", Short: "Storage resources"}
	cmd.AddCommand(
		storVolumesCmd(),
		storSnapshotsCmd(),
		storVolumeTypesCmd(),
	)
	return cmd
}

// ─── Volumes ──────────────────────────────────────────────────────────────────

func storVolumesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "volumes", Short: "Manage volumes"}
	cmd.AddCommand(
		simpleListCmd("volumes", "/v1/storage/volumes", "ID|NAME|SIZE (GB)|STATUS|TYPE",
			func(it json.RawMessage) []string {
				return []string{field(it, "id"), field(it, "name"), field(it, "size_gb"), field(it, "status"), field(it, "volume_type")}
			},
		),
		simpleGetCmd("volume", "/v1/storage/volumes"),
		func() *cobra.Command {
			var name, volumeType, sourceSnap string
			var sizeGB int
			c := &cobra.Command{
				Use: "create", Short: "Create a volume",
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					body := map[string]interface{}{
						"name":        name,
						"size_gb":     sizeGB,
						"volume_type": volumeType,
					}
					if sourceSnap != "" {
						body["source_snapshot_id"] = sourceSnap
					}
					data, err := do("POST", apiURL(ep, "/v1/storage/volumes"), tok, body)
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&name, "name", "", "Volume name")
			c.Flags().IntVar(&sizeGB, "size", 10, "Size in GB")
			c.Flags().StringVar(&volumeType, "type", "", "Volume type ID")
			c.Flags().StringVar(&sourceSnap, "snapshot", "", "Source snapshot ID")
			c.MarkFlagRequired("name") //nolint:errcheck
			return c
		}(),
		simpleDeleteCmd("volume", "/v1/storage/volumes"),
		func() *cobra.Command {
			return &cobra.Command{
				Use:   "action <id> <attach|detach|extend>",
				Short: "Perform an action on a volume",
				Args:  cobra.ExactArgs(2),
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					data, err := do("POST", apiURL(ep, "/v1/storage/volumes/"+args[0]+"/action"), tok,
						map[string]string{"action": args[1]})
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
		}(),
	)
	return cmd
}

// ─── Snapshots ────────────────────────────────────────────────────────────────

func storSnapshotsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "snapshots", Short: "Manage volume snapshots"}
	cmd.AddCommand(
		simpleListCmd("snapshots", "/v1/storage/snapshots", "ID|NAME|VOLUME|SIZE (GB)|STATUS",
			func(it json.RawMessage) []string {
				return []string{field(it, "id"), field(it, "name"), field(it, "volume_id"), field(it, "size_gb"), field(it, "status")}
			},
		),
		simpleGetCmd("snapshot", "/v1/storage/snapshots"),
		func() *cobra.Command {
			var name, volumeID, description string
			c := &cobra.Command{
				Use: "create", Short: "Create a snapshot",
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					data, err := do("POST", apiURL(ep, "/v1/storage/snapshots"), tok, map[string]interface{}{
						"name": name, "volume_id": volumeID, "description": description,
					})
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&name, "name", "", "Snapshot name")
			c.Flags().StringVar(&volumeID, "volume", "", "Source volume ID")
			c.Flags().StringVar(&description, "description", "", "Description")
			c.MarkFlagRequired("name")   //nolint:errcheck
			c.MarkFlagRequired("volume") //nolint:errcheck
			return c
		}(),
		simpleDeleteCmd("snapshot", "/v1/storage/snapshots"),
	)
	return cmd
}

// ─── Volume Types ─────────────────────────────────────────────────────────────

func storVolumeTypesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "volume-types", Short: "Manage volume types"}
	cmd.AddCommand(
		simpleListCmd("volume-types", "/v1/storage/volume-types", "ID|NAME|BACKEND",
			func(it json.RawMessage) []string {
				return []string{field(it, "id"), field(it, "name"), field(it, "backend")}
			},
		),
		simpleGetCmd("volume-type", "/v1/storage/volume-types"),
		func() *cobra.Command {
			var name, backend string
			c := &cobra.Command{
				Use: "create", Short: "Create a volume type",
				RunE: func(cmd *cobra.Command, args []string) error {
					ep, tok, err := resolve()
					if err != nil {
						return err
					}
					data, err := do("POST", apiURL(ep, "/v1/storage/volume-types"), tok,
						map[string]interface{}{"name": name, "backend": backend})
					if err != nil {
						return err
					}
					printJSON(data)
					return nil
				},
			}
			c.Flags().StringVar(&name, "name", "", "Volume type name")
			c.Flags().StringVar(&backend, "backend", "lvm", "Storage backend (lvm|ceph|nfs)")
			c.MarkFlagRequired("name") //nolint:errcheck
			return c
		}(),
		simpleDeleteCmd("volume-type", "/v1/storage/volume-types"),
	)
	return cmd
}

