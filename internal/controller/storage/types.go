package storage

import "time"

// QoSSpec defines I/O throttling limits for a volume.
// Zero values mean "unlimited". These map 1:1 to virsh blkdeviotune parameters.
type QoSSpec struct {
	ReadIOPSSec   int64 `json:"read_iops_sec,omitempty"`
	WriteIOPSSec  int64 `json:"write_iops_sec,omitempty"`
	TotalIOPSSec  int64 `json:"total_iops_sec,omitempty"`
	ReadBytesSec  int64 `json:"read_bytes_sec,omitempty"`
	WriteBytesSec int64 `json:"write_bytes_sec,omitempty"`
	TotalBytesSec int64 `json:"total_bytes_sec,omitempty"`
}

// ─── Volume ──────────────────────────────────────────────────────────────────

// VolumeStatus values
const (
	VolumeStatusCreating  = "creating"
	VolumeStatusAvailable = "available"
	VolumeStatusInUse     = "in-use"
	VolumeStatusExtending = "extending"
	VolumeStatusDeleting  = "deleting"
	VolumeStatusError     = "error"
)

type Volume struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	SizeGB       int       `json:"size_gb"`
	VolumeTypeID string    `json:"volume_type_id,omitempty"`
	Status       string    `json:"status"`
	AgentID      string    `json:"agent_id,omitempty"`      // storage agent that holds the LV
	Bootable     bool      `json:"bootable,omitempty"`      // populated from an image; can be used as boot disk
	ImageID      string    `json:"image_id,omitempty"`      // source image (when Bootable=true)
	AttachedTo   string    `json:"attached_to,omitempty"`   // instance ID
	AttachedHost string    `json:"attached_host,omitempty"` // compute node_id
	DevicePath   string    `json:"device_path,omitempty"`   // guest-side, e.g. /dev/vdb
	HostDevPath  string    `json:"host_dev_path,omitempty"` // host-side iSCSI block dev
	ISCSIIQN     string    `json:"iscsi_iqn,omitempty"`
	ISCSIPortal  string    `json:"iscsi_portal,omitempty"` // host:3260
	ISCSITID     int       `json:"iscsi_tid,omitempty"`
	SnapshotID   string    `json:"snapshot_id,omitempty"` // source snapshot
	QoS          *QoSSpec  `json:"qos,omitempty"`         // I/O throttling limits
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CreateVolumeRequest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	SizeGB       int      `json:"size_gb"`
	VolumeTypeID string   `json:"volume_type_id,omitempty"`
	SnapshotID   string   `json:"snapshot_id,omitempty"`
	ImageID      string   `json:"image_id,omitempty"` // populate from image; makes volume bootable
	QoS          *QoSSpec `json:"qos,omitempty"`      // I/O throttling limits
}

type VolumeActionRequest struct {
	Action     string   `json:"action"` // "attach" | "detach" | "extend" | "set-qos"
	InstanceID string   `json:"instance_id,omitempty"`
	DevicePath string   `json:"device_path,omitempty"` // guest target, e.g. /dev/vdb
	NewSizeGB  int      `json:"new_size_gb,omitempty"`
	QoS        *QoSSpec `json:"qos,omitempty"` // used with action="set-qos"
}

// ─── Snapshot ────────────────────────────────────────────────────────────────

const (
	SnapshotStatusCreating  = "creating"
	SnapshotStatusAvailable = "available"
	SnapshotStatusDeleting  = "deleting"
	SnapshotStatusError     = "error"
)

type Snapshot struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	VolumeID    string    `json:"volume_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	SizeGB      int       `json:"size_gb"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateSnapshotRequest struct {
	VolumeID    string `json:"volume_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ─── VolumeType ───────────────────────────────────────────────────────────────

type VolumeType struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Driver     string            `json:"driver"` // "lvm" | "ceph" | "nfs"
	ExtraSpecs map[string]string `json:"extra_specs,omitempty"`
	QoS        *QoSSpec          `json:"qos,omitempty"` // default QoS for volumes of this type
	CreatedAt  time.Time         `json:"created_at"`
}

type CreateVolumeTypeRequest struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	ExtraSpecs map[string]string `json:"extra_specs,omitempty"`
}
