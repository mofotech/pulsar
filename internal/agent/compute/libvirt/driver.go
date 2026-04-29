package libvirt

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	golibvirt "github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/socket/dialers"

	compute "github.com/agomez/pulsar/internal/agent/compute"
	"github.com/agomez/pulsar/internal/agent/compute/cloudinit"
)

// Driver implements compute.HypervisorDriver for KVM/QEMU via libvirt.
type Driver struct {
	lv          *golibvirt.Libvirt
	instanceDir string
	domainType  string // "kvm" or "qemu"
	emulator    string // path to QEMU binary
}

func New(socketPath, instanceDir, domainType, emulator string) (*Driver, error) {
	if socketPath == "" {
		socketPath = "/var/run/libvirt/libvirt-sock"
	}
	if domainType == "" {
		domainType = "kvm"
	}
	if emulator == "" {
		emulator = "/usr/bin/kvm"
	}
	lv := golibvirt.NewWithDialer(dialers.NewLocal(
		dialers.WithSocket(socketPath),
		dialers.WithLocalTimeout(2*time.Second),
	))
	if err := lv.Connect(); err != nil {
		return nil, fmt.Errorf("libvirt connect: %w", err)
	}
	if err := os.MkdirAll(instanceDir, 0o755); err != nil {
		return nil, fmt.Errorf("create instance dir: %w", err)
	}
	return &Driver{lv: lv, instanceDir: instanceDir, domainType: domainType, emulator: emulator}, nil
}

// prepareInstanceDisk creates a per-instance qcow2 overlay backed by baseImage
// and resizes it to diskGB. Returns the path to the instance disk.
func (d *Driver) prepareInstanceDisk(instanceID, baseImage string, diskGB int64) (string, error) {
	dest := filepath.Join(d.instanceDir, instanceID+".qcow2")

	// qemu-img create -f qcow2 -b <base> -F qcow2 <dest> <size>G
	// The overlay shares the base image's blocks via copy-on-write; only
	// writes from this instance are stored in <dest>.
	out, err := exec.Command("qemu-img", "create",
		"-f", "qcow2",
		"-b", baseImage,
		"-F", "qcow2",
		dest,
		fmt.Sprintf("%dG", diskGB),
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("qemu-img create: %w\n%s", err, out)
	}

	return dest, nil
}

func (d *Driver) Capabilities() compute.DriverCapabilities {
	return compute.DriverCapabilities{
		HypervisorType:        "kvm",
		SupportsLiveMigration: true,
	}
}

func (d *Driver) CreateInstance(ctx context.Context, spec compute.InstanceSpec) (compute.InstanceInfo, error) {
	if spec.BootVolumeDev == "" {
		// Normal image-based boot: create a per-instance qcow2 overlay.
		instanceDisk, err := d.prepareInstanceDisk(spec.ID, spec.ImagePath, spec.DiskGB)
		if err != nil {
			return compute.InstanceInfo{}, fmt.Errorf("prepare instance disk: %w", err)
		}
		// Point the domain at the per-instance overlay, not the shared cache image.
		spec.ImagePath = instanceDisk
	}
	// If BootVolumeDev is set we skip overlay creation — the raw block device is the root disk.

	// Generate cloud-init seed ISO.
	seedISO := filepath.Join(d.instanceDir, spec.ID+"-seed.iso")
	var ports []cloudinit.Port
	for _, p := range spec.NetworkPorts {
		ports = append(ports, cloudinit.Port{MACAddr: p.MACAddr})
	}
	// cleanup removes the instance disk overlay (if any) and the cloud-init ISO on error.
	// For boot-from-volume instances spec.ImagePath is empty, so os.Remove("") is a no-op.
	cleanup := func() {
		if spec.ImagePath != "" {
			os.Remove(spec.ImagePath) //nolint:errcheck
		}
		os.Remove(seedISO) //nolint:errcheck
	}

	if err := cloudinit.WriteISO(cloudinit.Config{
		InstanceID:    spec.ID,
		Hostname:      spec.Name,
		UserData:      spec.UserData,
		SSHPublicKeys: spec.SSHPublicKeys,
		Ports:         ports,
	}, seedISO); err != nil {
		cleanup()
		return compute.InstanceInfo{}, fmt.Errorf("write cloud-init iso: %w", err)
	}

	xmlDesc, err := buildDomainXML(spec, seedISO, d.domainType, d.emulator)
	if err != nil {
		cleanup()
		return compute.InstanceInfo{}, fmt.Errorf("build domain xml: %w", err)
	}

	// Undefine any stale domain with this ID before redefining.
	if existing, lookupErr := d.lv.DomainLookupByName(spec.ID); lookupErr == nil {
		_ = d.lv.DomainDestroy(existing)
		_ = d.lv.DomainUndefine(existing)
	}

	dom, err := d.lv.DomainDefineXML(xmlDesc)
	if err != nil {
		cleanup()
		return compute.InstanceInfo{}, fmt.Errorf("define domain: %w", err)
	}

	if err := d.lv.DomainCreate(dom); err != nil {
		cleanup()
		return compute.InstanceInfo{}, fmt.Errorf("start domain: %w", err)
	}

	return compute.InstanceInfo{
		ID:    spec.ID,
		Name:  spec.Name,
		State: "running",
	}, nil
}

