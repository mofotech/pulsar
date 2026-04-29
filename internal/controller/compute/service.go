package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	computepb "github.com/agomez/pulsar/gen/proto/compute"
	imageservice "github.com/agomez/pulsar/internal/controller/image"
	keypairservice "github.com/agomez/pulsar/internal/controller/keypair"
	networkhandler "github.com/agomez/pulsar/internal/controller/network"
	"github.com/agomez/pulsar/internal/controller/registry"
	storagehandler "github.com/agomez/pulsar/internal/controller/storage"
	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/pkg/id"
)

const (
	instanceKeyPrefix = "/pulsar/compute/instances/"
	flavorKeyPrefix   = "/pulsar/compute/flavors/"
)

// pendingTask tracks an in-flight task so results can be routed back to the right instance.
type pendingTask struct {
	instanceID string
	projectID  string
	taskType   string
	targetHost string // used by instance.migrate to update NodeID on success
}

// pendingConsoleTask is a synchronous console request waiting for a VNC address.
type pendingConsoleTask struct {
	resultCh chan consoleResult
}

type consoleResult struct {
	vncAddr string
	err     error
}

// Service orchestrates instance lifecycle: scheduling, etcd state, and gRPC dispatch.
type Service struct {
	store               *etcd.Client
	registry            *registry.AgentRegistry
	netSvc              *networkhandler.Service
	storageSvc          *storagehandler.Service
	keypairSvc          *keypairservice.Service
	log                 *zap.Logger
	mu                  sync.Mutex
	pendingTasks        map[string]pendingTask        // task_id → pending
	pendingConsoleTasks map[string]pendingConsoleTask // task_id → console request
}

func NewService(store *etcd.Client, reg *registry.AgentRegistry, netSvc *networkhandler.Service, storageSvc *storagehandler.Service, log *zap.Logger) *Service {
	return &Service{
		store:               store,
		registry:            reg,
		netSvc:              netSvc,
		storageSvc:          storageSvc,
		log:                 log,
		pendingTasks:        make(map[string]pendingTask),
		pendingConsoleTasks: make(map[string]pendingConsoleTask),
	}
}

// SetKeypairService attaches an SSH keypair service for key injection at boot.
func (s *Service) SetKeypairService(kpSvc *keypairservice.Service) {
	s.keypairSvc = kpSvc
}

