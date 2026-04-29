package compute

import "context"

// HypervisorDriver is the interface every hypervisor backend must implement.
// The executor selects the appropriate driver based on the task's hypervisor_type field.
type HypervisorDriver interface {
	// Lifecycle
	CreateInstance(ctx context.Context, spec InstanceSpec) (InstanceInfo, error)
	DeleteInstance(ctx context.Context, id string) error
	StartInstance(ctx context.Context, id string) error
	StopInstance(ctx context.Context, id string, force bool) error
	RebootInstance(ctx context.Context, id string, hard bool) error

	// Console
	GetConsoleURL(ctx context.Context, id string, consoleType string) (string, error)

	// Inspection
	ListInstances(ctx context.Context) ([]InstanceInfo, error)
	GetInstanceInfo(ctx context.Context, id string) (InstanceInfo, error)

	// Migration
	MigrateInstance(ctx context.Context, id string, targetHost string, live bool) error

	// Resize applies CPU and/or RAM changes to a domain.
	// vcpus == 0 means "keep current"; ramMB == 0 means "keep current".
	// Hot-add is attempted first; the implementation falls back to offline
	// modification when live changes are unsupported.
	ResizeInstance(ctx context.Context, id string, vcpus int, ramMB int64) error

	// Block storage
	// AttachDisk hot-plugs a block device into a running domain.
	// hostDev is the host-side block device (e.g. /dev/sdb from iSCSI login).
	// targetDev is the guest-side device name (e.g. vdb).
	AttachDisk(ctx context.Context, instanceID, hostDev, targetDev string) error
	// DetachDisk removes a block device from a running domain.
	DetachDisk(ctx context.Context, instanceID, targetDev string) error

	// Introspection
	Capabilities() DriverCapabilities
	// AllocatedResources returns the sum of vCPUs and RAM (in MB) across all
	// domains currently known to this driver, regardless of running state.
	AllocatedResources() (vcpusUsed int32, ramMBUsed int64)
}

// InstanceSpec describes what the hypervisor should create.
type InstanceSpec struct {
	ID            string
	ProjectID     string
	Name          string
	FlavorID      string
	ImageID       string
	VCPUs         int
	RamMB         int64
	DiskGB        int64
	ImagePath     string // Local path after image download; empty when BootVolumeDev is set
	BootVolumeDev string // Host-side block device for boot-from-volume (e.g. /dev/sdb); mutually exclusive with ImagePath
	NetworkPorts  []PortSpec
	UserData      string   // cloud-init userdata (raw, already decoded)
	SSHPublicKeys []string // OpenSSH authorized_keys lines to inject via cloud-init
	Metadata      map[string]string
}

// PortSpec describes a network interface to attach.
type PortSpec struct {
	PortID    string
	MACAddr   string
	Bridge    string // Linux bridge on this compute node
	OVSBridge string // OVS integration bridge ("br-int") when using OVN
}

// InstanceInfo is the live state returned by the hypervisor.
type InstanceInfo struct {
	ID     string
	Name   string
	State  string // "running" | "stopped" | "paused" | "error"
	VNCURL string
}

// DriverCapabilities advertises what this driver supports.
type DriverCapabilities struct {
	HypervisorType        string
	SupportsLiveMigration bool
	MaxInstances          int
}