func (d *Driver) DeleteInstance(ctx context.Context, id string) error {
	dom, err := d.lv.DomainLookupByName(id)
	if err == nil {
		// Destroy (force off) then undefine
		_ = d.lv.DomainDestroy(dom)
		_ = d.lv.DomainUndefine(dom)
	}
	// Remove the per-instance overlay disk if it exists.
	// Boot-from-volume instances have no qcow2 overlay (their root disk is a raw block device
	// managed by the storage service), so we silently skip missing files.
	instanceDisk := filepath.Join(d.instanceDir, id+".qcow2")
	if err := os.Remove(instanceDisk); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove instance disk: %w", err)
	}
	// Remove cloud-init seed ISO.
	_ = os.Remove(filepath.Join(d.instanceDir, id+"-seed.iso"))
	return nil
}

func (d *Driver) StartInstance(ctx context.Context, id string) error {
	dom, err := d.lv.DomainLookupByName(id)
	if err != nil {
		return fmt.Errorf("lookup domain %s: %w", id, err)
	}
	return d.lv.DomainCreate(dom)
}

func (d *Driver) StopInstance(ctx context.Context, id string, force bool) error {
	dom, err := d.lv.DomainLookupByName(id)
	if err != nil {
		return fmt.Errorf("lookup domain %s: %w", id, err)
	}
	if force {
		return d.lv.DomainDestroy(dom)
	}
	return d.lv.DomainShutdown(dom)
}

func (d *Driver) RebootInstance(ctx context.Context, id string, hard bool) error {
	dom, err := d.lv.DomainLookupByName(id)
	if err != nil {
		return fmt.Errorf("lookup domain %s: %w", id, err)
	}
	if hard {
		if err := d.lv.DomainDestroy(dom); err != nil {
			return err
		}
		return d.lv.DomainCreate(dom)
	}
	return d.lv.DomainReboot(dom, 0)
}