// HandleTaskResult is called by the registry whenever an agent sends a task result.
func (s *Service) HandleTaskResult(result *agentpb.TaskResult) {
	s.mu.Lock()
	// Check console tasks first (synchronous path).
	if consoleReq, ok := s.pendingConsoleTasks[result.TaskId]; ok {
		delete(s.pendingConsoleTasks, result.TaskId)
		s.mu.Unlock()
		if !result.Success {
			consoleReq.resultCh <- consoleResult{err: fmt.Errorf("%s", result.ErrorMessage)}
		} else {
			var res map[string]string
			json.Unmarshal(result.Result, &res) //nolint:errcheck
			consoleReq.resultCh <- consoleResult{vncAddr: res["vnc_addr"]}
		}
		return
	}
	task, ok := s.pendingTasks[result.TaskId]
	if ok {
		delete(s.pendingTasks, result.TaskId)
	}
	s.mu.Unlock()

	if !ok {
		return // not a compute task
	}

	ctx := context.Background()
	switch task.taskType {
	case "instance.create":
		inst, err := s.GetInstance(ctx, task.projectID, task.instanceID)
		if err != nil {
			s.log.Error("task result: instance not found", zap.String("instance_id", task.instanceID))
			return
		}
		if result.Success {
			s.log.Info("instance active", zap.String("id", inst.ID))
			s.transitionInstance(ctx, inst, StateActive)
		} else {
			s.log.Error("instance build failed",
				zap.String("id", inst.ID),
				zap.String("error", result.ErrorMessage),
			)
			s.transitionInstance(ctx, inst, StateError)
		}

	case "instance.delete":
		s.log.Info("instance deleted", zap.String("id", task.instanceID), zap.Bool("success", result.Success))
		// Read instance before deleting so we can honour delete_on_terminate.
		deletedInst, _ := s.GetInstance(ctx, task.projectID, task.instanceID)
		if s.storageSvc != nil {
			s.storageSvc.ReleaseVolumesByInstance(ctx, task.instanceID)
			if deletedInst != nil && deletedInst.DeleteBootVolume && deletedInst.BootVolumeID != "" {
				go s.storageSvc.DeleteVolume(context.Background(), deletedInst.ProjectID, deletedInst.BootVolumeID) //nolint:errcheck
			}
		}
		s.store.Delete(ctx, instanceKeyPrefix+task.projectID+"/"+task.instanceID) //nolint:errcheck

	case "instance.start":
		inst, err := s.GetInstance(ctx, task.projectID, task.instanceID)
		if err != nil {
			return
		}
		if result.Success {
			s.transitionInstance(ctx, inst, StateActive)
		} else {
			s.transitionInstance(ctx, inst, StateError)
		}

	case "instance.stop":
		inst, err := s.GetInstance(ctx, task.projectID, task.instanceID)
		if err != nil {
			return
		}
		if result.Success {
			s.transitionInstance(ctx, inst, StateStopped)
		} else {
			s.transitionInstance(ctx, inst, StateError)
		}

	case "instance.reboot":
		inst, err := s.GetInstance(ctx, task.projectID, task.instanceID)
		if err != nil {
			return
		}
		if result.Success {
			s.transitionInstance(ctx, inst, StateActive)
		} else {
			s.transitionInstance(ctx, inst, StateError)
		}

	case "instance.migrate":
		inst, err := s.GetInstance(ctx, task.projectID, task.instanceID)
		if err != nil {
			return
		}
		if result.Success {
			// targetHost was stashed in the task metadata field via pendingTask.
			inst.NodeID = task.targetHost
			inst.UpdatedAt = time.Now().UTC()
			if err := s.saveInstance(ctx, inst); err != nil {
				s.log.Error("migrate: save instance", zap.Error(err))
			}
			s.transitionInstance(ctx, inst, StateActive)
		} else {
			s.log.Error("migration failed",
				zap.String("instance_id", inst.ID),
				zap.String("error", result.ErrorMessage),
			)
			s.transitionInstance(ctx, inst, StateError)
		}

	case "instance.resize":
		inst, err := s.GetInstance(ctx, task.projectID, task.instanceID)
		if err != nil {
			return
		}
		if result.Success {
			// newFlavorID was stashed in pendingTask.targetHost (re-used field).
			if task.targetHost != "" {
				inst.FlavorID = task.targetHost
			}
			inst.UpdatedAt = time.Now().UTC()
			if err := s.saveInstance(ctx, inst); err != nil {
				s.log.Error("resize: save instance", zap.Error(err))
			}
			s.transitionInstance(ctx, inst, StateActive)
		} else {
			s.log.Error("resize failed",
				zap.String("instance_id", inst.ID),
				zap.String("error", result.ErrorMessage),
			)
			s.transitionInstance(ctx, inst, StateError)
		}
	}
}

