package compute

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"go.uber.org/zap"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	computepb "github.com/agomez/pulsar/gen/proto/compute"
)

// Executor manages compute drivers and dispatches task assignments.
type Executor struct {
	drivers       map[string]HypervisorDriver // keyed by hypervisor type: "kvm", "lxd", etc.
	imageCacheDir string
	log           *zap.Logger
}

func NewExecutor(imageCacheDir string, log *zap.Logger) *Executor {
	return &Executor{
		drivers:       make(map[string]HypervisorDriver),
		imageCacheDir: imageCacheDir,
		log:           log,
	}
}

// ResourceUsage returns current host totals and tracked allocation usage.
func (e *Executor) ResourceUsage() agentpb.ResourceUsage {
	vcpusTotal := int32(runtime.NumCPU())
	var ramMBTotal, ramMBUsed int64

	if f, err := os.Open("/proc/meminfo"); err == nil {
		scanner := bufio.NewScanner(f)
		var memTotalKB, memAvailKB int64
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}
			val, _ := strconv.ParseInt(fields[1], 10, 64)
			switch fields[0] {
			case "MemTotal:":
				memTotalKB = val
			case "MemAvailable:":
				memAvailKB = val
			}
		}
		f.Close()
		ramMBTotal = memTotalKB / 1024
		ramMBUsed = (memTotalKB - memAvailKB) / 1024
	}

	var vcpusUsed int32
	for _, d := range e.drivers {
		v, _ := d.AllocatedResources()
		vcpusUsed += v
	}

	return agentpb.ResourceUsage{
		VcpusTotal: vcpusTotal,
		VcpusUsed:  vcpusUsed,
		RamMbTotal: ramMBTotal,
		RamMbUsed:  ramMBUsed,
	}
}

// RegisterDriver adds a hypervisor driver for the given type name.
func (e *Executor) RegisterDriver(name string, d HypervisorDriver) {
	e.drivers[name] = d
}

// AdvertisedCapabilities returns the capabilities of all registered drivers.
func (e *Executor) AdvertisedCapabilities() agentpb.AgentCapabilities {
	var types []string
	for t := range e.drivers {
		types = append(types, t)
	}
	return agentpb.AgentCapabilities{
		HypervisorTypes: types,
	}
}

// Execute handles a task assignment and returns a TaskResult.
func (e *Executor) Execute(ctx context.Context, task *agentpb.TaskAssignment) *agentpb.TaskResult {
	result := &agentpb.TaskResult{TaskId: task.TaskId}

	var err error
	switch task.TaskType {
	case "instance.create":
		err = e.createInstance(ctx, task.Payload, result)
	case "instance.delete":
		err = e.deleteInstance(ctx, task.Payload)
	case "instance.start":
		err = e.startInstance(ctx, task.Payload)
	case "instance.stop":
		err = e.stopInstance(ctx, task.Payload)
	case "instance.reboot":
		err = e.rebootInstance(ctx, task.Payload)
	case "instance.console":
		result.Result, err = e.consoleInstance(ctx, task.Payload)
	case "volume.attach":
		err = e.attachVolume(ctx, task.Payload, result)
	case "volume.detach":
		err = e.detachVolume(ctx, task.Payload)
	case "volume.qos.set":
		err = e.setVolumeQoS(ctx, task.Payload)
	case "instance.list":
		result.Result, err = e.listInstances(ctx)
	case "instance.migrate":
		err = e.migrateInstance(ctx, task.Payload)
	case "instance.resize":
		err = e.resizeInstance(ctx, task.Payload)
	default:
		err = fmt.Errorf("unknown task type: %s", task.TaskType)
	}

	if err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
		e.log.Error("task failed",
			zap.String("task_id", task.TaskId),
			zap.String("task_type", task.TaskType),
			zap.Error(err),
		)
	} else {
		result.Success = true
	}
	return result
}

