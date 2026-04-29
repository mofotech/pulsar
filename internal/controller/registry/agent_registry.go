package registry

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	"github.com/agomez/pulsar/internal/store/etcd"
)

const (
	agentLeaseTTL   = 30               // seconds
	agentStaleAfter = 90 * time.Second // agent must miss ~3 heartbeat cycles before declared lost
	cleanupInterval = 15 * time.Second
	startupGrace    = 90 * time.Second // sweepStale won't evict within this window of controller start
)

// AgentRecord holds live state for a connected agent node.
type AgentRecord struct {
	AgentID      string
	Pillar       string
	Capabilities agentpb.AgentCapabilities
	Resources    agentpb.ResourceUsage
	LastSeen     time.Time
	sendCh       chan *agentpb.ControllerMessage
}

// TaskResultHandler is called whenever an agent sends back a task result.
type TaskResultHandler func(result *agentpb.TaskResult)

// AgentLostHandler is called when an agent is removed as stale.
type AgentLostHandler func(rec *AgentRecord)

// AgentReconnectHandler is called when a previously-absent agent sends its first heartbeat.
type AgentReconnectHandler func(agentID string)

// AgentRegistry tracks connected agents and implements the gRPC AgentGateway service.
type AgentRegistry struct {
	agentpb.UnimplementedAgentGatewayServer

	mu                    sync.RWMutex
	agents                map[string]*AgentRecord // keyed by "pillar:agentID"
	taskResultHandlers    map[string]TaskResultHandler
	agentLostHandlers     map[string]AgentLostHandler      // keyed by pillar
	agentReconnectHandler map[string]AgentReconnectHandler // keyed by pillar
	etcd                  *etcd.Client
	log                   *zap.Logger
	startedAt             time.Time
}

func NewAgentRegistry(etcd *etcd.Client, log *zap.Logger) *AgentRegistry {
	return &AgentRegistry{
		agents:                make(map[string]*AgentRecord),
		taskResultHandlers:    make(map[string]TaskResultHandler),
		agentLostHandlers:     make(map[string]AgentLostHandler),
		agentReconnectHandler: make(map[string]AgentReconnectHandler),
		etcd:                  etcd,
		log:                   log,
		startedAt:             time.Now(),
	}
}

// RegisterAgentLostHandler registers a callback invoked when an agent for the
// given pillar is removed due to a missed heartbeat timeout.
func (r *AgentRegistry) RegisterAgentLostHandler(pillar string, fn AgentLostHandler) {
	r.mu.Lock()
	r.agentLostHandlers[pillar] = fn
	r.mu.Unlock()
}

// RegisterAgentReconnectHandler registers a callback invoked when an agent for
// the given pillar connects for the first time (or reconnects after being evicted).
func (r *AgentRegistry) RegisterAgentReconnectHandler(pillar string, fn AgentReconnectHandler) {
	r.mu.Lock()
	r.agentReconnectHandler[pillar] = fn
	r.mu.Unlock()
}

// StartCleanup runs a background goroutine that evicts agents that have not
// sent a heartbeat within agentStaleAfter and notifies registered handlers.
func (r *AgentRegistry) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.sweepStale()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (r *AgentRegistry) sweepStale() {
	// Don't evict agents during the startup grace window — they may still be
	// reconnecting after a controller restart.
	if time.Since(r.startedAt) < startupGrace {
		return
	}

	cutoff := time.Now().Add(-agentStaleAfter)

	r.mu.Lock()
	var stale []*AgentRecord
	for key, rec := range r.agents {
		if rec.LastSeen.Before(cutoff) {
			stale = append(stale, rec)
			delete(r.agents, key)
			r.log.Warn("evicting stale agent",
				zap.String("agent_id", rec.AgentID),
				zap.String("pillar", rec.Pillar),
				zap.Duration("silent_for", time.Since(rec.LastSeen)),
			)
		}
	}
	// Snapshot handlers while holding the lock, then release before calling them.
	handlers := make(map[string]AgentLostHandler, len(r.agentLostHandlers))
	for k, v := range r.agentLostHandlers {
		handlers[k] = v
	}
	r.mu.Unlock()

	for _, rec := range stale {
		if fn, ok := handlers[rec.Pillar]; ok {
			go fn(rec)
		}
	}
}

// ListAll returns all currently registered agents across every pillar.
func (r *AgentRegistry) ListAll() []*AgentRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*AgentRecord, 0, len(r.agents))
	for _, rec := range r.agents {
		out = append(out, rec)
	}
	return out
}

// RegisterTaskResultHandler registers a callback for tasks with the given namespace prefix.
func (r *AgentRegistry) RegisterTaskResultHandler(namespace string, fn TaskResultHandler) {
	r.mu.Lock()
	r.taskResultHandlers[namespace] = fn
	r.mu.Unlock()
}