// CreateInstance schedules and launches a new instance.
func (s *Service) CreateInstance(ctx context.Context, projectID, userID string, req CreateInstanceRequest) (*Instance, error) {
	flavor, err := s.GetFlavor(ctx, req.FlavorID)
	if err != nil {
		return nil, fmt.Errorf("flavor not found: %w", err)
	}

	bootFromVolume := len(req.BlockDeviceMappings) > 0

	var image *imageservice.Image
	if !bootFromVolume {
		// Normal image-based boot requires an image.
		if req.ImageID == "" {
			return nil, fmt.Errorf("image_id required when block_device_mappings is not set")
		}
		image, err = s.GetImage(ctx, req.ImageID)
		if err != nil {
			return nil, fmt.Errorf("image not found: %w", err)
		}
	}

	ht := req.HypervisorType
	if ht == "" {
		ht = HypervisorKVM // default
	}

	imageID := ""
	if image != nil {
		imageID = image.ID
	}

	inst := &Instance{
		ID:             id.New(),
		ProjectID:      projectID,
		Name:           req.Name,
		FlavorID:       flavor.ID,
		ImageID:        imageID,
		HypervisorType: ht,
		Status:         StatePending,
		UserData:       req.UserData,
		KeyNames:       req.KeyNames,
		Metadata:       req.Metadata,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	// Resolve keypair public keys up front (fail fast on unknown names).
	// Keys are looked up by user ID — they are user-scoped, not project-scoped.
	var sshPublicKeys []string
	if len(req.KeyNames) > 0 && s.keypairSvc != nil {
		sshPublicKeys, err = s.keypairSvc.PublicKeysForNames(ctx, userID, req.KeyNames)
		if err != nil {
			return nil, fmt.Errorf("resolve keypairs: %w", err)
		}
	}

	if err := s.saveInstance(ctx, inst); err != nil {
		return nil, err
	}

	// Async: schedule → dispatch to agent
	go s.scheduleAndBoot(context.Background(), inst, req, flavor, image, sshPublicKeys)

	return inst, nil
}

func (s *Service) scheduleAndBoot(ctx context.Context, inst *Instance, req CreateInstanceRequest, flavor *Flavor, image *imageservice.Image, sshPublicKeys []string) {
	s.transitionInstance(ctx, inst, StateScheduling)

	// Pick compute agent
	agents := s.registry.ListByPillar("compute")
	if len(agents) == 0 {
		s.log.Error("no compute agents available", zap.String("instance_id", inst.ID))
		s.transitionInstance(ctx, inst, StateError)
		return
	}
	target := pickAgent(agents, string(inst.HypervisorType))
	if target == nil {
		s.log.Error("no agent supports hypervisor type",
			zap.String("instance_id", inst.ID),
			zap.String("hypervisor", string(inst.HypervisorType)),
		)
		s.transitionInstance(ctx, inst, StateError)
		return
	}

	inst.NodeID = target.AgentID
	s.transitionInstance(ctx, inst, StateScheduled)

	// Auto-create ports for each requested network (if network service is available)
	var portSpecs []*computepb.NetworkPortSpec
	if s.netSvc != nil {
		for _, nr := range req.Networks {
			if nr.NetworkID == "" {
				continue
			}
			// Strip "network:" prefix if present
			networkID := strings.TrimPrefix(nr.NetworkID, "network:")
			portInfo, err := s.netSvc.AutoCreatePort(ctx, inst.ProjectID, networkID)
			if err != nil {
				s.log.Error("auto-create port failed",
					zap.String("instance_id", inst.ID),
					zap.String("network_id", networkID),
					zap.Error(err),
				)
				s.transitionInstance(ctx, inst, StateError)
				return
			}
			inst.PortIDs = append(inst.PortIDs, portInfo.ID)
			portSpecs = append(portSpecs, &computepb.NetworkPortSpec{
				PortId:    portInfo.ID,
				MacAddr:   portInfo.MACAddress,
				Bridge:    "br-int",
				OvsBridge: "br-int",
			})
		}
		// Bind ports to the chassis (non-fatal)
		for _, portID := range inst.PortIDs {
			if bindErr := s.netSvc.BindPort(ctx, portID, target.AgentID); bindErr != nil {
				s.log.Warn("port bind failed", zap.String("port_id", portID), zap.Error(bindErr))
			}
		}
	}

	// Build task payload. Inject Pulsar-internal fields via the metadata map
	// so the agent can embed them in the libvirt domain XML without requiring
	// a proto regeneration.
	taskMeta := make(map[string]string, len(inst.Metadata)+6)
	for k, v := range inst.Metadata {
		taskMeta[k] = v
	}
	taskMeta["pulsar:project_id"] = inst.ProjectID
	taskMeta["pulsar:flavor_id"] = inst.FlavorID
	taskMeta["pulsar:image_id"] = inst.ImageID

	// Handle boot-from-volume: export iSCSI target and inject connection info
	// into the task metadata so the compute agent can log in at create time.
	var imageURL string
	if image != nil {
		imageURL = image.URL
	}
	if len(req.BlockDeviceMappings) > 0 && s.storageSvc != nil {
		bdm := req.BlockDeviceMappings[0]
		bootVol, err := s.storageSvc.ExportVolumeISCSI(ctx, inst.ProjectID, bdm.VolumeID, inst.ID, target.AgentID)
		if err != nil {
			s.log.Error("boot volume iSCSI export failed",
				zap.String("instance_id", inst.ID),
				zap.String("volume_id", bdm.VolumeID),
				zap.Error(err),
			)
			s.transitionInstance(ctx, inst, StateError)
			return
		}
		taskMeta["pulsar:boot_volume_id"] = bdm.VolumeID
		taskMeta["pulsar:boot_iscsi_iqn"] = bootVol.ISCSIIQN
		taskMeta["pulsar:boot_iscsi_portal"] = bootVol.ISCSIPortal
		inst.BootVolumeID = bdm.VolumeID
		inst.DeleteBootVolume = bdm.DeleteOnTerminate
		// Persist boot volume tracking on instance
		if err := s.saveInstance(ctx, inst); err != nil {
			s.log.Error("save instance with boot volume", zap.Error(err))
		}
	}

	taskPayload := &computepb.CreateInstanceTask{
		Id:             inst.ID,
		Name:           inst.Name,
		HypervisorType: string(inst.HypervisorType),
		Vcpus:          int32(flavor.VCPUs),
		RamMb:          int64(flavor.RamMB),
		DiskGb:         int64(flavor.DiskGB),
		ImageUrl:       imageURL,
		UserData:       inst.UserData,
		Metadata:       taskMeta,
		NetworkPorts:   portSpecs,
		SshPublicKeys:  sshPublicKeys,
	}
	payload, _ := json.Marshal(taskPayload)

	taskID := "compute:" + id.New()
	taskMsg := &agentpb.ControllerMessage{
		Payload: &agentpb.ControllerMessage_TaskAssignment{
			TaskAssignment: &agentpb.TaskAssignment{
				TaskId:         taskID,
				TaskType:       "instance.create",
				Payload:        payload,
				TimeoutSeconds: 300,
			},
		},
	}

	s.mu.Lock()
	s.pendingTasks[taskID] = pendingTask{
		instanceID: inst.ID,
		projectID:  inst.ProjectID,
		taskType:   "instance.create",
	}
	s.mu.Unlock()

	s.transitionInstance(ctx, inst, StateBuilding)

	if !s.registry.Send("compute", target.AgentID, taskMsg) {
		s.log.Error("failed to dispatch task to agent",
			zap.String("agent_id", target.AgentID),
			zap.String("instance_id", inst.ID),
		)
		s.transitionInstance(ctx, inst, StateError)
	}
}

// pickAgent selects the least-loaded agent that supports the required hypervisor type.
func pickAgent(agents []*registry.AgentRecord, hypervisorType string) *registry.AgentRecord {
	var best *registry.AgentRecord
	var bestLoad float64 = 2.0

	for _, a := range agents {
		supported := false
		for _, ht := range a.Capabilities.HypervisorTypes {
			if ht == hypervisorType {
				supported = true
				break
			}
		}
		if !supported {
			continue
		}
		total := a.Resources.VcpusTotal
		if total == 0 {
			total = 1
		}
		load := float64(a.Resources.VcpusUsed) / float64(total)
		if best == nil || load < bestLoad {
			best = a
			bestLoad = load
		}
	}
	return best
}

// HandleAgentLost is called by the registry when a compute agent goes stale.
// Instances in stable states (active, stopped) are moved to StateUnknown —
// the VM may still be running on the hypervisor; the controller has simply lost
// contact with the node. Only in-flight transitional states (building, starting,
// stopping) are moved to StateError because those tasks definitely did not
// complete successfully.
func (s *Service) HandleAgentLost(rec *registry.AgentRecord) {
	ctx := context.Background()
	vals, err := s.store.GetPrefix(ctx, instanceKeyPrefix)
	if err != nil {
		s.log.Error("agent lost: failed to list instances", zap.Error(err))
		return
	}
	for _, v := range vals {
		var inst Instance
		if err := json.Unmarshal([]byte(v), &inst); err != nil {
			continue
		}
		if inst.NodeID != rec.AgentID {
			continue
		}
		switch inst.Status {
		case StateDeleted, StateDeleting, StateError, StateUnknown:
			continue
		case StateBuilding, StateStarting, StateStopping:
			// In-flight operation — task will never complete, so error is correct.
			s.log.Warn("marking instance error: in-flight operation when agent lost",
				zap.String("instance_id", inst.ID),
				zap.String("status", string(inst.Status)),
				zap.String("agent_id", rec.AgentID),
			)
			s.transitionInstance(ctx, &inst, StateError)
		default:
			// Stable state (active, stopped, etc.) — VM may still be running.
			s.log.Warn("marking instance unknown: compute agent lost",
				zap.String("instance_id", inst.ID),
				zap.String("prior_status", string(inst.Status)),
				zap.String("agent_id", rec.AgentID),
			)
			s.transitionInstance(ctx, &inst, StateUnknown)
		}
	}
}

// HandleAgentReconnected is called when a previously-lost agent sends its first
// heartbeat after reconnecting. It reconciles unknown instances back to their
// last known stable state. A future enhancement would query the agent for live
// VM states; for now we optimistically restore active→active / stopped→stopped
// based on stored pre-unknown status. We default to stopped if we can't tell.
func (s *Service) HandleAgentReconnected(agentID string) {
	ctx := context.Background()
	vals, err := s.store.GetPrefix(ctx, instanceKeyPrefix)
	if err != nil {
		s.log.Error("agent reconnect: failed to list instances", zap.Error(err))
		return
	}
	for _, v := range vals {
		var inst Instance
		if err := json.Unmarshal([]byte(v), &inst); err != nil {
			continue
		}
		if inst.NodeID != agentID || inst.Status != StateUnknown {
			continue
		}
		// Default to stopped — safer than active; operator can start if needed.
		s.log.Info("recovering instance from unknown: agent reconnected",
			zap.String("instance_id", inst.ID),
			zap.String("agent_id", agentID),
		)
		s.transitionInstance(ctx, &inst, StateStopped)
	}
}

func (s *Service) transitionInstance(ctx context.Context, inst *Instance, state InstanceState) {
	inst.Status = state
	inst.UpdatedAt = time.Now().UTC()
	if err := s.saveInstance(ctx, inst); err != nil {
		s.log.Error("failed to save instance state", zap.String("id", inst.ID), zap.Error(err))
	}
}

// ResetInstance moves an instance from error back to stopped so it can be
// started again. Only valid when the instance is in the error state.
func (s *Service) ResetInstance(ctx context.Context, projectID, instanceID string) (*Instance, error) {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return nil, err
	}
	if inst.Status != StateError {
		return nil, fmt.Errorf("instance is not in error state (current: %s)", inst.Status)
	}
	s.transitionInstance(ctx, inst, StateStopped)
	return inst, nil
}