func (d *Driver) GetConsoleURL(ctx context.Context, id string, consoleType string) (string, error) {
	dom, err := d.lv.DomainLookupByName(id)
	if err != nil {
		return "", fmt.Errorf("lookup domain %s: %w", id, err)
	}
	xmlDesc, err := d.lv.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return "", fmt.Errorf("get domain xml: %w", err)
	}
	var domXML struct {
		Devices struct {
			Graphics []struct {
				Type string `xml:"type,attr"`
				Port int    `xml:"port,attr"`
			} `xml:"graphics"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(xmlDesc), &domXML); err != nil {
		return "", fmt.Errorf("parse domain xml: %w", err)
	}
	for _, g := range domXML.Devices.Graphics {
		if g.Type == "vnc" && g.Port > 0 {
			return fmt.Sprintf("0.0.0.0:%d", g.Port), nil
		}
	}
	return "", fmt.Errorf("no active VNC graphics found for domain %s", id)
}

func (d *Driver) ListInstances(ctx context.Context) ([]compute.InstanceInfo, error) {
	domains, _, err := d.lv.ConnectListAllDomains(1, golibvirt.ConnectListDomainsActive|golibvirt.ConnectListDomainsInactive)
	if err != nil {
		return nil, err
	}
	var out []compute.InstanceInfo
	for _, dom := range domains {
		// The domain name is always set to spec.ID (the Pulsar instance UUID)
		// at create time — use it as the canonical ID so it matches etcd keys.
		// The raw dom.UUID bytes formatted with %x produce a dash-less hex string
		// that never matches the standard UUID format stored in etcd.
		out = append(out, compute.InstanceInfo{
			ID:   dom.Name,
			Name: dom.Name,
		})
	}
	return out, nil
}

func (d *Driver) GetInstanceInfo(ctx context.Context, id string) (compute.InstanceInfo, error) {
	dom, err := d.lv.DomainLookupByName(id)
	if err != nil {
		return compute.InstanceInfo{}, err
	}
	rState, _, _, _, _, err := d.lv.DomainGetInfo(dom)
	if err != nil {
		return compute.InstanceInfo{}, err
	}
	state := domainStateString(golibvirt.DomainState(rState))
	return compute.InstanceInfo{ID: id, Name: id, State: state}, nil
}

func (d *Driver) AllocatedResources() (vcpusUsed int32, ramMBUsed int64) {
	domains, _, err := d.lv.ConnectListAllDomains(1, golibvirt.ConnectListDomainsActive|golibvirt.ConnectListDomainsInactive)
	if err != nil {
		return 0, 0
	}
	for _, dom := range domains {
		_, _, memKB, nrVCPU, _, err := d.lv.DomainGetInfo(dom)
		if err != nil {
			continue
		}
		vcpusUsed += int32(nrVCPU)
		ramMBUsed += int64(memKB) / 1024
	}
	return
}

func (d *Driver) MigrateInstance(ctx context.Context, id string, targetHost string, live bool) error {
	dom, err := d.lv.DomainLookupByName(id)
	if err != nil {
		return fmt.Errorf("domain lookup: %w", err)
	}

	// Always copy disks (local qcow2 overlays are not shared).
	flags := golibvirt.MigratePersistDest |
		golibvirt.MigrateUndefineSource |
		golibvirt.MigrateNonSharedDisk

	if live {
		flags |= golibvirt.MigrateLive | golibvirt.MigratePeer2peer
	}

	dconnuri := []string{"qemu+tcp://" + targetHost + "/system"}
	if _, err := d.lv.DomainMigratePerform3Params(dom, dconnuri, []golibvirt.TypedParam{}, []byte{}, flags); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// ResizeInstance adjusts the vCPU count and/or RAM of a domain.
// It attempts live hot-add via virsh setvcpus / setmem first (requires
// ACPI + memory balloon device in the guest). For stopped domains it
// falls back to offline config-only edits.
func (d *Driver) ResizeInstance(ctx context.Context, id string, vcpus int, ramMB int64) error {
	dom, err := d.lv.DomainLookupByName(id)
	if err != nil {
		return fmt.Errorf("domain lookup: %w", err)
	}

	rState, _, _, _, _, err := d.lv.DomainGetInfo(dom)
	if err != nil {
		return fmt.Errorf("get domain info: %w", err)
	}
	isRunning := golibvirt.DomainState(rState) == golibvirt.DomainRunning

	if vcpus > 0 {
		// --maximum must be updated in the persistent config first so that
		// the live count does not exceed the maximum.
		//nolint:gosec // id is a Pulsar-controlled instance UUID, not user input
		if out, err := exec.CommandContext(ctx, "virsh", "setvcpus", id,
			fmt.Sprintf("%d", vcpus), "--config", "--maximum",
		).CombinedOutput(); err != nil {
			return fmt.Errorf("virsh setvcpus --maximum: %w\n%s", err, out)
		}
		if out, err := exec.CommandContext(ctx, "virsh", "setvcpus", id, //nolint:gosec
			fmt.Sprintf("%d", vcpus), "--config",
		).CombinedOutput(); err != nil {
			return fmt.Errorf("virsh setvcpus --config: %w\n%s", err, out)
		}
		if isRunning {
			// Best-effort live hotplug; guest must support CPU hot-add.
			if out, err := exec.CommandContext(ctx, "virsh", "setvcpus", id, //nolint:gosec
				fmt.Sprintf("%d", vcpus), "--live",
			).CombinedOutput(); err != nil {
				// Non-fatal: the change will take effect on next boot.
				_ = out
			}
		}
	}

	if ramMB > 0 {
		ramKB := ramMB * 1024
		// Update the maximum memory in the persistent config.
		if out, err := exec.CommandContext(ctx, "virsh", "setmaxmem", id, //nolint:gosec // id is a Pulsar-controlled UUID
			fmt.Sprintf("%d", ramKB), "--config",
		).CombinedOutput(); err != nil {
			return fmt.Errorf("virsh setmaxmem: %w\n%s", err, out)
		}
		// Update the current memory in the persistent config.
		if out, err := exec.CommandContext(ctx, "virsh", "setmem", id, //nolint:gosec // id is a Pulsar-controlled UUID
			fmt.Sprintf("%d", ramKB), "--config",
		).CombinedOutput(); err != nil {
			return fmt.Errorf("virsh setmem --config: %w\n%s", err, out)
		}
		if isRunning {
			// Best-effort live balloon; guest must have virtio-balloon driver.
			if out, err := exec.CommandContext(ctx, "virsh", "setmem", id, //nolint:gosec // id is a Pulsar-controlled UUID
				fmt.Sprintf("%d", ramKB), "--live",
			).CombinedOutput(); err != nil {
				// Non-fatal: the change will take effect on next boot.
				_ = out
			}
		}
	}

	return nil
}

// ─── Domain XML builder ───────────────────────────────────────────────────────

type domain struct {
	XMLName  xml.Name    `xml:"domain"`
	Type     string      `xml:"type,attr"`
	Name     string      `xml:"name"`
	UUID     string      `xml:"uuid"`
	Metadata *domainMeta `xml:"metadata,omitempty"`
	Memory   memory      `xml:"memory"`
	VCPU     int         `xml:"vcpu"`
	OS       domainOS    `xml:"os"`
	Features features    `xml:"features"`
	Devices  devices     `xml:"devices"`
}

// domainMeta holds Pulsar-specific metadata stored inside the libvirt domain XML.
type domainMeta struct {
	Pulsar pulsarMeta `xml:"pulsar"`
}

type pulsarMeta struct {
	XMLName    xml.Name `xml:"http://pulsar.io/domain/1 pulsar"`
	ProjectID  string   `xml:"projectId,attr"`
	InstanceID string   `xml:"instanceId,attr"`
	Name       string   `xml:"name,attr"`
	FlavorID   string   `xml:"flavorId,attr"`
	ImageID    string   `xml:"imageId,attr"`
}

type memory struct {
	Unit  string `xml:"unit,attr"`
	Value int64  `xml:",chardata"`
}

type domainOS struct {
	Type domainOSType `xml:"type"`
	Boot boot         `xml:"boot"`
}

type domainOSType struct {
	Arch    string `xml:"arch,attr"`
	Machine string `xml:"machine,attr"`
	Value   string `xml:",chardata"`
}

type boot struct {
	Dev string `xml:"dev,attr"`
}

type features struct {
	ACPI struct{} `xml:"acpi"`
	APIC struct{} `xml:"apic"`
}

type devices struct {
	Emulator    string       `xml:"emulator"`
	Controllers []controller `xml:"controller"`
	Disks       []disk       `xml:"disk"`
	Interfaces  []iface      `xml:"interface"`
	Graphics    graphics     `xml:"graphics"`
	Console     console      `xml:"console"`
}

type controller struct {
	Type  string `xml:"type,attr"`
	Index string `xml:"index,attr"`
	Model string `xml:"model,attr,omitempty"`
}

// readonlyElem marshals to <readonly/> when non-nil.
type readonlyElem struct{}

type disk struct {
	Type     string        `xml:"type,attr"`
	Device   string        `xml:"device,attr"`
	Driver   diskDriver    `xml:"driver"`
	Source   diskSource    `xml:"source"`
	Target   diskTarget    `xml:"target"`
	ReadOnly *readonlyElem `xml:"readonly"`
}

type diskDriver struct {
	Name string `xml:"name,attr"`
	Type string `xml:"type,attr"`
}

type diskSource struct {
	File string `xml:"file,attr,omitempty"` // for type="file"
	Dev  string `xml:"dev,attr,omitempty"`  // for type="block"
}

type diskTarget struct {
	Dev string `xml:"dev,attr"`
	Bus string `xml:"bus,attr"`
}

type iface struct {
	Type        string            `xml:"type,attr"`
	Source      ifaceSource       `xml:"source"`
	MAC         ifaceMAC          `xml:"mac"`
	Model       ifaceModel        `xml:"model"`
	VirtualPort *ifaceVirtualPort `xml:"virtualport,omitempty"`
}

type ifaceVirtualPort struct {
	Type       string            `xml:"type,attr"`
	Parameters ifaceVPParameters `xml:"parameters"`
}

type ifaceVPParameters struct {
	InterfaceID string `xml:"interfaceid,attr"`
}

type ifaceSource struct {
	Bridge string `xml:"bridge,attr,omitempty"`
}

type ifaceMAC struct {
	Address string `xml:"address,attr"`
}

type ifaceModel struct {
	Type string `xml:"type,attr"`
}

type graphics struct {
	Type   string `xml:"type,attr"`
	Port   int    `xml:"port,attr"`
	Listen string `xml:"listen,attr"`
}

type console struct {
	Type string `xml:"type,attr"`
}

func buildDomainXML(spec compute.InstanceSpec, seedISO, domainType, emulator string) (string, error) {
	var ifaces []iface
	for _, p := range spec.NetworkPorts {
		i := iface{
			MAC:   ifaceMAC{Address: p.MACAddr},
			Model: ifaceModel{Type: "virtio"},
		}
		if p.OVSBridge != "" && p.PortID != "" {
			i.Type = "bridge"
			i.Source = ifaceSource{Bridge: p.OVSBridge}
			i.VirtualPort = &ifaceVirtualPort{
				Type:       "openvswitch",
				Parameters: ifaceVPParameters{InterfaceID: p.PortID},
			}
		} else {
			i.Type = "bridge"
			i.Source = ifaceSource{Bridge: p.Bridge}
		}
		ifaces = append(ifaces, i)
	}

	// Root disk: raw block device (boot-from-volume) or qcow2 overlay (image-based).
	var rootDisk disk
	if spec.BootVolumeDev != "" {
		rootDisk = disk{
			Type:   "block",
			Device: "disk",
			Driver: diskDriver{Name: "qemu", Type: "raw"},
			Source: diskSource{Dev: spec.BootVolumeDev},
			Target: diskTarget{Dev: "vda", Bus: "virtio"},
		}
	} else {
		rootDisk = disk{
			Type:   "file",
			Device: "disk",
			Driver: diskDriver{Name: "qemu", Type: "qcow2"},
			Source: diskSource{File: spec.ImagePath},
			Target: diskTarget{Dev: "vda", Bus: "virtio"},
		}
	}

	disks := []disk{
		rootDisk,
		{
			Type:     "file",
			Device:   "cdrom",
			Driver:   diskDriver{Name: "qemu", Type: "raw"},
			Source:   diskSource{File: seedISO},
			Target:   diskTarget{Dev: "sda", Bus: "sata"},
			ReadOnly: &readonlyElem{},
		},
	}

	d := domain{
		Type: domainType,
		Name: spec.ID, // Use UUID as libvirt domain name — unique across tenants
		UUID: spec.ID,
		Metadata: &domainMeta{
			Pulsar: pulsarMeta{
				ProjectID:  spec.ProjectID,
				InstanceID: spec.ID,
				Name:       spec.Name,
				FlavorID:   spec.FlavorID,
				ImageID:    spec.ImageID,
			},
		},
		Memory: memory{Unit: "MiB", Value: spec.RamMB},
		VCPU:   spec.VCPUs,
		OS: domainOS{
			Type: domainOSType{Arch: "x86_64", Machine: "q35", Value: "hvm"},
			Boot: boot{Dev: "hd"},
		},
		Devices: devices{
			Emulator: emulator,
			Controllers: []controller{
				// virtio-scsi controller: enables hotplug of scsi disks at runtime
				// without requiring a new PCI device to be hotplugged.
				{Type: "scsi", Index: "0", Model: "virtio-scsi"},
			},
			Disks:      disks,
			Interfaces: ifaces,
			Graphics:   graphics{Type: "vnc", Port: -1, Listen: "0.0.0.0"},
			Console:    console{Type: "pty"},
		},
	}

	out, err := xml.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", err
	}
	return strings.Replace(string(out), "&#39;", "'", -1), nil
}

func domainStateString(s golibvirt.DomainState) string {
	switch s {
	case golibvirt.DomainRunning:
		return "running"
	case golibvirt.DomainShutoff:
		return "stopped"
	case golibvirt.DomainPaused:
		return "paused"
	default:
		return "unknown"
	}
}
