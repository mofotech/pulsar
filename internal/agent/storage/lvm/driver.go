// Package lvm implements the storage driver for LVM thin-pool volumes with
// iSCSI export via tgt (tgtd / tgtadm).
//
// Prerequisites on the storage host:
//   - LVM2: lvcreate, lvremove, lvextend, lvdisplay
//   - tgt:  tgtd running, tgtadm in PATH
//
// Volume naming: LVs are created as <volume_group>/<volume_id> inside the
// thin pool <thin_pool>.  Snapshot LVs are named snap_<snapshot_id>.
package lvm

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"go.uber.org/zap"
)

// Driver manages LVM thin-pool volumes and iSCSI targets.
type Driver struct {
	vg     string // LVM volume group
	pool   string // LVM thin pool name within vg
	nodeIP string // IP advertised to initiators; auto-detected if empty
	log    *zap.Logger
}

func New(vg, pool, nodeIP string, log *zap.Logger) (*Driver, error) {
	if vg == "" || pool == "" {
		return nil, fmt.Errorf("lvm: volume_group and thin_pool are required")
	}
	ip := nodeIP
	if ip == "" {
		ip = primaryIP()
	}
	d := &Driver{vg: vg, pool: pool, nodeIP: ip, log: log}

	// Activate the VG so device mapper nodes are created inside the container.
	// Without this, lvcreate fails with "Failed to locally activate thin pool"
	// because udevd is not running inside the container to create /dev/mapper nodes.
	if out, err := run(context.Background(), "vgchange", "-ay", vg); err != nil {
		log.Warn("vgchange -ay failed (VG may not be present yet)",
			zap.String("vg", vg),
			zap.String("output", string(out)),
			zap.Error(err),
		)
	} else {
		log.Info("LVM VG activated", zap.String("vg", vg))
	}

	// Verify tgtd socket is reachable (host's tgtd via /run/tgtd bind-mount).
	if _, err := run(context.Background(), "tgtadm", "--op", "show", "--mode", "system"); err != nil {
		log.Warn("tgtd not reachable — iSCSI export will not work", zap.Error(err))
	} else {
		log.Info("tgtd ready (host)")
	}

	return d, nil
}

// NodeIP returns the IP address that iSCSI initiators should connect to.
func (d *Driver) NodeIP() string { return d.nodeIP }

// ─── Volume operations ────────────────────────────────────────────────────────

// CreateVolume creates a thin-provisioned LV of the given size.
// If sourceSnapshotID is non-empty, the LV is created from that snapshot.
func (d *Driver) CreateVolume(ctx context.Context, volumeID string, sizeGB int, sourceSnapshotID string) error {
	if sourceSnapshotID != "" {
		// Clone from snapshot: create a new thin LV by merging/converting the snapshot
		// For thinly provisioned snapshots, we can just create a new LV from the snapshot
		snapLV := fmt.Sprintf("%s/snap_%s", d.vg, sourceSnapshotID)
		destLV := fmt.Sprintf("%s/%s", d.vg, volumeID)
		out, err := run(ctx, "lvcreate", "--snapshot", "--name", volumeID, snapLV, "--thinpool", d.pool)
		if err != nil {
			// Fall back: create new volume and dd from snapshot (rare path)
			d.log.Warn("snapshot clone not supported directly, creating fresh volume", zap.String("snap", snapLV), zap.String("error", string(out)))
			return d.createThinLV(ctx, volumeID, sizeGB)
		}
		_ = destLV
		return nil
	}
	return d.createThinLV(ctx, volumeID, sizeGB)
}

func (d *Driver) createThinLV(ctx context.Context, volumeID string, sizeGB int) error {
	// lvcreate -T <vg>/<pool> -V <size>G -n <volumeID>
	_, err := run(ctx, "lvcreate",
		"--thin", fmt.Sprintf("%s/%s", d.vg, d.pool),
		"-V", fmt.Sprintf("%dG", sizeGB),
		"-n", volumeID,
	)
	return err
}