// loginBootVolumeISCSI performs the iSCSI initiator login for a boot-from-volume
// disk and returns the host-side block device path (e.g. /dev/sdb).
// This mirrors the login steps in attachVolume but skips the virsh hot-plug since
// the device is used as the initial root disk in the domain XML instead.
func (e *Executor) loginBootVolumeISCSI(ctx context.Context, iqn, portal string) (string, error) {
	if err := ensureISCSIReady(ctx, e.log); err != nil {
		return "", fmt.Errorf("iSCSI init: %w", err)
	}

	// Create node record (ignore "already exists" errors).
	if out, err := iscsiCmd(ctx, "iscsiadm",
		"-m", "node",
		"--op", "new",
		"-T", iqn,
		"-p", portal,
	); err != nil && !strings.Contains(out, "already exists") && !strings.Contains(out, "already present") {
		return "", fmt.Errorf("iscsiadm node new: %w\n%s", err, out)
	}

	// Login (cycle stale session if needed).
	if out, err := iscsiCmd(ctx, "iscsiadm",
		"-m", "node",
		"-T", iqn,
		"-p", portal,
		"--login",
	); err != nil {
		if !strings.Contains(out, "already present") {
			iscsiCmd(ctx, "iscsiadm", "-m", "node", "-T", iqn, "-p", portal, "--op", "delete") //nolint:errcheck
			return "", fmt.Errorf("iscsiadm login: %w\n%s", err, out)
		}
		e.log.Info("stale iSCSI session detected, cycling", zap.String("iqn", iqn))
		iscsiCmd(ctx, "iscsiadm", "-m", "node", "-T", iqn, "-p", portal, "--logout") //nolint:errcheck
		runCmd(ctx, "udevadm", "settle", "--timeout=3")                              //nolint:errcheck
		if out2, err2 := iscsiCmd(ctx, "iscsiadm",
			"-m", "node",
			"-T", iqn,
			"-p", portal,
			"--login",
		); err2 != nil {
			iscsiCmd(ctx, "iscsiadm", "-m", "node", "-T", iqn, "-p", portal, "--op", "delete") //nolint:errcheck
			return "", fmt.Errorf("iscsiadm re-login: %w\n%s", err2, out2)
		}
	}

	hostDev, err := waitForISCSIDevice(ctx, iqn)
	if err != nil {
		runCmd(ctx, "iscsiadm", "-m", "node", "-T", iqn, "-p", portal, "--logout") //nolint:errcheck
		return "", fmt.Errorf("waiting for iSCSI block device: %w", err)
	}
	return hostDev, nil
}

func (e *Executor) driverFor(hypervisorType string) (HypervisorDriver, error) {
	d, ok := e.drivers[hypervisorType]
	if !ok {
		return nil, fmt.Errorf("no driver registered for hypervisor type %q", hypervisorType)
	}
	return d, nil
}

func (e *Executor) createInstance(ctx context.Context, payload []byte, result *agentpb.TaskResult) error {
	var task computepb.CreateInstanceTask
	if err := json.Unmarshal(payload, &task); err != nil {
		return fmt.Errorf("unmarshal create task: %w", err)
	}

	driver, err := e.driverFor(task.HypervisorType)
	if err != nil {
		return err
	}

	var ports []PortSpec
	for _, p := range task.NetworkPorts {
		ports = append(ports, PortSpec{
			PortID:    p.PortId,
			MACAddr:   p.MacAddr,
			Bridge:    p.Bridge,
			OVSBridge: p.GetOvsBridge(),
		})
	}

	spec := InstanceSpec{
		ID:            task.Id,
		ProjectID:     task.Metadata["pulsar:project_id"],
		Name:          task.Name,
		FlavorID:      task.Metadata["pulsar:flavor_id"],
		ImageID:       task.Metadata["pulsar:image_id"],
		VCPUs:         int(task.Vcpus),
		RamMB:         task.RamMb,
		DiskGB:        task.DiskGb,
		NetworkPorts:  ports,
		UserData:      task.UserData,
		Metadata:      task.Metadata,
		SSHPublicKeys: task.SshPublicKeys,
	}

	bootIQN := task.Metadata["pulsar:boot_iscsi_iqn"]
	bootPortal := task.Metadata["pulsar:boot_iscsi_portal"]

	if bootIQN != "" && bootPortal != "" {
		// Boot-from-volume: log in to iSCSI and use the block device as root disk.
		e.log.Info("boot-from-volume: logging in to iSCSI",
			zap.String("iqn", bootIQN),
			zap.String("portal", bootPortal),
		)
		hostDev, err := e.loginBootVolumeISCSI(ctx, bootIQN, bootPortal)
		if err != nil {
			return fmt.Errorf("boot volume iSCSI login: %w", err)
		}
		spec.BootVolumeDev = hostDev
	} else {
		// Normal image-based boot: download/resolve image.
		e.log.Info("resolving image", zap.String("image_url", task.ImageUrl))
		localImagePath, err := resolveImage(ctx, e.imageCacheDir, task.ImageUrl)
		if err != nil {
			return fmt.Errorf("resolve image: %w", err)
		}
		e.log.Info("image ready", zap.String("path", localImagePath))
		spec.ImagePath = localImagePath
	}

	info, err := driver.CreateInstance(ctx, spec)
	if err != nil {
		return err
	}

	res, _ := json.Marshal(computepb.CreateInstanceResult{VncUrl: info.VNCURL})
	result.Result = res
	return nil
}

