package compute

import "time"

type HypervisorType string

const (
	HypervisorKVM        HypervisorType = "kvm"
	HypervisorLXD        HypervisorType = "lxd"
	HypervisorLXC        HypervisorType = "lxc"
	HypervisorContainerd HypervisorType = "containerd"
)

type InstanceState string

const (
	StatePending    InstanceState = "pending"
	StateScheduling InstanceState = "scheduling"
	StateScheduled  InstanceState = "scheduled"
	StateBuilding   InstanceState = "building"
	StateActive     InstanceState = "active"
	StateStopping   InstanceState = "stopping"
	StateStopped    InstanceState = "stopped"
	StateStarting   InstanceState = "starting"
	StateDeleting   InstanceState = "deleting"
	StateDeleted    InstanceState = "deleted"
	StateError      InstanceState = "error"
	StateUnknown    InstanceState = "unknown" // node unreachable; VM may still be running
)

// InstanceAddress holds the IP assigned to an instance port.
type InstanceAddress struct {
	NetworkID string `json:"network_id"`
	IPAddress string `json:"ip_address"`
	MACAddr   string `json:"mac_address,omitempty"`
}

type Instance struct {
	ID               string            `json:"id"`
	ProjectID        string            `json:"project_id"`
	Name             string            `json:"name"`
	FlavorID         string            `json:"flavor_id"`
	ImageID          string            `json:"image_id"`
	HypervisorType   HypervisorType    `json:"hypervisor_type"`
	Status           InstanceState     `json:"status"`
	NodeID           string            `json:"node_id,omitempty"`
	UserData         string            `json:"user_data,omitempty"`
	KeyNames         []string          `json:"key_names,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	PortIDs          []string          `json:"port_ids,omitempty"`
	Addresses        []InstanceAddress `json:"addresses,omitempty"`
	BootVolumeID     string            `json:"boot_volume_id,omitempty"`     // set when booting from volume
	DeleteBootVolume bool              `json:"delete_boot_volume,omitempty"` // delete volume on instance delete
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// BlockDeviceMapping describes a volume to use as the boot disk.
type BlockDeviceMapping struct {
	VolumeID          string `json:"volume_id"`
	DeleteOnTerminate bool   `json:"delete_on_terminate,omitempty"`
}

type Flavor struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	VCPUs       int    `json:"vcpus"`
	RamMB       int    `json:"ram_mb"`
	DiskGB      int    `json:"disk_gb"`
	EphemeralGB int    `json:"ephemeral_gb"`
	Public      bool   `json:"public"`
}

// CreateInstanceRequest is the deserialized request body for POST /v1/compute/instances.
type CreateInstanceRequest struct {
	Name                string               `json:"name"`
	FlavorID            string               `json:"flavor_id"`
	ImageID             string               `json:"image_id,omitempty"`        // mutually exclusive with BlockDeviceMappings
	HypervisorType      HypervisorType       `json:"hypervisor_type,omitempty"` // optional; scheduler picks if empty
	Networks            []NetworkRequest     `json:"networks"`
	UserData            string               `json:"user_data,omitempty"`
	KeyNames            []string             `json:"key_names,omitempty"` // SSH keypair names to inject
	Metadata            map[string]string    `json:"metadata,omitempty"`
	BlockDeviceMappings []BlockDeviceMapping `json:"block_device_mappings,omitempty"` // first entry is boot disk
}

type NetworkRequest struct {
	NetworkID string `json:"network_id"`
	PortID    string `json:"port_id,omitempty"`
}

type InstanceActionRequest struct {
	Action string `json:"action"` // "start" | "stop" | "reboot" | "hard-reboot" | "console"
}

// ResizeInstanceRequest requests a flavor change for an existing instance.
// The resize is applied live where the hypervisor supports CPU/RAM hot-add,
// otherwise the instance must be stopped first.
type ResizeInstanceRequest struct {
	FlavorID string `json:"flavor_id"`
}
