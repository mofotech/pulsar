// Package reconciler implements a background garbage-collector that identifies
// and destroys orphaned resources — instances, networks, ports, and volumes that
// exist on the hypervisor / network / storage layer but have no corresponding
// record in the Pulsar etcd state store.
//
// # Architecture
//
// The reconciler runs on a configurable interval (default 5 minutes).  During
// each cycle it queries every live agent for its inventory via dedicated list
// tasks, then diffs the inventory against etcd.  Any resource present on the
// agent but absent from etcd is considered an orphan and is destroyed via the
// same task dispatch pathway used by the normal service layer.
//
// # Task namespacing
//
// The reconciler registers itself under the "reconciler" task-result namespace.
// Task IDs therefore take the form "reconciler:<uuid>".  The registry routes
// incoming results to HandleTaskResult based on the namespace prefix.
package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	"github.com/agomez/pulsar/internal/controller/registry"
	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/pkg/id"
)

// etcd key prefixes — must match the constants in each service package.
const (
	instanceKeyPrefix = "/pulsar/compute/instances/"
	networkKeyPrefix  = "/pulsar/network/networks/"
	portKeyPrefix     = "/pulsar/network/ports/"
	volumeKeyPrefix   = "/pulsar/storage/volumes/"

	defaultInterval = 5 * time.Minute
	taskTimeout     = 30 * time.Second
)

// pendingRec tracks an in-flight list task dispatched by the reconciler.
type pendingRec struct {
	done chan recResult
}

type recResult struct {
	payload []byte
	err     error
}

// Reconciler is a background worker that periodically identifies and removes
// orphaned infrastructure resources.
type Reconciler struct {
	store    *etcd.Client
	registry *registry.AgentRegistry
	log      *zap.Logger
	interval time.Duration

	mu      sync.Mutex
	pending map[string]pendingRec // "reconciler:<uuid>" → waiter
}

// New creates a new Reconciler.  interval controls how often the garbage
// collection sweep runs; pass 0 to use the default (5 minutes).
func New(store *etcd.Client, reg *registry.AgentRegistry, log *zap.Logger, interval time.Duration) *Reconciler {
	if interval <= 0 {
		interval = defaultInterval
	}
	return &Reconciler{
		store:    store,
		registry: reg,
		log:      log,
		interval: interval,
		pending:  make(map[string]pendingRec),
	}
}

// HandleTaskResult is registered with the AgentRegistry for the "reconciler"
// task namespace.  It wakes any goroutine blocked on dispatchAndWait.
func (r *Reconciler) HandleTaskResult(result *agentpb.TaskResult) {
	r.mu.Lock()
	rec, ok := r.pending[result.TaskId]
	if ok {
		delete(r.pending, result.TaskId)
	}
	r.mu.Unlock()

	if !ok {
		return
	}

	if result.Success {
		select {
		case rec.done <- recResult{payload: result.Result}:
		default:
		}
	} else {
		select {
		case rec.done <- recResult{err: fmt.Errorf("%s", result.ErrorMessage)}:
		default:
		}
	}
}