func (e *Executor) deleteInstance(ctx context.Context, payload []byte) error {
	var task computepb.DeleteInstanceTask
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	for _, d := range e.drivers {
		if err := d.DeleteInstance(ctx, task.Id); err == nil {
			return nil
		}
	}
	return nil // idempotent if already gone
}

func (e *Executor) startInstance(ctx context.Context, payload []byte) error {
	var task computepb.StartInstanceTask
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	for _, d := range e.drivers {
		if err := d.StartInstance(ctx, task.Id); err == nil {
			return nil
		}
	}
	return fmt.Errorf("instance %s not found on any driver", task.Id)
}

func (e *Executor) stopInstance(ctx context.Context, payload []byte) error {
	var task computepb.StopInstanceTask
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	for _, d := range e.drivers {
		if err := d.StopInstance(ctx, task.Id, task.Force); err == nil {
			return nil
		}
	}
	return fmt.Errorf("instance %s not found on any driver", task.Id)
}

func (e *Executor) consoleInstance(ctx context.Context, payload []byte) ([]byte, error) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	for _, d := range e.drivers {
		addr, err := d.GetConsoleURL(ctx, req.ID, "vnc")
		if err != nil {
			continue
		}
		return json.Marshal(map[string]string{"vnc_addr": addr})
	}
	return nil, fmt.Errorf("instance %s not found on any driver", req.ID)
}

func (e *Executor) rebootInstance(ctx context.Context, payload []byte) error {
	var task computepb.RebootInstanceTask
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	for _, d := range e.drivers {
		if err := d.RebootInstance(ctx, task.Id, task.Hard); err == nil {
			return nil
		}
	}
	return fmt.Errorf("instance %s not found on any driver", task.Id)
}

func (e *Executor) migrateInstance(ctx context.Context, payload []byte) error {
	var task computepb.MigrateInstanceTask
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	for _, d := range e.drivers {
		infos, err := d.ListInstances(ctx)
		if err != nil {
			continue
		}
		for _, info := range infos {
			if info.ID == task.Id {
				return d.MigrateInstance(ctx, task.Id, task.TargetHost, task.Live)
			}
		}
	}
	return fmt.Errorf("instance %s not found on any driver", task.Id)
}

func (e *Executor) resizeInstance(ctx context.Context, payload []byte) error {
	var task struct {
		ID    string `json:"id"`
		VCPUs int    `json:"vcpus"`
		RamMB int64  `json:"ram_mb"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return fmt.Errorf("unmarshal resize task: %w", err)
	}
	if task.ID == "" {
		return fmt.Errorf("resize: id is required")
	}
	for _, d := range e.drivers {
		infos, err := d.ListInstances(ctx)
		if err != nil {
			continue
		}
		for _, info := range infos {
			if info.ID == task.ID {
				return d.ResizeInstance(ctx, task.ID, task.VCPUs, task.RamMB)
			}
		}
	}
	return fmt.Errorf("instance %s not found on any driver", task.ID)
}

// listInstances returns a JSON inventory of all instances across all registered drivers.
// Response: {"instances": [{"id": "<id>", "name": "<name>"}, ...]}
func (e *Executor) listInstances(ctx context.Context) ([]byte, error) {
	type instanceEntry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type response struct {
		Instances []instanceEntry `json:"instances"`
	}

	var instances []instanceEntry
	for _, d := range e.drivers {
		infos, err := d.ListInstances(ctx)
		if err != nil {
			e.log.Warn("driver ListInstances failed", zap.Error(err))
			continue
		}
		for _, info := range infos {
			instances = append(instances, instanceEntry{ID: info.ID, Name: info.Name})
		}
	}
	if instances == nil {
		instances = []instanceEntry{}
	}
	return json.Marshal(response{Instances: instances})
}