// AttachInterface adds a network interface to the instance.
// If portID is non-empty the existing port is used; otherwise a new port is
// created on networkID.
func (s *Service) AttachInterface(ctx context.Context, projectID, instanceID, networkID, portID string) (*networkhandler.Port, error) {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return nil, err
	}
	if s.netSvc == nil {
		return nil, fmt.Errorf("network service unavailable")
	}

	var port *networkhandler.Port
	if portID != "" {
		// Use an existing port.
		port, err = s.netSvc.GetPort(ctx, portID)
		if err != nil {
			return nil, fmt.Errorf("port not found: %w", err)
		}
	} else {
		// Create a new port on the requested network.
		if networkID == "" {
			return nil, fmt.Errorf("network_id or port_id required")
		}
		portInfo, err := s.netSvc.AutoCreatePort(ctx, projectID, networkID)
		if err != nil {
			return nil, fmt.Errorf("create port: %w", err)
		}
		port, _ = s.netSvc.GetPort(ctx, portInfo.ID)
		portID = portInfo.ID
	}

	inst.PortIDs = append(inst.PortIDs, portID)
	if err := s.saveInstance(ctx, inst); err != nil {
		return nil, fmt.Errorf("save instance: %w", err)
	}
	// Best-effort bind to chassis so OVN wires the port.
	if inst.NodeID != "" {
		s.netSvc.BindPort(ctx, portID, inst.NodeID) //nolint:errcheck
	}
	return port, nil
}

