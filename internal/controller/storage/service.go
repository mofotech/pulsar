package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	"github.com/agomez/pulsar/internal/controller/registry"
	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/pkg/id"
)

const (
	volumeKeyPrefix     = "/pulsar/storage/volumes/"
	volumeIndexKey      = "/pulsar/storage/volumes/idx/" // volumeID → projectID
	snapshotKeyPrefix   = "/pulsar/storage/snapshots/"
	volumeTypeKeyPrefix = "/pulsar/storage/volume-types/"
	tidCounterKey       = "/pulsar/storage/iscsi/tid_counter"
)

// pendingTask tracks an outstanding task so results route back correctly.
type pendingTask struct {
	volumeID  string
	projectID string
	taskType  string
	resultCh  chan taskResult // non-nil for synchronous dispatch
}

type taskResult struct {
	result []byte
	err    error
}

// Service manages storage state in etcd and dispatches work to storage/compute agents.
type Service struct {
	store    *etcd.Client
	registry *registry.AgentRegistry
	log      *zap.Logger
	mu       sync.Mutex
	pending  map[string]pendingTask // task_id → pending
}

func NewService(store *etcd.Client, reg *registry.AgentRegistry, log *zap.Logger) *Service {
	return &Service{
		store:    store,
		registry: reg,
		log:      log,
		pending:  make(map[string]pendingTask),
	}
}

// HandleAgentLost is called by the registry when a storage agent goes stale.
func (s *Service) HandleAgentLost(rec *registry.AgentRecord) {
	s.log.Warn("storage agent lost",
		zap.String("agent_id", rec.AgentID),
	)
}

// HandleTaskResult is registered with the AgentRegistry for "storage:" prefixed tasks.
func (s *Service) HandleTaskResult(result *agentpb.TaskResult) {
	s.mu.Lock()
	pt, ok := s.pending[result.TaskId]
	if ok {
		delete(s.pending, result.TaskId)
	}
	s.mu.Unlock()

	if !ok {
		return
	}

	// Synchronous callers waiting on a resultCh
	if pt.resultCh != nil {
		if result.Success {
			pt.resultCh <- taskResult{result: result.Result}
		} else {
			pt.resultCh <- taskResult{err: fmt.Errorf("%s", result.ErrorMessage)}
		}
		return
	}

	// Async task completion (volume.create, volume.delete)
	ctx := context.Background()
	vol, err := s.getVolumeByID(ctx, pt.volumeID)
	if err != nil {
		s.log.Error("task result: volume not found",
			zap.String("volume_id", pt.volumeID),
			zap.String("task_type", pt.taskType),
		)
		return
	}

	switch pt.taskType {
	case "volume.create":
		if result.Success {
			s.log.Info("volume created", zap.String("id", vol.ID))
			s.setVolumeStatus(ctx, vol, VolumeStatusAvailable)
		} else {
			s.log.Error("volume create failed", zap.String("id", vol.ID), zap.String("error", result.ErrorMessage))
			s.setVolumeStatus(ctx, vol, VolumeStatusError)
		}

	case "volume.delete":
		if result.Success {
			s.log.Info("volume deleted", zap.String("id", vol.ID))
			s.store.Delete(ctx, volumeKeyPrefix+vol.ProjectID+"/"+vol.ID) //nolint:errcheck
			s.store.Delete(ctx, volumeIndexKey+vol.ID)                    //nolint:errcheck
		} else {
			s.log.Error("volume delete failed", zap.String("id", vol.ID), zap.String("error", result.ErrorMessage))
			s.setVolumeStatus(ctx, vol, VolumeStatusError)
		}

	case "volume.snapshot.create":
		snap, err := s.getSnapshotByVolume(ctx, pt.projectID, vol.ID)
		if err == nil {
			if result.Success {
				s.setSnapshotStatus(ctx, snap, SnapshotStatusAvailable)
			} else {
				s.setSnapshotStatus(ctx, snap, SnapshotStatusError)
			}
		}
	}
}

// ─── Synchronous dispatch helper ─────────────────────────────────────────────