func (r *AgentRegistry) RegisterGRPC(srv *grpc.Server) {
	agentpb.RegisterAgentGatewayServer(srv, r)
}

// registryKey returns the map key used to uniquely identify an agent+pillar pair.
// Multiple pillars on the same host share a node_id, so we key by "pillar:agentID".
func registryKey(pillar, agentID string) string {
	return pillar + ":" + agentID
}

// Connect implements the bidirectional stream between agent and controller.
func (r *AgentRegistry) Connect(stream agentpb.AgentGateway_ConnectServer) error {
	var agentID string
	var pillar string
	ctx := stream.Context()

	// Per-agent outbound send channel
	sendCh := make(chan *agentpb.ControllerMessage, 64)

	// Goroutine: forward controller messages to agent
	go func() {
		for {
			select {
			case msg, ok := <-sendCh:
				if !ok {
					return
				}
				if err := stream.Send(msg); err != nil {
					r.log.Warn("failed to send to agent", zap.String("agent_id", agentID), zap.Error(err))
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	defer func() {
		if agentID != "" {
			r.unregister(registryKey(pillar, agentID))
		}
	}()

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch p := msg.Payload.(type) {
		case *agentpb.AgentMessage_Heartbeat:
			hb := p.Heartbeat
			agentID = hb.AgentId
			pillar = hb.Pillar
			r.upsert(registryKey(pillar, agentID), hb, sendCh)

		case *agentpb.AgentMessage_TaskResult:
			r.log.Info("task result",
				zap.String("agent_id", agentID),
				zap.String("task_id", p.TaskResult.TaskId),
				zap.Bool("success", p.TaskResult.Success),
				zap.String("error", p.TaskResult.ErrorMessage),
			)
			taskID := p.TaskResult.TaskId
			namespace := taskID
			if idx := strings.Index(taskID, ":"); idx >= 0 {
				namespace = taskID[:idx]
			}
			r.mu.RLock()
			handler := r.taskResultHandlers[namespace]
			r.mu.RUnlock()
			if handler != nil {
				go handler(p.TaskResult)
			}

		case *agentpb.AgentMessage_ResourceEvent:
			ev := p.ResourceEvent
			r.log.Info("resource event",
				zap.String("agent_id", agentID),
				zap.String("resource_type", ev.ResourceType),
				zap.String("resource_id", ev.ResourceId),
				zap.String("new_state", ev.NewState),
			)
		}
	}
}

func (r *AgentRegistry) upsert(key string, hb *agentpb.Heartbeat, sendCh chan *agentpb.ControllerMessage) {
	r.mu.Lock()

	rec, exists := r.agents[key]
	if !exists {
		rec = &AgentRecord{AgentID: hb.AgentId, sendCh: sendCh}
		r.agents[key] = rec
		r.log.Info("agent registered", zap.String("id", hb.AgentId), zap.String("pillar", hb.Pillar))
	}
	rec.Pillar = hb.Pillar
	rec.LastSeen = time.Now()
	if hb.Capabilities != nil {
		rec.Capabilities = *hb.Capabilities
	}
	if hb.Resources != nil {
		rec.Resources = *hb.Resources
	}

	// Snapshot reconnect handler while holding the lock.
	var reconnectFn AgentReconnectHandler
	if !exists {
		reconnectFn = r.agentReconnectHandler[hb.Pillar]
	}
	r.mu.Unlock()

	// Refresh etcd lease
	go r.etcd.PutWithTTL(context.Background(),
		"/pulsar/agents/"+hb.AgentId,
		hb.AgentId,
		agentLeaseTTL,
	)

	// Notify reconnect handler outside the lock.
	if reconnectFn != nil {
		go reconnectFn(hb.AgentId)
	}
}

func (r *AgentRegistry) unregister(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.agents[key]; ok {
		r.log.Info("agent disconnected", zap.String("id", rec.AgentID), zap.String("pillar", rec.Pillar))
	}
	delete(r.agents, key)
}

// ListByPillar returns all live agents for a given pillar.
func (r *AgentRegistry) ListByPillar(pillar string) []*AgentRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*AgentRecord, 0)
	for _, rec := range r.agents {
		if rec.Pillar == pillar {
			out = append(out, rec)
		}
	}
	return out
}

// Send dispatches a controller message to a specific agent identified by pillar and agentID.
func (r *AgentRegistry) Send(pillar, agentID string, msg *agentpb.ControllerMessage) bool {
	r.mu.RLock()
	rec, ok := r.agents[registryKey(pillar, agentID)]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	select {
	case rec.sendCh <- msg:
		return true
	default:
		return false
	}
}