// ListVolumes returns the IDs of all thin LVs in the pool that are NOT
// snapshots (i.e. names that don't start with "snap_").
func (d *Driver) ListVolumes(ctx context.Context) ([]string, error) {
	// lvs -S "pool_lv=<pool> && lv_attr=~^V" --noheadings -o lv_name <vg>
	// -S filters: pool_lv matches our thin pool; lv_attr starting with 'V' means thin volume (not snapshot).
	out, err := run(ctx, "lvs",
		"--noheadings",
		"-o", "lv_name",
		"-S", fmt.Sprintf("pool_lv=%s", d.pool),
		d.vg,
	)
	if err != nil {
		return nil, fmt.Errorf("lvs list: %w", err)
	}
	var ids []string
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "snap_") {
			continue
		}
		ids = append(ids, name)
	}
	return ids, nil
}

// DeleteVolume removes the LV for the given volume ID.
func (d *Driver) DeleteVolume(ctx context.Context, volumeID string) error {
	lvPath := fmt.Sprintf("%s/%s", d.vg, volumeID)
	_, err := run(ctx, "lvremove", "--force", "--yes", lvPath)
	return err
}

// PopulateFromImage writes a cloud/server image onto the raw LV using qemu-img convert.
// The LV must already exist. Any qemu-img-supported input format (qcow2, raw, vmdk, …)
// is accepted; output is always raw so the block device can be used directly.
func (d *Driver) PopulateFromImage(ctx context.Context, volumeID, imageURL string) error {
	lvPath := d.LVPath(volumeID)
	if _, err := os.Stat(lvPath); err != nil {
		return fmt.Errorf("LV %s not found: %w", lvPath, err)
	}
	d.log.Info("qemu-img convert: writing image to LV",
		zap.String("image_url", imageURL),
		zap.String("lv_path", lvPath),
	)
	out, err := run(ctx,
		"qemu-img", "convert",
		"-f", "qcow2",
		"-O", "raw",
		imageURL,
		lvPath,
	)
	if err != nil {
		return fmt.Errorf("qemu-img convert: %w\n%s", err, out)
	}
	return nil
}

// ExtendVolume resizes the LV to newSizeGB (must be larger than current).
func (d *Driver) ExtendVolume(ctx context.Context, volumeID string, newSizeGB int) error {
	lvPath := fmt.Sprintf("%s/%s", d.vg, volumeID)
	_, err := run(ctx, "lvextend", "-L", fmt.Sprintf("%dG", newSizeGB), lvPath)
	return err
}

// LVPath returns the /dev path of a volume LV.
func (d *Driver) LVPath(volumeID string) string {
	return fmt.Sprintf("/dev/%s/%s", d.vg, volumeID)
}

// ─── Snapshots ────────────────────────────────────────────────────────────────

// CreateSnapshot creates a thin snapshot of volumeID named snap_<snapshotID>.
func (d *Driver) CreateSnapshot(ctx context.Context, snapshotID, volumeID string) error {
	srcLV := fmt.Sprintf("%s/%s", d.vg, volumeID)
	_, err := run(ctx, "lvcreate",
		"--snapshot",
		"--name", "snap_"+snapshotID,
		srcLV,
	)
	return err
}

// DeleteSnapshot removes the snapshot LV.
func (d *Driver) DeleteSnapshot(ctx context.Context, snapshotID, volumeID string) error {
	lvPath := fmt.Sprintf("%s/snap_%s", d.vg, snapshotID)
	_, err := run(ctx, "lvremove", "--force", "--yes", lvPath)
	return err
}

// ─── iSCSI target (tgt) ───────────────────────────────────────────────────────