// dispatch sends a task to an agent and waits for the result (up to timeoutSecs).
func (s *Service) dispatch(ctx context.Context, pillar, agentID, taskType string, payload []byte, timeoutSecs int32) ([]byte, error) {
	taskID := "storage:" + id.New()
	resultCh := make(chan taskResult, 1)

	s.mu.Lock()
	s.pending[taskID] = pendingTask{resultCh: resultCh}
	s.mu.Unlock()

	msg := &agentpb.ControllerMessage{
		Payload: &agentpb.ControllerMessage_TaskAssignment{
			TaskAssignment: &agentpb.TaskAssignment{
				TaskId:         taskID,
				TaskType:       taskType,
				Payload:        payload,
				TimeoutSeconds: timeoutSecs,
			},
		},
	}
	if !s.registry.Send(pillar, agentID, msg) {
		s.mu.Lock()
		delete(s.pending, taskID)
		s.mu.Unlock()
		return nil, fmt.Errorf("agent %s/%s not available", pillar, agentID)
	}

	select {
	case res := <-resultCh:
		return res.result, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(time.Duration(timeoutSecs) * time.Second):
		s.mu.Lock()
		delete(s.pending, taskID)
		s.mu.Unlock()
		return nil, fmt.Errorf("task %s timed out after %ds", taskType, timeoutSecs)
	}
}

// dispatchAsync sends a task and tracks it; result is handled by HandleTaskResult.
func (s *Service) dispatchAsync(pillar, agentID, taskType string, payload []byte, timeoutSecs int32, pt pendingTask) error {
	taskID := "storage:" + id.New()
	pt.taskType = taskType
	pt.resultCh = nil // async: no channel

	s.mu.Lock()
	s.pending[taskID] = pt
	s.mu.Unlock()

	msg := &agentpb.ControllerMessage{
		Payload: &agentpb.ControllerMessage_TaskAssignment{
			TaskAssignment: &agentpb.TaskAssignment{
				TaskId:         taskID,
				TaskType:       taskType,
				Payload:        payload,
				TimeoutSeconds: timeoutSecs,
			},
		},
	}
	if !s.registry.Send(pillar, agentID, msg) {
		s.mu.Lock()
		delete(s.pending, taskID)
		s.mu.Unlock()
		return fmt.Errorf("agent %s/%s not available", pillar, agentID)
	}
	return nil
}

// ─── Agent selection ──────────────────────────────────────────────────────────

func (s *Service) pickStorageAgent() (*registry.AgentRecord, error) {
	agents := s.registry.ListByPillar("storage")
	if len(agents) == 0 {
		return nil, fmt.Errorf("no storage agents available")
	}
	// Pick first agent with lvm backend; fall back to any storage agent
	for _, a := range agents {
		for _, b := range a.Capabilities.StorageBackends {
			if b == "lvm" {
				return a, nil
			}
		}
	}
	return agents[0], nil
}

// pickStorageAgentForVolume returns the specific agent that holds the volume's
// LV (stored as vol.AgentID). Falls back to pickStorageAgent() for volumes
// created before AgentID tracking was introduced.
func (s *Service) pickStorageAgentForVolume(vol *Volume) (*registry.AgentRecord, error) {
	if vol.AgentID != "" {
		agents := s.registry.ListByPillar("storage")
		for _, a := range agents {
			if a.AgentID == vol.AgentID {
				return a, nil
			}
		}
		return nil, fmt.Errorf("storage agent %q for volume %s is not connected", vol.AgentID, vol.ID)
	}
	// Legacy volume: no AgentID recorded — fall back to any agent.
	return s.pickStorageAgent()
}

func (s *Service) pickComputeAgent(nodeID string) (*registry.AgentRecord, error) {
	agents := s.registry.ListByPillar("compute")
	for _, a := range agents {
		if a.AgentID == nodeID {
			return a, nil
		}
	}
	return nil, fmt.Errorf("compute agent %q not connected", nodeID)
}

// ─── Volume CRUD ─────────────────────────────────────────────────────────────

func (s *Service) CreateVolume(ctx context.Context, projectID string, req CreateVolumeRequest) (*Volume, error) {
	if req.SizeGB <= 0 {
		return nil, fmt.Errorf("size_gb must be > 0")
	}
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	agent, err := s.pickStorageAgent()
	if err != nil {
		return nil, err
	}

	vol := &Volume{
		ID:           id.New(),
		ProjectID:    projectID,
		Name:         req.Name,
		Description:  req.Description,
		SizeGB:       req.SizeGB,
		VolumeTypeID: req.VolumeTypeID,
		SnapshotID:   req.SnapshotID,
		Bootable:     req.ImageID != "",
		ImageID:      req.ImageID,
		QoS:          req.QoS,
		AgentID:      agent.AgentID,
		Status:       VolumeStatusCreating,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	if err := s.saveVolume(ctx, vol); err != nil {
		return nil, err
	}

	// Resolve image URL from etcd if this is a bootable volume.
	var imageURL string
	if req.ImageID != "" {
		imageURL, err = s.getImageURL(ctx, req.ImageID)
		if err != nil {
			vol.Status = VolumeStatusError
			s.saveVolume(ctx, vol) //nolint:errcheck
			return vol, nil
		}
	}

	// Timeout is longer when image population is required (qemu-img convert can take minutes).
	timeout := int32(120)
	if imageURL != "" {
		timeout = 600
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"volume_id":   vol.ID,
		"name":        vol.Name,
		"size_gb":     vol.SizeGB,
		"snapshot_id": vol.SnapshotID,
		"image_url":   imageURL,
	})

	if err := s.dispatchAsync("storage", agent.AgentID, "volume.create", payload, timeout, pendingTask{
		volumeID:  vol.ID,
		projectID: projectID,
	}); err != nil {
		vol.Status = VolumeStatusError
		s.saveVolume(ctx, vol) //nolint:errcheck
		return vol, nil        // return the record; caller can see error status
	}

	return vol, nil
}