// Start launches the reconciler loop in a background goroutine.  It returns
// immediately; the loop runs until ctx is cancelled.
func (r *Reconciler) Start(ctx context.Context) {
	go func() {
		// Delay the first run slightly so all agents have time to connect.
		select {
		case <-time.After(30 * time.Second):
		case <-ctx.Done():
			return
		}

		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()

		for {
			r.runOnce(ctx)
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
}

// runOnce executes a single reconciliation sweep across all resource types.
func (r *Reconciler) runOnce(ctx context.Context) {
	r.log.Info("reconciler: starting sweep")
	start := time.Now()

	if err := r.reconcileInstances(ctx); err != nil {
		r.log.Error("reconciler: instances sweep failed", zap.Error(err))
	}
	if err := r.reconcileNetworks(ctx); err != nil {
		r.log.Error("reconciler: networks sweep failed", zap.Error(err))
	}
	if err := r.reconcilePorts(ctx); err != nil {
		r.log.Error("reconciler: ports sweep failed", zap.Error(err))
	}
	if err := r.reconcileVolumes(ctx); err != nil {
		r.log.Error("reconciler: volumes sweep failed", zap.Error(err))
	}

	r.log.Info("reconciler: sweep complete", zap.Duration("elapsed", time.Since(start)))
}

// ─── Instances ────────────────────────────────────────────────────────────────

func (r *Reconciler) reconcileInstances(ctx context.Context) error {
	// Build a set of all instance IDs known to etcd (across all projects).
	etcdVals, err := r.store.GetPrefix(ctx, instanceKeyPrefix)
	if err != nil {
		return fmt.Errorf("etcd GetPrefix instances: %w", err)
	}
	known := make(map[string]bool, len(etcdVals))
	for _, raw := range etcdVals {
		var inst struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(raw), &inst) == nil && inst.ID != "" {
			known[inst.ID] = true
		}
	}

	agents := r.registry.ListByPillar("compute")
	if len(agents) == 0 {
		return nil // no agents online — nothing to check
	}

	type instanceEntry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type listResponse struct {
		Instances []instanceEntry `json:"instances"`
	}

	for _, agent := range agents {
		raw, err := r.dispatchAndWait(ctx, "compute", agent.AgentID, "instance.list", nil)
		if err != nil {
			r.log.Warn("reconciler: instance.list failed",
				zap.String("agent", agent.AgentID),
				zap.Error(err),
			)
			continue
		}

		var resp listResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			r.log.Warn("reconciler: bad instance.list response",
				zap.String("agent", agent.AgentID),
				zap.Error(err),
			)
			continue
		}

		for _, inst := range resp.Instances {
			if known[inst.ID] {
				continue
			}
			r.log.Warn("reconciler: orphaned instance detected — deleting",
				zap.String("instance_id", inst.ID),
				zap.String("agent", agent.AgentID),
			)
			payload, _ := json.Marshal(map[string]string{"id": inst.ID})
			deleteCtx, cancel := context.WithTimeout(ctx, taskTimeout)
			if _, err := r.dispatchAndWait(deleteCtx, "compute", agent.AgentID, "instance.delete", payload); err != nil {
				r.log.Error("reconciler: instance.delete failed",
					zap.String("instance_id", inst.ID),
					zap.Error(err),
				)
			}
			cancel()
		}
	}
	return nil
}

// ─── Networks ─────────────────────────────────────────────────────────────────

func (r *Reconciler) reconcileNetworks(ctx context.Context) error {
	etcdVals, err := r.store.GetPrefix(ctx, networkKeyPrefix)
	if err != nil {
		return fmt.Errorf("etcd GetPrefix networks: %w", err)
	}
	known := make(map[string]bool, len(etcdVals))
	for _, raw := range etcdVals {
		var n struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(raw), &n) == nil && n.ID != "" {
			known[n.ID] = true
		}
	}

	agents := r.registry.ListByPillar("network")
	if len(agents) == 0 {
		return nil
	}

	type listResponse struct {
		Switches []string `json:"switches"`
	}

	for _, agent := range agents {
		raw, err := r.dispatchAndWait(ctx, "network", agent.AgentID, "network.list", nil)
		if err != nil {
			r.log.Warn("reconciler: network.list failed",
				zap.String("agent", agent.AgentID),
				zap.Error(err),
			)
			continue
		}

		var resp listResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			r.log.Warn("reconciler: bad network.list response",
				zap.String("agent", agent.AgentID),
				zap.Error(err),
			)
			continue
		}

		for _, switchID := range resp.Switches {
			if known[switchID] {
				continue
			}
			r.log.Warn("reconciler: orphaned OVN logical switch detected — deleting",
				zap.String("network_id", switchID),
				zap.String("agent", agent.AgentID),
			)
			payload, _ := json.Marshal(map[string]string{"id": switchID})
			deleteCtx, cancel := context.WithTimeout(ctx, taskTimeout)
			if _, err := r.dispatchAndWait(deleteCtx, "network", agent.AgentID, "network.delete", payload); err != nil {
				r.log.Error("reconciler: network.delete failed",
					zap.String("network_id", switchID),
					zap.Error(err),
				)
			}
			cancel()
		}
	}
	return nil
}

// ─── Ports ────────────────────────────────────────────────────────────────────

