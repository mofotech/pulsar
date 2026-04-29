package libvirt

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// AttachDisk hot-plugs a raw block device into the running domain and persists
// it to the domain config so it survives a reboot.
//
//   hostDev   — host-side block device, e.g. /dev/sdb (from iSCSI login)
//   targetDev — guest-side device name, e.g. vdb or sdb (no /dev/ prefix)
//
// Disks are attached via the virtio-scsi controller (index 0) that is defined
// in every domain at creation time. The target name is normalised to "sd*" as
// required by the SCSI bus.
func (d *Driver) AttachDisk(ctx context.Context, instanceID, hostDev, targetDev string) error {
	// Strip /dev/ prefix if the caller included it.
	target := strings.TrimPrefix(targetDev, "/dev/")

	// virtio-scsi uses "sd*" target names; remap "vd*" → "sd*".
	if strings.HasPrefix(target, "vd") {
		target = "sd" + target[2:]
	}

	out, err := exec.CommandContext(ctx, "virsh",
		"attach-disk", instanceID,
		hostDev, target,
		"--driver", "qemu",
		"--subdriver", "raw",
		"--targetbus", "scsi",
		"--live", "--config",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("virsh attach-disk %s → %s: %w\n%s",
			hostDev, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DetachDisk removes a scsi block device from the running domain.
// targetDev is the guest-side device name (e.g. sdb, vdb, or /dev/vdb).
func (d *Driver) DetachDisk(ctx context.Context, instanceID, targetDev string) error {
	target := strings.TrimPrefix(targetDev, "/dev/")

	// Normalise vd* → sd* to match what AttachDisk stored.
	if strings.HasPrefix(target, "vd") {
		target = "sd" + target[2:]
	}

	out, err := exec.CommandContext(ctx, "virsh",
		"detach-disk", instanceID,
		target,
		"--live", "--config",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("virsh detach-disk %s: %w\n%s",
			target, err, strings.TrimSpace(string(out)))
	}
	return nil
}