// DetachInterface deletes portID from the network and removes it from the instance.
func (s *Service) DetachInterface(ctx context.Context, projectID, instanceID, portID string) error {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return err
	}
	if s.netSvc == nil {
		return fmt.Errorf("network service unavailable")
	}
	if err := s.netSvc.DeletePort(ctx, portID); err != nil {
		return fmt.Errorf("delete port: %w", err)
	}
	newIDs := inst.PortIDs[:0]
	for _, pid := range inst.PortIDs {
		if pid != portID {
			newIDs = append(newIDs, pid)
		}
	}
	inst.PortIDs = newIDs
	return s.saveInstance(ctx, inst)
}

// ─── CRUD helpers ─────────────────────────────────────────────────────────────

func (s *Service) saveInstance(ctx context.Context, inst *Instance) error {
	data, err := json.Marshal(inst)
	if err != nil {
		return err
	}
	return s.store.Put(ctx, instanceKeyPrefix+inst.ProjectID+"/"+inst.ID, string(data))
}

func (s *Service) GetInstance(ctx context.Context, projectID, instanceID string) (*Instance, error) {
	val, err := s.store.Get(ctx, instanceKeyPrefix+projectID+"/"+instanceID)
	if err != nil {
		return nil, err
	}
	if val == "" {
		return nil, fmt.Errorf("instance not found")
	}
	var inst Instance
	if err := json.Unmarshal([]byte(val), &inst); err != nil {
		return nil, err
	}
	s.enrichAddresses(ctx, &inst)
	return &inst, nil
}