func (r *Reconciler) reconcilePorts(ctx context.Context) error {
	etcdVals, err := r.store.GetPrefix(ctx, portKeyPrefix)
	if err != nil {
		return fmt.Errorf("etcd GetPrefix ports: %w", err)
	}
	known := make(map[string]bool, len(etcdVals))
	for _, raw := range etcdVals {
		var p struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(raw), &p) == nil && p.ID != "" {
			known[p.ID] = true
		}
	}

	agents := r.registry.ListByPillar("network")
	if len(agents) == 0 {
		return nil
	}

	type listResponse struct {
		Ports []string `json:"ports"`
	}

	for _, agent := range agents {
		raw, err := r.dispatchAndWait(ctx, "network", agent.AgentID, "network.list", nil)
		if err != nil {
			// Already logged in reconcileNetworks; skip silently here.
			continue
		}

		var resp listResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			continue
		}

		for _, portID := range resp.Ports {
			// Internal OVN ports (localnet, router-type) are not tracked in etcd.
			// Skip any port name that matches the well-known internal suffixes.
			if isInternalOVNPort(portID) {
				continue
			}
			if known[portID] {
				continue
			}
			r.log.Warn("reconciler: orphaned OVN logical port detected — deleting",
				zap.String("port_id", portID),
				zap.String("agent", agent.AgentID),
			)
			payload, _ := json.Marshal(map[string]string{"port_id": portID})
			deleteCtx, cancel := context.WithTimeout(ctx, taskTimeout)
			if _, err := r.dispatchAndWait(deleteCtx, "network", agent.AgentID, "network.port.delete", payload); err != nil {
				r.log.Error("reconciler: network.port.delete failed",
					zap.String("port_id", portID),
					zap.Error(err),
				)
			}
			cancel()
		}
	}
	return nil
}

// isInternalOVNPort returns true for ports that are created internally by OVN
// or Pulsar as infrastructure (localnet ports, router-facing switch ports, etc.)
// and therefore do not have etcd records.
func isInternalOVNPort(name string) bool {
	suffixes := []string{
		"-localnet", // localnet provider port added by Pulsar on flat/vlan networks
		"-lrp",      // switch-side router port (lsp of type "router")
		"-ext-lsp",  // external gateway switch port
	}
	for _, s := range suffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// ─── Volumes ──────────────────────────────────────────────────────────────────

func (r *Reconciler) reconcileVolumes(ctx context.Context) error {
	// Read all volume records from etcd, keyed by volume ID.
	etcdVals, err := r.store.GetPrefix(ctx, volumeKeyPrefix)
	if err != nil {
		return fmt.Errorf("etcd GetPrefix volumes: %w", err)
	}

	type etcdVolume struct {
		ID         string `json:"id"`
		ProjectID  string `json:"project_id"`
		Name       string `json:"name"`
		SizeGB     int    `json:"size_gb"`
		Status     string `json:"status"`
		AgentID    string `json:"agent_id"`
		Bootable   bool   `json:"bootable"`
		ImageID    string `json:"image_id"`
		SnapshotID string `json:"snapshot_id"`
	}

	knownVolumes := make(map[string]etcdVolume, len(etcdVals))
	for _, raw := range etcdVals {
		var v etcdVolume
		if json.Unmarshal([]byte(raw), &v) == nil && v.ID != "" {
			knownVolumes[v.ID] = v
		}
	}

	agents := r.registry.ListByPillar("storage")
	if len(agents) == 0 {
		return nil
	}

	type listResponse struct {
		Volumes []string `json:"volumes"`
	}

	// presentOnAgent is the union of LV IDs reported by ALL storage agents.
	presentOnAgent := make(map[string]bool)

	for _, agent := range agents {
		raw, err := r.dispatchAndWait(ctx, "storage", agent.AgentID, "storage.volume.list", nil)
		if err != nil {
			r.log.Warn("reconciler: storage.volume.list failed",
				zap.String("agent", agent.AgentID),
				zap.Error(err),
			)
			continue
		}

		var resp listResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			r.log.Warn("reconciler: bad storage.volume.list response",
				zap.String("agent", agent.AgentID),
				zap.Error(err),
			)
			continue
		}

		for _, volID := range resp.Volumes {
			presentOnAgent[volID] = true
		}

		// ── Direction 1: orphaned LV (agent has it, etcd doesn't) ──────────
		for _, volID := range resp.Volumes {
			if _, inEtcd := knownVolumes[volID]; inEtcd {
				continue
			}
			r.log.Warn("reconciler: orphaned LVM volume detected — deleting",
				zap.String("volume_id", volID),
				zap.String("agent", agent.AgentID),
			)
			payload, _ := json.Marshal(map[string]string{"volume_id": volID})
			deleteCtx, cancel := context.WithTimeout(ctx, taskTimeout)
			if _, err := r.dispatchAndWait(deleteCtx, "storage", agent.AgentID, "volume.delete", payload); err != nil {
				r.log.Error("reconciler: volume.delete failed",
					zap.String("volume_id", volID),
					zap.Error(err),
				)
			}
			cancel()
		}
	}

	// ── Direction 2: ghost etcd record (etcd says available/creating, no LV exists) ──
	// This happens when a volume.create task fails mid-flight (e.g. agent restart,
	// container crash) before the result can be reported back to the controller.
	// The controller never receives the failure, so the status stays "available".
	// We detect this by cross-referencing etcd against the union of all agents' LV lists.
	for _, vol := range knownVolumes {
		if vol.Status != "available" && vol.Status != "creating" {
			continue // error / in-use / deleting volumes are handled elsewhere
		}
		if presentOnAgent[vol.ID] {
			continue // LV confirmed present
		}

		r.log.Warn("reconciler: volume in etcd has no backing LV — re-dispatching volume.create",
			zap.String("volume_id", vol.ID),
			zap.String("name", vol.Name),
			zap.String("status", vol.Status),
		)

		// Target the specific agent that originally held the LV (vol.AgentID).
		// Fall back to any available agent for volumes created before AgentID tracking.
		var targetAgentID string
		if vol.AgentID != "" {
			targetAgentID = vol.AgentID
		} else if len(agents) > 0 {
			targetAgentID = agents[0].AgentID
		} else {
			continue
		}

		payload, _ := json.Marshal(map[string]interface{}{
			"volume_id":   vol.ID,
			"name":        vol.Name,
			"size_gb":     vol.SizeGB,
			"snapshot_id": vol.SnapshotID,
			// image_url intentionally omitted: image population is a one-shot
			// operation; re-dispatching with the URL would re-download and
			// overwrite. Boot volumes that lost their LV need manual recovery.
		})
		createCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if _, err := r.dispatchAndWait(createCtx, "storage", targetAgentID, "volume.create", payload); err != nil {
			r.log.Error("reconciler: volume.create (re-dispatch) failed",
				zap.String("volume_id", vol.ID),
				zap.String("agent", targetAgentID),
				zap.Error(err),
			)
			// Mark as error in etcd so the user can see it needs attention.
			r.markVolumeError(ctx, vol.ProjectID, vol.ID)
		} else {
			r.log.Info("reconciler: volume.create re-dispatch succeeded",
				zap.String("volume_id", vol.ID),
			)
		}
		cancel()
	}

	return nil
}

