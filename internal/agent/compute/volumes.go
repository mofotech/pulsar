package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
)

// attachVolume handles a "volume.attach" task:
//  1. iscsiadm discover + login
//  2. Wait for the block device to appear in /dev/disk/by-path
//  3. Hot-plug into the libvirt domain via virsh attach-disk
func (e *Executor) attachVolume(ctx context.Context, payload []byte, result *agentpb.TaskResult) error {
	var task struct {
		VolumeID   string `json:"volume_id"`
		InstanceID string `json:"instance_id"`
		IQN        string `json:"iqn"`
		Portal     string `json:"portal"`
		DevicePath string `json:"device_path"` // desired guest path, e.g. /dev/vdb
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}

	e.log.Info("attaching volume via iSCSI",
		zap.String("volume_id", task.VolumeID),
		zap.String("iqn", task.IQN),
		zap.String("portal", task.Portal),
	)

	// 0. Ensure iscsi_tcp module is loaded.
	if err := ensureISCSIReady(ctx, e.log); err != nil {
		return fmt.Errorf("iSCSI init: %w", err)
	}

	// 1. Create node record directly — we already know the IQN and portal so
	// SendTargets discovery is unnecessary and can fail when tgtd is slow.
	// Ignore "record already exists" errors (exit 15).
	if out, err := iscsiCmd(ctx, "iscsiadm",
		"-m", "node",
		"--op", "new",
		"-T", task.IQN,
		"-p", task.Portal,
	); err != nil && !strings.Contains(out, "already exists") && !strings.Contains(out, "already present") {
		return fmt.Errorf("iscsiadm node new: %w\n%s", err, out)
	}

	// 2. Login.
	// If a stale session already exists, log it out first so we get a fresh
	// block device — reusing a stale session results in ENXIO when QEMU tries
	// to open the device node.
	if out, err := iscsiCmd(ctx, "iscsiadm",
		"-m", "node",
		"-T", task.IQN,
		"-p", task.Portal,
		"--login",
	); err != nil {
		if !strings.Contains(out, "already present") {
			iscsiCmd(ctx, "iscsiadm", "-m", "node", "-T", task.IQN, "-p", task.Portal, "--op", "delete") //nolint:errcheck
			return fmt.Errorf("iscsiadm login: %w\n%s", err, out)
		}
		// Stale session found — force logout and re-login for a fresh device node.
		e.log.Info("stale iSCSI session detected, cycling session", zap.String("iqn", task.IQN))
		iscsiCmd(ctx, "iscsiadm", "-m", "node", "-T", task.IQN, "-p", task.Portal, "--logout")   //nolint:errcheck
		runCmd(ctx, "udevadm", "settle", "--timeout=3")                                            //nolint:errcheck
		if out2, err2 := iscsiCmd(ctx, "iscsiadm",
			"-m", "node",
			"-T", task.IQN,
			"-p", task.Portal,
			"--login",
		); err2 != nil {
			iscsiCmd(ctx, "iscsiadm", "-m", "node", "-T", task.IQN, "-p", task.Portal, "--op", "delete") //nolint:errcheck
			return fmt.Errorf("iscsiadm re-login: %w\n%s", err2, out2)
		}
	}

	// 3. Wait for the block device to appear
	hostDev, err := waitForISCSIDevice(ctx, task.IQN)
	if err != nil {
		// Best-effort logout
		runCmd(ctx, "iscsiadm", "-m", "node", "-T", task.IQN, "-p", task.Portal, "--logout") //nolint:errcheck
		return fmt.Errorf("waiting for iSCSI block device: %w", err)
	}

	e.log.Info("iSCSI device appeared", zap.String("host_dev", hostDev))

	// 4. Hot-attach into the libvirt domain
	driver, err := e.driverForInstance(ctx, task.InstanceID)
	if err != nil {
		return fmt.Errorf("find driver for instance: %w", err)
	}
	targetDev := strings.TrimPrefix(task.DevicePath, "/dev/")
	if err := driver.AttachDisk(ctx, task.InstanceID, hostDev, targetDev); err != nil {
		return fmt.Errorf("attach disk to domain: %w", err)
	}

	res, _ := json.Marshal(map[string]string{"host_dev_path": hostDev})
	result.Result = res
	return nil
}