func (s *Service) ListInstances(ctx context.Context, projectID string) ([]*Instance, error) {
	vals, err := s.store.GetPrefix(ctx, instanceKeyPrefix+projectID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]*Instance, 0)
	for _, v := range vals {
		var inst Instance
		if err := json.Unmarshal([]byte(v), &inst); err == nil {
			s.enrichAddresses(ctx, &inst)
			out = append(out, &inst)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Service) UpdateMetadata(ctx context.Context, projectID, instanceID string, metadata map[string]string) (*Instance, error) {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return nil, err
	}
	inst.Metadata = metadata
	inst.UpdatedAt = time.Now().UTC()
	if err := s.saveInstance(ctx, inst); err != nil {
		return nil, err
	}
	return inst, nil
}

func (s *Service) SetMetadataKey(ctx context.Context, projectID, instanceID, key, value string) (*Instance, error) {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return nil, err
	}
	if inst.Metadata == nil {
		inst.Metadata = make(map[string]string)
	}
	inst.Metadata[key] = value
	inst.UpdatedAt = time.Now().UTC()
	if err := s.saveInstance(ctx, inst); err != nil {
		return nil, err
	}
	return inst, nil
}

func (s *Service) DeleteMetadataKey(ctx context.Context, projectID, instanceID, key string) (*Instance, error) {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return nil, err
	}
	delete(inst.Metadata, key)
	inst.UpdatedAt = time.Now().UTC()
	if err := s.saveInstance(ctx, inst); err != nil {
		return nil, err
	}
	return inst, nil
}

// enrichAddresses populates inst.Addresses by looking up each port's fixed IPs.
// Errors are silently ignored so a missing port doesn't break the list.
func (s *Service) enrichAddresses(ctx context.Context, inst *Instance) {
	if s.netSvc == nil || len(inst.PortIDs) == 0 {
		return
	}
	inst.Addresses = inst.Addresses[:0]
	for _, portID := range inst.PortIDs {
		port, err := s.netSvc.GetPort(ctx, portID)
		if err != nil {
			continue
		}
		for _, fip := range port.FixedIPs {
			inst.Addresses = append(inst.Addresses, InstanceAddress{
				NetworkID: port.NetworkID,
				IPAddress: fip.IPAddress,
				MACAddr:   port.MACAddress,
			})
		}
	}
}

func (s *Service) DeleteInstance(ctx context.Context, projectID, instanceID string) error {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return err
	}

	// Clean up network ports regardless of VM state.
	if s.netSvc != nil {
		for _, portID := range inst.PortIDs {
			if perr := s.netSvc.DeletePort(ctx, portID); perr != nil {
				s.log.Warn("failed to delete port on instance delete",
					zap.String("port_id", portID), zap.Error(perr))
			}
		}
	}

	// For instances stuck in non-running states or with no node, force-purge from etcd.
	// Also send a best-effort cleanup task to the agent if we know which node it was on.
	stuck := inst.Status == StateError ||
		inst.Status == StateDeleting ||
		inst.Status == StateBuilding ||
		inst.NodeID == ""

	if inst.NodeID != "" {
		taskID := "compute:" + id.New()
		payload, _ := json.Marshal(computepb.DeleteInstanceTask{Id: inst.ID, Force: true})
		if !stuck {
			// Normal path: track the task and wait for the result to purge etcd.
			s.mu.Lock()
			s.pendingTasks[taskID] = pendingTask{
				instanceID: inst.ID,
				projectID:  inst.ProjectID,
				taskType:   "instance.delete",
			}
			s.mu.Unlock()
		}
		s.registry.Send("compute", inst.NodeID, &agentpb.ControllerMessage{
			Payload: &agentpb.ControllerMessage_TaskAssignment{
				TaskAssignment: &agentpb.TaskAssignment{
					TaskId:   taskID,
					TaskType: "instance.delete",
					Payload:  payload,
				},
			},
		})
	}

	if stuck {
		// Purge immediately — don't wait for agent confirmation.
		if s.storageSvc != nil {
			s.storageSvc.ReleaseVolumesByInstance(ctx, instanceID)
			if inst.DeleteBootVolume && inst.BootVolumeID != "" {
				go s.storageSvc.DeleteVolume(context.Background(), inst.ProjectID, inst.BootVolumeID) //nolint:errcheck
			}
		}
		return s.store.Delete(ctx, instanceKeyPrefix+projectID+"/"+instanceID)
	}

	inst.Status = StateDeleting
	return s.saveInstance(ctx, inst)
}

