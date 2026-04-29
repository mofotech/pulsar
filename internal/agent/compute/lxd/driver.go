// Package lxd provides a stub HypervisorDriver implementation for LXD
// (lightweight container-based instances). The methods below follow the
// compute.HypervisorDriver interface contract and will be fleshed out
// in a future phase once the LXD REST API client is integrated.
package lxd

import (
	"context"
	"fmt"

	compute "github.com/agomez/pulsar/internal/agent/compute"
)

// Driver is the LXD hypervisor driver stub.
type Driver struct{}

// New returns a new LXD Driver stub.
func New() *Driver {
	return &Driver{}
}

func (d *Driver) Capabilities() compute.DriverCapabilities {
	return compute.DriverCapabilities{
		HypervisorType:        "lxd",
		SupportsLiveMigration: false,
		MaxInstances:          0, // unlimited
	}
}

func (d *Driver) CreateInstance(_ context.Context, spec compute.InstanceSpec) (compute.InstanceInfo, error) {
	return compute.InstanceInfo{}, fmt.Errorf("lxd: CreateInstance not yet implemented (instance %s)", spec.ID)
}

func (d *Driver) DeleteInstance(_ context.Context, id string) error {
	return fmt.Errorf("lxd: DeleteInstance not yet implemented (instance %s)", id)
}

func (d *Driver) StartInstance(_ context.Context, id string) error {
	return fmt.Errorf("lxd: StartInstance not yet implemented (instance %s)", id)
}

func (d *Driver) StopInstance(_ context.Context, id string, _ bool) error {
	return fmt.Errorf("lxd: StopInstance not yet implemented (instance %s)", id)
}

func (d *Driver) RebootInstance(_ context.Context, id string, _ bool) error {
	return fmt.Errorf("lxd: RebootInstance not yet implemented (instance %s)", id)
}

func (d *Driver) GetConsoleURL(_ context.Context, id string, _ string) (string, error) {
	return "", fmt.Errorf("lxd: GetConsoleURL not yet implemented (instance %s)", id)
}

func (d *Driver) ListInstances(_ context.Context) ([]compute.InstanceInfo, error) {
	return nil, nil // no instances managed yet
}

func (d *Driver) GetInstanceInfo(_ context.Context, id string) (compute.InstanceInfo, error) {
	return compute.InstanceInfo{}, fmt.Errorf("lxd: GetInstanceInfo not yet implemented (instance %s)", id)
}

func (d *Driver) MigrateInstance(_ context.Context, id string, _ string, _ bool) error {
	return fmt.Errorf("lxd: MigrateInstance not supported (instance %s)", id)
}

func (d *Driver) ResizeInstance(_ context.Context, id string, _ int, _ int64) error {
	return fmt.Errorf("lxd: ResizeInstance not yet implemented (instance %s)", id)
}

func (d *Driver) AttachDisk(_ context.Context, instanceID, _, _ string) error {
	return fmt.Errorf("lxd: AttachDisk not yet implemented (instance %s)", instanceID)
}

func (d *Driver) DetachDisk(_ context.Context, instanceID, _ string) error {
	return fmt.Errorf("lxd: DetachDisk not yet implemented (instance %s)", instanceID)
}

func (d *Driver) AllocatedResources() (vcpusUsed int32, ramMBUsed int64) {
	return 0, 0
}