func (s *Service) GetVolume(ctx context.Context, projectID, volumeID string) (*Volume, error) {
	return s.getVolume(ctx, projectID, volumeID)
}

func (s *Service) ListVolumes(ctx context.Context, projectID string) ([]*Volume, error) {
	vals, err := s.store.GetPrefix(ctx, volumeKeyPrefix+projectID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]*Volume, 0)
	for _, v := range vals {
		var vol Volume
		if err := json.Unmarshal([]byte(v), &vol); err == nil {
			out = append(out, &vol)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Service) DeleteVolume(ctx context.Context, projectID, volumeID string) error {
	vol, err := s.getVolume(ctx, projectID, volumeID)
	if err != nil {
		return err
	}
	switch vol.Status {
	case VolumeStatusInUse:
		// Allow deletion if the attached instance no longer exists in etcd.
		if vol.AttachedTo == "" || s.instanceExists(ctx, vol.AttachedTo) {
			return fmt.Errorf("volume is in use; detach before deleting")
		}
		// Instance is gone — release the stale attachment and proceed.
		vol.AttachedTo = ""
		vol.AttachedHost = ""
		vol.DevicePath = ""
		vol.HostDevPath = ""
		vol.ISCSIIQN = ""
		vol.ISCSIPortal = ""
		vol.ISCSITID = 0
		vol.UpdatedAt = time.Now().UTC()
	case VolumeStatusDeleting:
		// A previous delete attempt is stuck — allow retry.
	case VolumeStatusError:
		// Create or prior delete failed — allow delete to clean up.
	}

	agent, err := s.pickStorageAgentForVolume(vol)
	if err != nil {
		return err
	}

	vol.Status = VolumeStatusDeleting
	if err := s.saveVolume(ctx, vol); err != nil {
		return err
	}

	payload, _ := json.Marshal(map[string]string{"volume_id": vol.ID})
	return s.dispatchAsync("storage", agent.AgentID, "volume.delete", payload, 60, pendingTask{
		volumeID:  vol.ID,
		projectID: projectID,
	})
}

// VolumeAction handles attach / detach / extend / set-qos.
func (s *Service) VolumeAction(ctx context.Context, projectID, volumeID string, req VolumeActionRequest) (*Volume, error) {
	switch req.Action {
	case "attach":
		return s.attachVolume(ctx, projectID, volumeID, req)
	case "detach":
		return s.detachVolume(ctx, projectID, volumeID)
	case "extend":
		return s.extendVolume(ctx, projectID, volumeID, req.NewSizeGB)
	case "set-qos":
		return s.setVolumeQoS(ctx, projectID, volumeID, req.QoS)
	default:
		return nil, fmt.Errorf("unknown action %q; valid: attach, detach, extend, set-qos", req.Action)
	}
}

func (s *Service) attachVolume(ctx context.Context, projectID, volumeID string, req VolumeActionRequest) (*Volume, error) {
	vol, err := s.getVolume(ctx, projectID, volumeID)
	if err != nil {
		return nil, err
	}
	if vol.Status != VolumeStatusAvailable {
		return nil, fmt.Errorf("volume must be in 'available' state to attach (current: %s)", vol.Status)
	}
	if req.InstanceID == "" {
		return nil, fmt.Errorf("instance_id required for attach")
	}
	devicePath := req.DevicePath
	if devicePath == "" {
		devicePath = "/dev/vdb" // default guest target
	}

	// Find which node the instance is on (read from etcd directly)
	nodeID, err := s.getInstanceNodeID(ctx, projectID, req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("instance not found: %w", err)
	}

	storageAgent, err := s.pickStorageAgentForVolume(vol)
	if err != nil {
		return nil, err
	}
	computeAgent, err := s.pickComputeAgent(nodeID)
	if err != nil {
		return nil, err
	}

	// Allocate iSCSI TID
	tid, err := s.store.IncrCounter(ctx, tidCounterKey, 0)
	if err != nil {
		return nil, fmt.Errorf("allocate iSCSI TID: %w", err)
	}
	iqn := fmt.Sprintf("iqn.2026-04.io.pulsar:volume.%s", vol.ID)

	// Step 1: export iSCSI target from storage agent
	exportPayload, _ := json.Marshal(map[string]interface{}{
		"volume_id": vol.ID,
		"tid":       tid,
		"iqn":       iqn,
	})
	exportResult, err := s.dispatch(ctx, "storage", storageAgent.AgentID, "volume.iscsi.export", exportPayload, 60)
	if err != nil {
		return nil, fmt.Errorf("iSCSI export: %w", err)
	}
	var exportRes struct {
		Portal string `json:"portal"`
	}
	if err := json.Unmarshal(exportResult, &exportRes); err != nil {
		return nil, fmt.Errorf("parse iSCSI export result: %w", err)
	}

	// Step 2: connect iSCSI + hot-attach on compute agent
	attachPayload, _ := json.Marshal(map[string]interface{}{
		"volume_id":   vol.ID,
		"instance_id": req.InstanceID,
		"iqn":         iqn,
		"portal":      exportRes.Portal,
		"device_path": devicePath,
	})
	attachResult, err := s.dispatch(ctx, "compute", computeAgent.AgentID, "volume.attach", attachPayload, 120)
	if err != nil {
		// Best-effort unexport
		unexportPayload, _ := json.Marshal(map[string]interface{}{"tid": tid})
		s.dispatch(ctx, "storage", storageAgent.AgentID, "volume.iscsi.unexport", unexportPayload, 30) //nolint:errcheck
		return nil, fmt.Errorf("volume attach on compute: %w", err)
	}
	var attachRes struct {
		HostDevPath string `json:"host_dev_path"`
	}
	json.Unmarshal(attachResult, &attachRes) //nolint:errcheck

	vol.Status = VolumeStatusInUse
	vol.AttachedTo = req.InstanceID
	vol.AttachedHost = nodeID
	vol.DevicePath = devicePath
	vol.HostDevPath = attachRes.HostDevPath
	vol.ISCSIIQN = iqn
	vol.ISCSIPortal = exportRes.Portal
	vol.ISCSITID = int(tid)
	vol.UpdatedAt = time.Now().UTC()
	if err := s.saveVolume(ctx, vol); err != nil {
		return nil, err
	}

	// Apply QoS if specified on the volume (best-effort; don't fail the attach).
	if vol.QoS != nil {
		if err := s.dispatchQoS(ctx, computeAgent.AgentID, req.InstanceID, devicePath, vol.QoS); err != nil {
			s.log.Warn("QoS apply failed after attach (volume is still attached)",
				zap.String("volume_id", vol.ID), zap.Error(err))
		}
	}

	return vol, nil
}

func (s *Service) detachVolume(ctx context.Context, projectID, volumeID string) (*Volume, error) {
	vol, err := s.getVolume(ctx, projectID, volumeID)
	if err != nil {
		return nil, err
	}
	if vol.Status != VolumeStatusInUse {
		return nil, fmt.Errorf("volume is not attached (status: %s)", vol.Status)
	}

	// If the attached instance no longer exists, skip the compute-side detach
	// and go straight to iSCSI unexport + state cleanup.
	instanceGone := vol.AttachedTo != "" && !s.instanceExists(ctx, vol.AttachedTo)

	if !instanceGone {
		computeAgent, err := s.pickComputeAgent(vol.AttachedHost)
		if err != nil {
			return nil, err
		}
		detachPayload, _ := json.Marshal(map[string]interface{}{
			"volume_id":   vol.ID,
			"instance_id": vol.AttachedTo,
			"device_path": vol.DevicePath,
			"host_dev":    vol.HostDevPath,
			"iqn":         vol.ISCSIIQN,
			"portal":      vol.ISCSIPortal,
		})
		if _, err := s.dispatch(ctx, "compute", computeAgent.AgentID, "volume.detach", detachPayload, 60); err != nil {
			return nil, fmt.Errorf("volume detach on compute: %w", err)
		}
	}

	// Unexport iSCSI target from storage agent
	storageAgent, err := s.pickStorageAgentForVolume(vol)
	if err != nil {
		return nil, err
	}
	unexportPayload, _ := json.Marshal(map[string]interface{}{"tid": vol.ISCSITID})
	if _, err := s.dispatch(ctx, "storage", storageAgent.AgentID, "volume.iscsi.unexport", unexportPayload, 30); err != nil {
		s.log.Warn("iSCSI unexport failed (continuing)", zap.String("volume_id", vol.ID), zap.Error(err))
	}

	vol.Status = VolumeStatusAvailable
	vol.AttachedTo = ""
	vol.AttachedHost = ""
	vol.DevicePath = ""
	vol.HostDevPath = ""
	vol.ISCSIIQN = ""
	vol.ISCSIPortal = ""
	vol.ISCSITID = 0
	vol.UpdatedAt = time.Now().UTC()
	if err := s.saveVolume(ctx, vol); err != nil {
		return nil, err
	}
	return vol, nil
}

// ExportVolumeISCSI exports the given bootable volume as an iSCSI target and
// marks it in-use against instanceID/attachHost. The compute agent will perform
// the initiator login as part of the instance.create task — there is no separate
// compute-side dispatch here. Returns the updated volume with ISCSIIQN and
// ISCSIPortal populated.
func (s *Service) ExportVolumeISCSI(ctx context.Context, projectID, volumeID, instanceID, attachHost string) (*Volume, error) {
	vol, err := s.getVolume(ctx, projectID, volumeID)
	if err != nil {
		return nil, err
	}
	if vol.Status != VolumeStatusAvailable {
		return nil, fmt.Errorf("boot volume must be in 'available' state (current: %s)", vol.Status)
	}
	if !vol.Bootable {
		return nil, fmt.Errorf("volume %s is not bootable; create it with image_id to use as boot disk", volumeID)
	}

	agent, err := s.pickStorageAgentForVolume(vol)
	if err != nil {
		return nil, err
	}

	tid, err := s.store.IncrCounter(ctx, tidCounterKey, 0)
	if err != nil {
		return nil, fmt.Errorf("allocate iSCSI TID: %w", err)
	}
	iqn := fmt.Sprintf("iqn.2026-04.io.pulsar:volume.%s", vol.ID)

	exportPayload, _ := json.Marshal(map[string]interface{}{
		"volume_id": vol.ID,
		"tid":       tid,
		"iqn":       iqn,
	})
	exportResult, err := s.dispatch(ctx, "storage", agent.AgentID, "volume.iscsi.export", exportPayload, 60)
	if err != nil {
		return nil, fmt.Errorf("iSCSI export for boot volume: %w", err)
	}
	var exportRes struct {
		Portal string `json:"portal"`
	}
	if err := json.Unmarshal(exportResult, &exportRes); err != nil {
		return nil, fmt.Errorf("parse iSCSI export result: %w", err)
	}

	vol.Status = VolumeStatusInUse
	vol.AttachedTo = instanceID
	vol.AttachedHost = attachHost
	vol.ISCSIIQN = iqn
	vol.ISCSIPortal = exportRes.Portal
	vol.ISCSITID = int(tid)
	vol.UpdatedAt = time.Now().UTC()
	if err := s.saveVolume(ctx, vol); err != nil {
		return nil, err
	}
	return vol, nil
}

// ReleaseVolumesByInstance resets all volumes attached to instanceID back to
// "available" without sending an agent task. Used during instance deletion so
// volumes don't remain permanently stuck in "in-use".
// For volumes that still have an active iSCSI target (ISCSITID > 0) a best-effort
// unexport is dispatched to the storage agent (e.g. boot-from-volume cleanup).
func (s *Service) ReleaseVolumesByInstance(ctx context.Context, instanceID string) {
	vals, err := s.store.GetPrefix(ctx, volumeKeyPrefix)
	if err != nil {
		return
	}
	for _, v := range vals {
		var vol Volume
		if err := json.Unmarshal([]byte(v), &vol); err != nil || vol.AttachedTo != instanceID {
			continue
		}
		// Best-effort unexport for boot volumes (HostDevPath is empty for boot volumes
		// since the iSCSI login was done inside the VM, not via the regular attach path).
		if vol.ISCSITID > 0 && vol.HostDevPath == "" {
			if storageAgent, err := s.pickStorageAgentForVolume(&vol); err == nil {
				payload, _ := json.Marshal(map[string]interface{}{"tid": vol.ISCSITID})
				s.dispatchAsync("storage", storageAgent.AgentID, "volume.iscsi.unexport", payload, 30, pendingTask{}) //nolint:errcheck
			}
		}
		vol.Status = VolumeStatusAvailable
		vol.AttachedTo = ""
		vol.AttachedHost = ""
		vol.DevicePath = ""
		vol.HostDevPath = ""
		vol.ISCSIIQN = ""
		vol.ISCSIPortal = ""
		vol.ISCSITID = 0
		vol.UpdatedAt = time.Now().UTC()
		s.saveVolume(ctx, &vol) //nolint:errcheck
	}
}

// setVolumeQoS stores QoS on the volume and, if the volume is currently in use,
// dispatches a volume.qos.set task to the attached compute agent.
func (s *Service) setVolumeQoS(ctx context.Context, projectID, volumeID string, qos *QoSSpec) (*Volume, error) {
	vol, err := s.getVolume(ctx, projectID, volumeID)
	if err != nil {
		return nil, err
	}

	vol.QoS = qos
	vol.UpdatedAt = time.Now().UTC()
	if err := s.saveVolume(ctx, vol); err != nil {
		return nil, err
	}

	// If the volume is attached, apply QoS live.
	if vol.Status == VolumeStatusInUse && vol.AttachedHost != "" && qos != nil {
		computeAgent, err := s.pickComputeAgent(vol.AttachedHost)
		if err != nil {
			return vol, nil // saved, but can't apply live
		}
		if err := s.dispatchQoS(ctx, computeAgent.AgentID, vol.AttachedTo, vol.DevicePath, qos); err != nil {
			s.log.Warn("QoS apply failed (saved to volume, will apply on next attach)",
				zap.String("volume_id", vol.ID), zap.Error(err))
		}
	}

	return vol, nil
}

// dispatchQoS sends a volume.qos.set task to the compute agent for the given
// instance/device. devicePath is the guest device path (e.g. /dev/vdb).
func (s *Service) dispatchQoS(ctx context.Context, agentID, instanceID, devicePath string, qos *QoSSpec) error {
	if qos == nil {
		return nil
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"instance_id":     instanceID,
		"device":          devicePath,
		"read_iops_sec":   qos.ReadIOPSSec,
		"write_iops_sec":  qos.WriteIOPSSec,
		"total_iops_sec":  qos.TotalIOPSSec,
		"read_bytes_sec":  qos.ReadBytesSec,
		"write_bytes_sec": qos.WriteBytesSec,
		"total_bytes_sec": qos.TotalBytesSec,
	})
	_, err := s.dispatch(ctx, "compute", agentID, "volume.qos.set", payload, 30)
	return err
}