// PerformAction dispatches a lifecycle action (start/stop/reboot/hard-reboot) to the agent.
func (s *Service) PerformAction(ctx context.Context, projectID, instanceID, action string) error {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return err
	}
	if inst.NodeID == "" {
		return fmt.Errorf("instance has no assigned node")
	}

	var taskType string
	var payload []byte

	switch action {
	case "start":
		taskType = "instance.start"
		payload, _ = json.Marshal(map[string]string{"id": instanceID})
	case "stop":
		taskType = "instance.stop"
		payload, _ = json.Marshal(map[string]interface{}{"id": instanceID, "force": false})
	case "hard-reboot":
		taskType = "instance.reboot"
		payload, _ = json.Marshal(map[string]interface{}{"id": instanceID, "hard": true})
	case "reboot":
		taskType = "instance.reboot"
		payload, _ = json.Marshal(map[string]interface{}{"id": instanceID, "hard": false})
	default:
		return fmt.Errorf("unknown action: %s", action)
	}

	taskID := "compute:" + id.New()
	s.mu.Lock()
	s.pendingTasks[taskID] = pendingTask{
		instanceID: inst.ID,
		projectID:  inst.ProjectID,
		taskType:   taskType,
	}
	s.mu.Unlock()

	sent := s.registry.Send("compute", inst.NodeID, &agentpb.ControllerMessage{
		Payload: &agentpb.ControllerMessage_TaskAssignment{
			TaskAssignment: &agentpb.TaskAssignment{
				TaskId:         taskID,
				TaskType:       taskType,
				Payload:        payload,
				TimeoutSeconds: 60,
			},
		},
	})
	if !sent {
		s.mu.Lock()
		delete(s.pendingTasks, taskID)
		s.mu.Unlock()
		return fmt.Errorf("agent %s not reachable", inst.NodeID)
	}

	// Optimistically update state so the UI reflects the transition immediately.
	switch action {
	case "start":
		s.transitionInstance(ctx, inst, StateStarting)
	case "stop", "hard-reboot":
		s.transitionInstance(ctx, inst, StateStopping)
	}
	return nil
}

// MigrateInstance live-migrates (or offline-migrates) an instance to targetHost.
// targetHost is the hostname/IP resolvable by libvirt on the source node.
func (s *Service) MigrateInstance(ctx context.Context, projectID, instanceID, targetHost string, live bool) error {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return err
	}
	if inst.NodeID == "" {
		return fmt.Errorf("instance has no assigned node")
	}
	if inst.Status != StateActive && inst.Status != StateStopped {
		return fmt.Errorf("cannot migrate instance in state %s", inst.Status)
	}

	payload, _ := json.Marshal(computepb.MigrateInstanceTask{
		Id:         instanceID,
		TargetHost: targetHost,
		Live:       live,
	})

	taskID := "compute:" + id.New()
	s.mu.Lock()
	s.pendingTasks[taskID] = pendingTask{
		instanceID: inst.ID,
		projectID:  inst.ProjectID,
		taskType:   "instance.migrate",
		targetHost: targetHost,
	}
	s.mu.Unlock()

	sent := s.registry.Send("compute", inst.NodeID, &agentpb.ControllerMessage{
		Payload: &agentpb.ControllerMessage_TaskAssignment{
			TaskAssignment: &agentpb.TaskAssignment{
				TaskId:         taskID,
				TaskType:       "instance.migrate",
				Payload:        payload,
				TimeoutSeconds: 600,
			},
		},
	})
	if !sent {
		s.mu.Lock()
		delete(s.pendingTasks, taskID)
		s.mu.Unlock()
		return fmt.Errorf("source agent %s not reachable", inst.NodeID)
	}
	return nil
}

// ResizeInstance changes the flavor of a running or stopped instance.
// CPU and RAM changes are dispatched to the agent; the flavor ID is updated
// on the controller record once the agent reports success.
func (s *Service) ResizeInstance(ctx context.Context, projectID, instanceID string, req ResizeInstanceRequest) (*Instance, error) {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return nil, err
	}
	if inst.NodeID == "" {
		return nil, fmt.Errorf("instance has no assigned node")
	}
	if inst.Status != StateActive && inst.Status != StateStopped {
		return nil, fmt.Errorf("cannot resize instance in state %s", inst.Status)
	}

	flavor, err := s.GetFlavor(ctx, req.FlavorID)
	if err != nil {
		return nil, fmt.Errorf("flavor not found: %w", err)
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"id":     instanceID,
		"vcpus":  flavor.VCPUs,
		"ram_mb": flavor.RamMB,
	})

	taskID := "compute:" + id.New()
	s.mu.Lock()
	s.pendingTasks[taskID] = pendingTask{
		instanceID: inst.ID,
		projectID:  inst.ProjectID,
		taskType:   "instance.resize",
		targetHost: flavor.ID, // re-use targetHost field to carry the new flavor ID
	}
	s.mu.Unlock()

	sent := s.registry.Send("compute", inst.NodeID, &agentpb.ControllerMessage{
		Payload: &agentpb.ControllerMessage_TaskAssignment{
			TaskAssignment: &agentpb.TaskAssignment{
				TaskId:         taskID,
				TaskType:       "instance.resize",
				Payload:        payload,
				TimeoutSeconds: 60,
			},
		},
	})
	if !sent {
		s.mu.Lock()
		delete(s.pendingTasks, taskID)
		s.mu.Unlock()
		return nil, fmt.Errorf("agent %s not reachable", inst.NodeID)
	}
	return inst, nil
}

