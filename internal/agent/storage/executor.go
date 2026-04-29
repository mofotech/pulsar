package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	"github.com/agomez/pulsar/internal/agent/storage/lvm"
	"github.com/agomez/pulsar/internal/config"
)

// Executor handles storage task assignments dispatched by the controller.
type Executor struct {
	lvm *lvm.Driver
	log *zap.Logger
}

func NewExecutor(cfg config.StorageAgent, log *zap.Logger) (*Executor, error) {
	lvmDrv, err := lvm.New(
		cfg.Backends.LVM.VolumeGroup,
		cfg.Backends.LVM.ThinPool,
		cfg.NodeIP,
		log,
	)
	if err != nil {
		return nil, fmt.Errorf("lvm driver: %w", err)
	}
	return &Executor{lvm: lvmDrv, log: log}, nil
}

// AdvertisedCapabilities returns capabilities for the heartbeat.
func (e *Executor) AdvertisedCapabilities() agentpb.AgentCapabilities {
	return agentpb.AgentCapabilities{
		StorageBackends: []string{"lvm"},
	}
}

// Execute dispatches a storage task and returns the result.
func (e *Executor) Execute(ctx context.Context, task *agentpb.TaskAssignment) *agentpb.TaskResult {
	result := &agentpb.TaskResult{TaskId: task.TaskId}

	var (
		resultData []byte
		err        error
	)

	switch task.TaskType {
	case "volume.create":
		err = e.createVolume(ctx, task.Payload)
	case "volume.delete":
		err = e.deleteVolume(ctx, task.Payload)
	case "volume.extend":
		err = e.extendVolume(ctx, task.Payload)
	case "volume.iscsi.export":
		resultData, err = e.exportISCSI(ctx, task.Payload)
	case "volume.iscsi.unexport":
		err = e.unexportISCSI(ctx, task.Payload)
	case "volume.snapshot.create":
		err = e.createSnapshot(ctx, task.Payload)
	case "volume.snapshot.delete":
		err = e.deleteSnapshot(ctx, task.Payload)
	case "storage.volume.list":
		resultData, err = e.listVolumes(ctx)
	default:
		err = fmt.Errorf("unknown storage task type: %s", task.TaskType)
	}

	if err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
		e.log.Error("storage task failed",
			zap.String("task_id", task.TaskId),
			zap.String("task_type", task.TaskType),
			zap.Error(err),
		)
	} else {
		result.Success = true
		result.Result = resultData
	}
	return result
}

func (e *Executor) createVolume(ctx context.Context, payload []byte) error {
	var task struct {
		VolumeID   string `json:"volume_id"`
		Name       string `json:"name"`
		SizeGB     int    `json:"size_gb"`
		SnapshotID string `json:"snapshot_id"`
		ImageURL   string `json:"image_url"` // non-empty → populate LV from image (bootable volume)
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	e.log.Info("creating LVM volume", zap.String("id", task.VolumeID), zap.Int("size_gb", task.SizeGB))
	if err := e.lvm.CreateVolume(ctx, task.VolumeID, task.SizeGB, task.SnapshotID); err != nil {
		return err
	}
	if task.ImageURL != "" {
		e.log.Info("populating boot volume from image",
			zap.String("volume_id", task.VolumeID),
			zap.String("image_url", task.ImageURL),
		)
		if err := e.lvm.PopulateFromImage(ctx, task.VolumeID, task.ImageURL); err != nil {
			return fmt.Errorf("populate volume from image: %w", err)
		}
	}
	return nil
}

func (e *Executor) deleteVolume(ctx context.Context, payload []byte) error {
	var task struct {
		VolumeID string `json:"volume_id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	e.log.Info("deleting LVM volume", zap.String("id", task.VolumeID))
	err := e.lvm.DeleteVolume(ctx, task.VolumeID)
	// If the LV doesn't exist the volume was never created (failed create) — treat as success.
	if err != nil && (strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "Failed to find")) {
		e.log.Info("LVM volume not found, treating delete as success", zap.String("id", task.VolumeID))
		return nil
	}
	return err
}

func (e *Executor) extendVolume(ctx context.Context, payload []byte) error {
	var task struct {
		VolumeID  string `json:"volume_id"`
		NewSizeGB int    `json:"new_size_gb"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	e.log.Info("extending LVM volume", zap.String("id", task.VolumeID), zap.Int("new_size_gb", task.NewSizeGB))
	return e.lvm.ExtendVolume(ctx, task.VolumeID, task.NewSizeGB)
}

func (e *Executor) exportISCSI(ctx context.Context, payload []byte) ([]byte, error) {
	var task struct {
		VolumeID string `json:"volume_id"`
		TID      int    `json:"tid"`
		IQN      string `json:"iqn"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return nil, err
	}
	e.log.Info("exporting iSCSI target",
		zap.String("volume_id", task.VolumeID),
		zap.String("iqn", task.IQN),
		zap.Int("tid", task.TID),
	)
	portal, err := e.lvm.ExportISCSI(ctx, task.VolumeID, task.TID, task.IQN)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"portal": portal})
}

func (e *Executor) unexportISCSI(ctx context.Context, payload []byte) error {
	var task struct {
		TID int `json:"tid"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	e.log.Info("unexporting iSCSI target", zap.Int("tid", task.TID))
	return e.lvm.UnexportISCSI(ctx, task.TID)
}

func (e *Executor) createSnapshot(ctx context.Context, payload []byte) error {
	var task struct {
		SnapshotID string `json:"snapshot_id"`
		VolumeID   string `json:"volume_id"`
		Name       string `json:"name"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	e.log.Info("creating LVM snapshot",
		zap.String("snapshot_id", task.SnapshotID),
		zap.String("volume_id", task.VolumeID),
	)
	return e.lvm.CreateSnapshot(ctx, task.SnapshotID, task.VolumeID)
}

func (e *Executor) deleteSnapshot(ctx context.Context, payload []byte) error {
	var task struct {
		SnapshotID string `json:"snapshot_id"`
		VolumeID   string `json:"volume_id"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		return err
	}
	e.log.Info("deleting LVM snapshot", zap.String("snapshot_id", task.SnapshotID))
	return e.lvm.DeleteSnapshot(ctx, task.SnapshotID, task.VolumeID)
}

// listVolumes returns a JSON payload enumerating all LVM thin volumes on this agent.
// Response: {"volumes": ["<volumeID>", ...]}
func (e *Executor) listVolumes(ctx context.Context) ([]byte, error) {
	ids, err := e.lvm.ListVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("lvm list volumes: %w", err)
	}
	type response struct {
		Volumes []string `json:"volumes"`
	}
	if ids == nil {
		ids = []string{}
	}
	return json.Marshal(response{Volumes: ids})
}