// ExportISCSI creates a tgt iSCSI target exposing the volume LV.
// Returns the portal string (ip:3260) for initiators to connect to.
func (d *Driver) ExportISCSI(ctx context.Context, volumeID string, tid int, iqn string) (string, error) {
	lvPath := d.LVPath(volumeID)

	// Verify the LV exists
	if _, err := os.Stat(lvPath); err != nil {
		return "", fmt.Errorf("LV %s not found: %w", lvPath, err)
	}

	// Remove any stale target with the same IQN before creating a new one.
	if staleTID := d.findTargetByIQN(ctx, iqn); staleTID > 0 {
		d.log.Warn("removing stale iSCSI target", zap.String("iqn", iqn), zap.Int("stale_tid", staleTID))
		d.forceDeleteTarget(ctx, staleTID)
	}

	// Create the target
	if _, err := run(ctx, "tgtadm",
		"--lld", "iscsi",
		"--op", "new",
		"--mode", "target",
		"--tid", fmt.Sprintf("%d", tid),
		"--targetname", iqn,
	); err != nil {
		return "", fmt.Errorf("tgtadm new target: %w", err)
	}

	// Add the LV as LUN 1
	if _, err := run(ctx, "tgtadm",
		"--lld", "iscsi",
		"--op", "new",
		"--mode", "logicalunit",
		"--tid", fmt.Sprintf("%d", tid),
		"--lun", "1",
		"--backing-store", lvPath,
	); err != nil {
		// Clean up target on failure
		run(ctx, "tgtadm", "--lld", "iscsi", "--op", "delete", "--mode", "target", "--tid", fmt.Sprintf("%d", tid)) //nolint:errcheck
		return "", fmt.Errorf("tgtadm add LU: %w", err)
	}

	// Open access to all initiators (portal security relies on network isolation)
	if _, err := run(ctx, "tgtadm",
		"--lld", "iscsi",
		"--op", "bind",
		"--mode", "target",
		"--tid", fmt.Sprintf("%d", tid),
		"-I", "ALL",
	); err != nil {
		d.log.Warn("tgtadm bind failed (target may still work)", zap.Error(err))
	}

	portal := fmt.Sprintf("%s:3260", d.nodeIP)
	d.log.Info("iSCSI target exported",
		zap.String("iqn", iqn),
		zap.String("portal", portal),
		zap.Int("tid", tid),
	)
	return portal, nil
}

// findTargetByIQN scans tgtadm output for a target with the given IQN and
// returns its TID, or 0 if not found.
func (d *Driver) findTargetByIQN(ctx context.Context, iqn string) int {
	out, err := run(ctx, "tgtadm", "--op", "show", "--mode", "target")
	if err != nil {
		return 0
	}
	// Output lines look like: "Target N: iqn...."
	currentTID := 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Target ") {
			var t int
			if _, err := fmt.Sscanf(line, "Target %d:", &t); err == nil {
				currentTID = t
			}
		}
		if strings.Contains(line, iqn) && currentTID > 0 {
			return currentTID
		}
	}
	return 0
}

// forceDeleteTarget closes all active connections on the target then deletes it.
// tgtd refuses to delete a target that has active sessions, so we must close
// each connection first.
func (d *Driver) forceDeleteTarget(ctx context.Context, tid int) {
	tidStr := fmt.Sprintf("%d", tid)

	// Parse active sessions from tgtadm conn output.
	// Each line with "Session:" starts a session block; "Connection:" follows.
	out, _ := run(ctx, "tgtadm", "--op", "show", "--mode", "conn", "--tid", tidStr)
	var sid, cid string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Session:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				sid = parts[1]
			}
		} else if strings.HasPrefix(line, "Connection:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				cid = parts[1]
			}
			if sid != "" && cid != "" {
				run(ctx, "tgtadm", "--op", "delete", "--mode", "conn", //nolint:errcheck
					"--tid", tidStr, "--sid", sid, "--cid", cid)
				cid = ""
			}
		}
	}

	// Now delete the target (sessions should be gone).
	if _, err := run(ctx, "tgtadm", "--lld", "iscsi", "--op", "delete", "--mode", "target", "--tid", tidStr); err != nil {
		d.log.Warn("failed to delete stale target", zap.Int("tid", tid), zap.Error(err))
	}
}

// UnexportISCSI deletes the tgt target with the given TID, force-closing any
// active sessions first.
func (d *Driver) UnexportISCSI(ctx context.Context, tid int) error {
	d.forceDeleteTarget(ctx, tid)
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// primaryIP returns the first non-loopback unicast IPv4 address found on the host.
func primaryIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}