// RequestConsole dispatches an instance.console task to the agent synchronously
// and returns the VNC address (host:port) reported by the agent.
func (s *Service) RequestConsole(ctx context.Context, projectID, instanceID string) (string, error) {
	inst, err := s.GetInstance(ctx, projectID, instanceID)
	if err != nil {
		return "", err
	}

	// Build a priority list of agents to try: assigned node first, then any compute agent.
	// This handles the case where the original agent container was replaced but libvirtd
	// (running on the host) still has the VM — any compute agent can reach it.
	var candidates []string
	if inst.NodeID != "" {
		candidates = append(candidates, inst.NodeID)
	}
	for _, a := range s.registry.ListByPillar("compute") {
		if a.AgentID != inst.NodeID {
			candidates = append(candidates, a.AgentID)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no compute agent available")
	}

	payload, _ := json.Marshal(map[string]string{"id": instanceID})

	for _, agentID := range candidates {
		taskID := "compute:" + id.New()
		ch := make(chan consoleResult, 1)
		s.mu.Lock()
		s.pendingConsoleTasks[taskID] = pendingConsoleTask{resultCh: ch}
		s.mu.Unlock()

		sent := s.registry.Send("compute", agentID, &agentpb.ControllerMessage{
			Payload: &agentpb.ControllerMessage_TaskAssignment{
				TaskAssignment: &agentpb.TaskAssignment{
					TaskId:         taskID,
					TaskType:       "instance.console",
					Payload:        payload,
					TimeoutSeconds: 10,
				},
			},
		})
		if !sent {
			s.mu.Lock()
			delete(s.pendingConsoleTasks, taskID)
			s.mu.Unlock()
			continue
		}

		select {
		case r := <-ch:
			if r.err == nil {
				return r.vncAddr, nil
			}
			// Try next agent on failure.
		case <-time.After(12 * time.Second):
			s.mu.Lock()
			delete(s.pendingConsoleTasks, taskID)
			s.mu.Unlock()
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "", fmt.Errorf("no agent could provide console for instance %s", instanceID)
}

// ─── Flavor helpers ───────────────────────────────────────────────────────────

func (s *Service) GetFlavor(ctx context.Context, flavorID string) (*Flavor, error) {
	val, err := s.store.Get(ctx, flavorKeyPrefix+flavorID)
	if err != nil || val == "" {
		return nil, fmt.Errorf("flavor %s not found", flavorID)
	}
	var f Flavor
	return &f, json.Unmarshal([]byte(val), &f)
}

func (s *Service) ListFlavors(ctx context.Context) ([]*Flavor, error) {
	vals, err := s.store.GetPrefix(ctx, flavorKeyPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]*Flavor, 0)
	for _, v := range vals {
		var f Flavor
		if err := json.Unmarshal([]byte(v), &f); err == nil {
			out = append(out, &f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Service) CreateFlavor(ctx context.Context, f *Flavor) error {
	if f.ID == "" {
		f.ID = id.New()
	}
	data, _ := json.Marshal(f)
	return s.store.Put(ctx, flavorKeyPrefix+f.ID, string(data))
}

func (s *Service) DeleteFlavor(ctx context.Context, flavorID string) error {
	return s.store.Delete(ctx, flavorKeyPrefix+flavorID)
}

// ─── Image helpers ────────────────────────────────────────────────────────────

// GetImage looks up an image from the shared image registry.
func (s *Service) GetImage(ctx context.Context, imageID string) (*imageservice.Image, error) {
	val, err := s.store.Get(ctx, "/pulsar/images/"+imageID)
	if err != nil || val == "" {
		return nil, fmt.Errorf("image %s not found", imageID)
	}
	var img imageservice.Image
	return &img, json.Unmarshal([]byte(val), &img)
}