// ─── Dispatch helper ──────────────────────────────────────────────────────────

// dispatchAndWait sends taskType with rawPayload to a specific agent and
// blocks until the result arrives or ctx is cancelled.
//
// If rawPayload is nil an empty JSON object is sent.
func (r *Reconciler) dispatchAndWait(ctx context.Context, pillar, agentID, taskType string, rawPayload []byte) ([]byte, error) {
	if rawPayload == nil {
		rawPayload = []byte("{}")
	}

	taskID := "reconciler:" + id.New()
	done := make(chan recResult, 1)

	r.mu.Lock()
	r.pending[taskID] = pendingRec{done: done}
	r.mu.Unlock()

	msg := &agentpb.ControllerMessage{
		Payload: &agentpb.ControllerMessage_TaskAssignment{
			TaskAssignment: &agentpb.TaskAssignment{
				TaskId:         taskID,
				TaskType:       taskType,
				Payload:        rawPayload,
				TimeoutSeconds: int32(taskTimeout.Seconds()),
			},
		},
	}

	if !r.registry.Send(pillar, agentID, msg) {
		r.mu.Lock()
		delete(r.pending, taskID)
		r.mu.Unlock()
		return nil, fmt.Errorf("agent %s/%s not available", pillar, agentID)
	}

	select {
	case res := <-done:
		return res.payload, res.err
	case <-ctx.Done():
		r.mu.Lock()
		delete(r.pending, taskID)
		r.mu.Unlock()
		return nil, ctx.Err()
	}
}

// markVolumeError patches the volume's status to "error" in etcd.
// It reads the existing record, updates only the status field, and writes it back.
func (r *Reconciler) markVolumeError(ctx context.Context, projectID, volumeID string) {
	key := volumeKeyPrefix + projectID + "/" + volumeID
	existing, err := r.store.Get(ctx, key)
	if err != nil || existing == "" {
		return
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(existing), &raw); err != nil {
		return
	}
	raw["status"] = "error"
	updated, err := json.Marshal(raw)
	if err != nil {
		return
	}
	if err := r.store.Put(ctx, key, string(updated)); err != nil {
		r.log.Error("reconciler: failed to mark volume as error",
			zap.String("volume_id", volumeID),
			zap.Error(err),
		)
	}
}