func (s *Service) extendVolume(ctx context.Context, projectID, volumeID string, newSizeGB int) (*Volume, error) {
	vol, err := s.getVolume(ctx, projectID, volumeID)
	if err != nil {
		return nil, err
	}
	if newSizeGB <= vol.SizeGB {
		return nil, fmt.Errorf("new_size_gb (%d) must be larger than current size (%d)", newSizeGB, vol.SizeGB)
	}
	if vol.Status == VolumeStatusCreating || vol.Status == VolumeStatusDeleting {
		return nil, fmt.Errorf("cannot extend volume in status %q", vol.Status)
	}

	agent, err := s.pickStorageAgentForVolume(vol)
	if err != nil {
		return nil, err
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"volume_id":   vol.ID,
		"new_size_gb": newSizeGB,
	})
	if _, err := s.dispatch(ctx, "storage", agent.AgentID, "volume.extend", payload, 60); err != nil {
		return nil, fmt.Errorf("extend volume: %w", err)
	}

	vol.SizeGB = newSizeGB
	vol.UpdatedAt = time.Now().UTC()
	if err := s.saveVolume(ctx, vol); err != nil {
		return nil, err
	}
	return vol, nil
}

// ─── Snapshots ────────────────────────────────────────────────────────────────

func (s *Service) CreateSnapshot(ctx context.Context, projectID string, req CreateSnapshotRequest) (*Snapshot, error) {
	vol, err := s.getVolume(ctx, projectID, req.VolumeID)
	if err != nil {
		return nil, fmt.Errorf("volume not found: %w", err)
	}
	if vol.Status == VolumeStatusCreating || vol.Status == VolumeStatusDeleting || vol.Status == VolumeStatusError {
		return nil, fmt.Errorf("cannot snapshot volume in status %q", vol.Status)
	}

	agent, err := s.pickStorageAgent()
	if err != nil {
		return nil, err
	}

	snap := &Snapshot{
		ID:          id.New(),
		ProjectID:   projectID,
		VolumeID:    req.VolumeID,
		Name:        req.Name,
		Description: req.Description,
		SizeGB:      vol.SizeGB,
		Status:      SnapshotStatusCreating,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := s.saveSnapshot(ctx, snap); err != nil {
		return nil, err
	}

	payload, _ := json.Marshal(map[string]string{
		"snapshot_id": snap.ID,
		"volume_id":   vol.ID,
		"name":        snap.Name,
	})
	return snap, s.dispatchAsync("storage", agent.AgentID, "volume.snapshot.create", payload, 120, pendingTask{
		volumeID:  vol.ID,
		projectID: projectID,
	})
}

func (s *Service) GetSnapshot(ctx context.Context, projectID, snapshotID string) (*Snapshot, error) {
	val, err := s.store.Get(ctx, snapshotKeyPrefix+projectID+"/"+snapshotID)
	if err != nil {
		return nil, err
	}
	if val == "" {
		return nil, fmt.Errorf("snapshot %s not found", snapshotID)
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(val), &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (s *Service) ListSnapshots(ctx context.Context, projectID string) ([]*Snapshot, error) {
	vals, err := s.store.GetPrefix(ctx, snapshotKeyPrefix+projectID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]*Snapshot, 0)
	for _, v := range vals {
		var snap Snapshot
		if err := json.Unmarshal([]byte(v), &snap); err == nil {
			out = append(out, &snap)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) DeleteSnapshot(ctx context.Context, projectID, snapshotID string) error {
	snap, err := s.GetSnapshot(ctx, projectID, snapshotID)
	if err != nil {
		return err
	}

	agent, err := s.pickStorageAgent()
	if err != nil {
		return err
	}

	snap.Status = SnapshotStatusDeleting
	s.saveSnapshot(ctx, snap) //nolint:errcheck

	payload, _ := json.Marshal(map[string]string{"snapshot_id": snap.ID, "volume_id": snap.VolumeID})
	_, err = s.dispatch(ctx, "storage", agent.AgentID, "volume.snapshot.delete", payload, 60)
	if err != nil {
		snap.Status = SnapshotStatusError
		s.saveSnapshot(ctx, snap) //nolint:errcheck
		return err
	}
	s.store.Delete(ctx, snapshotKeyPrefix+projectID+"/"+snapshotID) //nolint:errcheck
	return nil
}

// ─── Volume Types ─────────────────────────────────────────────────────────────

func (s *Service) CreateVolumeType(ctx context.Context, req CreateVolumeTypeRequest) (*VolumeType, error) {
	if req.Name == "" || req.Driver == "" {
		return nil, fmt.Errorf("name and driver are required")
	}
	vt := &VolumeType{
		ID:         id.New(),
		Name:       req.Name,
		Driver:     req.Driver,
		ExtraSpecs: req.ExtraSpecs,
		CreatedAt:  time.Now().UTC(),
	}
	data, err := json.Marshal(vt)
	if err != nil {
		return nil, err
	}
	if err := s.store.Put(ctx, volumeTypeKeyPrefix+vt.ID, string(data)); err != nil {
		return nil, err
	}
	return vt, nil
}

func (s *Service) GetVolumeType(ctx context.Context, vtID string) (*VolumeType, error) {
	val, err := s.store.Get(ctx, volumeTypeKeyPrefix+vtID)
	if err != nil {
		return nil, err
	}
	if val == "" {
		return nil, fmt.Errorf("volume type %s not found", vtID)
	}
	var vt VolumeType
	if err := json.Unmarshal([]byte(val), &vt); err != nil {
		return nil, err
	}
	return &vt, nil
}

func (s *Service) ListVolumeTypes(ctx context.Context) ([]*VolumeType, error) {
	vals, err := s.store.GetPrefix(ctx, volumeTypeKeyPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]*VolumeType, 0)
	for _, v := range vals {
		var vt VolumeType
		if err := json.Unmarshal([]byte(v), &vt); err == nil {
			out = append(out, &vt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) DeleteVolumeType(ctx context.Context, vtID string) error {
	if _, err := s.GetVolumeType(ctx, vtID); err != nil {
		return err
	}
	return s.store.Delete(ctx, volumeTypeKeyPrefix+vtID)
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

func (s *Service) getVolume(ctx context.Context, projectID, volumeID string) (*Volume, error) {
	val, err := s.store.Get(ctx, volumeKeyPrefix+projectID+"/"+volumeID)
	if err != nil {
		return nil, err
	}
	if val == "" {
		return nil, fmt.Errorf("volume %s not found", volumeID)
	}
	var vol Volume
	if err := json.Unmarshal([]byte(val), &vol); err != nil {
		return nil, err
	}
	return &vol, nil
}

// getVolumeByID scans the index then falls back to a full scan.
// instanceExists returns true if any etcd key with the instance prefix exists for this ID.
func (s *Service) instanceExists(ctx context.Context, instanceID string) bool {
	vals, err := s.store.GetPrefix(ctx, "/pulsar/compute/instances/")
	if err != nil {
		return true // assume exists on error to avoid accidental force-delete
	}
	for _, v := range vals {
		var obj struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(v), &obj) == nil && obj.ID == instanceID {
			return true
		}
	}
	return false
}

func (s *Service) getVolumeByID(ctx context.Context, volumeID string) (*Volume, error) {
	if projectID, err := s.store.Get(ctx, volumeIndexKey+volumeID); err == nil && projectID != "" {
		if vol, err := s.getVolume(ctx, projectID, volumeID); err == nil {
			return vol, nil
		}
	}
	// Full scan fallback
	vals, err := s.store.GetPrefix(ctx, volumeKeyPrefix)
	if err != nil {
		return nil, err
	}
	for _, v := range vals {
		var vol Volume
		if err := json.Unmarshal([]byte(v), &vol); err == nil && vol.ID == volumeID {
			s.store.Put(ctx, volumeIndexKey+vol.ID, vol.ProjectID) //nolint:errcheck
			return &vol, nil
		}
	}
	return nil, fmt.Errorf("volume %s not found", volumeID)
}

func (s *Service) saveVolume(ctx context.Context, vol *Volume) error {
	data, err := json.Marshal(vol)
	if err != nil {
		return err
	}
	if err := s.store.Put(ctx, volumeKeyPrefix+vol.ProjectID+"/"+vol.ID, string(data)); err != nil {
		return err
	}
	s.store.Put(ctx, volumeIndexKey+vol.ID, vol.ProjectID) //nolint:errcheck
	return nil
}

func (s *Service) setVolumeStatus(ctx context.Context, vol *Volume, status string) {
	vol.Status = status
	vol.UpdatedAt = time.Now().UTC()
	if err := s.saveVolume(ctx, vol); err != nil {
		s.log.Error("failed to save volume status", zap.String("id", vol.ID), zap.Error(err))
	}
}

func (s *Service) saveSnapshot(ctx context.Context, snap *Snapshot) error {
	snap.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return s.store.Put(ctx, snapshotKeyPrefix+snap.ProjectID+"/"+snap.ID, string(data))
}

func (s *Service) setSnapshotStatus(ctx context.Context, snap *Snapshot, status string) {
	snap.Status = status
	if err := s.saveSnapshot(ctx, snap); err != nil {
		s.log.Error("failed to save snapshot status", zap.String("id", snap.ID), zap.Error(err))
	}
}

// getSnapshotByVolume finds the first snapshot in creating state for a volume (used in async task result).
func (s *Service) getSnapshotByVolume(ctx context.Context, projectID, volumeID string) (*Snapshot, error) {
	snaps, err := s.ListSnapshots(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, snap := range snaps {
		if snap.VolumeID == volumeID && snap.Status == SnapshotStatusCreating {
			return snap, nil
		}
	}
	return nil, fmt.Errorf("no creating snapshot found for volume %s", volumeID)
}

// getImageURL fetches the download URL for an image from etcd.
// Images are stored at /pulsar/images/<id> with a "url" field.
func (s *Service) getImageURL(ctx context.Context, imageID string) (string, error) {
	val, err := s.store.Get(ctx, "/pulsar/images/"+imageID)
	if err != nil || val == "" {
		return "", fmt.Errorf("image %s not found", imageID)
	}
	var img struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(val), &img); err != nil {
		return "", fmt.Errorf("parse image record: %w", err)
	}
	if img.URL == "" {
		return "", fmt.Errorf("image %s has no URL", imageID)
	}
	return img.URL, nil
}

// getInstanceNodeID reads the instance from etcd to find which node it's on.
func (s *Service) getInstanceNodeID(ctx context.Context, projectID, instanceID string) (string, error) {
	val, err := s.store.Get(ctx, "/pulsar/compute/instances/"+projectID+"/"+instanceID)
	if err != nil || val == "" {
		// Try all projects (cross-project attach not typical but handle gracefully)
		vals, scanErr := s.store.GetPrefix(ctx, "/pulsar/compute/instances/")
		if scanErr != nil {
			return "", fmt.Errorf("instance %s not found", instanceID)
		}
		for _, v := range vals {
			var inst struct {
				ID     string `json:"id"`
				NodeID string `json:"node_id"`
			}
			if json.Unmarshal([]byte(v), &inst) == nil && inst.ID == instanceID {
				return inst.NodeID, nil
			}
		}
		return "", fmt.Errorf("instance %s not found", instanceID)
	}
	var inst struct {
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal([]byte(val), &inst); err != nil {
		return "", err
	}
	if inst.NodeID == "" {
		return "", fmt.Errorf("instance %s has no node_id (not yet scheduled)", instanceID)
	}
	return inst.NodeID, nil
}