// detachVolume handles a "volume.detach" task:
//  1. virsh detach-disk from the libvirt domain
//  2. iscsiadm logout + delete record
func (e *Executor) detachVolume(ctx context.Context, payload []byte) error {
	var task struct {
		VolumeID   string `json:"volume_id"`
		InstanceID string `json:"instance_id"`
		DevicePath string `json:"device_path"` // guest device, e.g. /dev/vdb
		HostDev    string `json:"host_dev"`    // host-side block device, e.g. /dev/sdb
		IQN        string `json:"iqn"`
		Portal     string `json:"portal"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}

	e.log.Info("detaching volume",
		zap.String("volume_id", task.VolumeID),
		zap.String("instance_id", task.InstanceID),
	)

	// 1. Detach from libvirt domain (best-effort: instance may already be gone)
	driver, err := e.driverForInstance(ctx, task.InstanceID)
	if err != nil {
		e.log.Warn("instance not found for disk detach — skipping virsh detach (VM already gone)",
			zap.String("instance_id", task.InstanceID))
	} else {
		if err := driver.DetachDisk(ctx, task.InstanceID, task.DevicePath); err != nil {
			e.log.Warn("virsh detach-disk failed — continuing with iSCSI logout",
				zap.String("instance_id", task.InstanceID),
				zap.Error(err))
		}
	}

	// udevadm settle to let the kernel clean up
	runCmd(ctx, "udevadm", "settle") //nolint:errcheck

	// 2. iSCSI logout
	if task.IQN != "" && task.Portal != "" {
		if out, err := iscsiCmd(ctx, "iscsiadm",
			"-m", "node",
			"-T", task.IQN,
			"-p", task.Portal,
			"--logout",
		); err != nil {
			e.log.Warn("iscsiadm logout failed", zap.String("iqn", task.IQN), zap.String("err", string(out)))
		}
		// Delete the node record
		iscsiCmd(ctx, "iscsiadm", "-m", "node", "-T", task.IQN, "-p", task.Portal, "--op", "delete") //nolint:errcheck
	}

	return nil
}

// setVolumeQoS applies I/O throttling to a disk attached to a running domain
// using virsh blkdeviotune. All limit fields are optional; zero means "unlimited".
func (e *Executor) setVolumeQoS(ctx context.Context, payload []byte) error {
	var task struct {
		InstanceID    string `json:"instance_id"`
		Device        string `json:"device"`         // guest device path, e.g. /dev/vdb
		ReadIOPSSec   int64  `json:"read_iops_sec"`
		WriteIOPSSec  int64  `json:"write_iops_sec"`
		TotalIOPSSec  int64  `json:"total_iops_sec"`
		ReadBytesSec  int64  `json:"read_bytes_sec"`
		WriteBytesSec int64  `json:"write_bytes_sec"`
		TotalBytesSec int64  `json:"total_bytes_sec"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	if task.InstanceID == "" || task.Device == "" {
		return fmt.Errorf("instance_id and device are required")
	}

	// virsh blkdeviotune uses the guest disk target (e.g. "vdb"), not the path.
	diskTarget := strings.TrimPrefix(task.Device, "/dev/")

	args := []string{"blkdeviotune", task.InstanceID, diskTarget}

	// Only pass limits that are explicitly set (non-zero).
	if task.ReadIOPSSec > 0 {
		args = append(args, "--read-iops-sec", fmt.Sprintf("%d", task.ReadIOPSSec))
	}
	if task.WriteIOPSSec > 0 {
		args = append(args, "--write-iops-sec", fmt.Sprintf("%d", task.WriteIOPSSec))
	}
	if task.TotalIOPSSec > 0 {
		args = append(args, "--total-iops-sec", fmt.Sprintf("%d", task.TotalIOPSSec))
	}
	if task.ReadBytesSec > 0 {
		args = append(args, "--read-bytes-sec", fmt.Sprintf("%d", task.ReadBytesSec))
	}
	if task.WriteBytesSec > 0 {
		args = append(args, "--write-bytes-sec", fmt.Sprintf("%d", task.WriteBytesSec))
	}
	if task.TotalBytesSec > 0 {
		args = append(args, "--total-bytes-sec", fmt.Sprintf("%d", task.TotalBytesSec))
	}

	// If no limits specified, nothing to apply.
	if len(args) == 3 {
		e.log.Info("setVolumeQoS: no limits specified, skipping")
		return nil
	}

	e.log.Info("applying volume QoS",
		zap.String("instance_id", task.InstanceID),
		zap.String("disk", diskTarget),
	)
	out, err := runCmd(ctx, "virsh", args...)
	if err != nil {
		return fmt.Errorf("virsh blkdeviotune: %w\n%s", err, out)
	}
	return nil
}

// driverForInstance finds which registered driver owns the given instance.
func (e *Executor) driverForInstance(ctx context.Context, instanceID string) (HypervisorDriver, error) {
	for _, d := range e.drivers {
		if info, err := d.GetInstanceInfo(ctx, instanceID); err == nil && info.ID != "" {
			return d, nil
		}
	}
	return nil, fmt.Errorf("instance %s not found on any registered driver", instanceID)
}

// waitForISCSIDevice scans /dev/disk/by-path for a symlink that contains the
// IQN, resolves it to the real /dev/sdX path, and returns it. Retries for up
// to 30 seconds (kernel + udev settle time).
func waitForISCSIDevice(ctx context.Context, iqn string) (string, error) {
	// Linux by-path names preserve colons in the IQN, e.g.:
	// ip-10.11.3.190:3260-iscsi-iqn.2026-04.io.pulsar:volume.xxx-lun-1
	iqnPart := iqn

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir("/dev/disk/by-path")
		if err == nil {
			for _, e := range entries {
				if strings.Contains(e.Name(), iqnPart) {
					linkPath := filepath.Join("/dev/disk/by-path", e.Name())
					target, err := filepath.EvalSymlinks(linkPath)
					if err == nil {
						// Prefer the -part1 LUN path if present, else the bare disk
						if !strings.Contains(e.Name(), "-part") {
							return target, nil
						}
					}
				}
			}
		}

		// Also try udevadm settle then re-check
		runCmd(ctx, "udevadm", "settle", "--timeout=2") //nolint:errcheck

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return "", fmt.Errorf("iSCSI device for %s did not appear within 30s", iqn)
}

// hostNetNS is the path where the host network namespace file is mounted.
// The compute container mounts /proc/1/ns from the host at /host-proc-ns.
const hostNetNS = "/host-proc-ns/net"

// iscsiCmd runs an iscsiadm command inside the host network namespace so it
// can reach the host iscsid's abstract Unix socket (@ISCSIADM_ABSTRACT_NAMESPACE).
func iscsiCmd(ctx context.Context, args ...string) (string, error) {
	if _, err := os.Stat(hostNetNS); err == nil {
		// nsenter --net=<host-ns> -- iscsiadm <args...>
		nsArgs := append([]string{"--net=" + hostNetNS, "--"}, args...)
		return runCmd(ctx, "nsenter", nsArgs...)
	}
	// Fallback: run directly (e.g., host networking or already in host NS)
	return runCmd(ctx, args[0], args[1:]...)
}

// ensureISCSIReady loads the iscsi_tcp kernel module.
// iscsid runs on the host; iscsiadm reaches it via nsenter into the host network namespace.
func ensureISCSIReady(ctx context.Context, log *zap.Logger) error {
	if out, err := runCmd(ctx, "modprobe", "iscsi_tcp"); err != nil {
		log.Warn("modprobe iscsi_tcp failed (may already be loaded)", zap.String("out", out))
	}
	if _, err := os.Stat(hostNetNS); err != nil {
		return fmt.Errorf("host network namespace not mounted at %s: %w", hostNetNS, err)
	}
	return nil
}

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
