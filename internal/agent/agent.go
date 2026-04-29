package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	computeexec "github.com/agomez/pulsar/internal/agent/compute"
	libvirtdrv "github.com/agomez/pulsar/internal/agent/compute/libvirt"
	networkexec "github.com/agomez/pulsar/internal/agent/network"
	storageexec "github.com/agomez/pulsar/internal/agent/storage"
	"github.com/agomez/pulsar/internal/config"
)

const heartbeatInterval = 10 * time.Second

// Agent connects to the controller and runs a single pillar executor.
type Agent struct {
	cfg     *config.Config
	log     *zap.Logger
	compute *computeexec.Executor
	network *networkexec.Executor
	storage *storageexec.Executor
}

func NewAgent(cfg *config.Config, log *zap.Logger) *Agent {
	a := &Agent{cfg: cfg, log: log}
	if cfg.Agent.Pillar == "compute" {
		a.compute = computeexec.NewExecutor(cfg.Agent.Compute.ImageCacheDir, log)
		a.registerComputeDrivers()
	}
	if cfg.Agent.Pillar == "network" {
		a.network = networkexec.NewExecutor(cfg.Agent.Network, log)
	}
	if cfg.Agent.Pillar == "storage" {
		exec, err := storageexec.NewExecutor(cfg.Agent.Storage, log)
		if err != nil {
			log.Warn("storage executor init failed", zap.Error(err))
		} else {
			a.storage = exec
			log.Info("storage executor ready",
				zap.String("vg", cfg.Agent.Storage.Backends.LVM.VolumeGroup),
				zap.String("pool", cfg.Agent.Storage.Backends.LVM.ThinPool),
			)
		}
	}
	return a
}

func (a *Agent) registerComputeDrivers() {
	drv, err := libvirtdrv.New(
		a.cfg.Agent.Compute.LibvirtSocket,
		a.cfg.Agent.Compute.InstanceDir,
		a.cfg.Agent.Compute.DomainType,
		a.cfg.Agent.Compute.Emulator,
	)
	if err != nil {
		a.log.Warn("libvirt unavailable, kvm driver not registered", zap.Error(err))
		return
	}
	a.compute.RegisterDriver("kvm", drv)
	a.log.Info("registered kvm driver via libvirt",
		zap.String("socket", a.cfg.Agent.Compute.LibvirtSocket),
		zap.String("domain_type", a.cfg.Agent.Compute.DomainType),
		zap.String("emulator", a.cfg.Agent.Compute.Emulator),
	)
}

func (a *Agent) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, err := grpc.NewClient(
		a.cfg.Agent.ControllerGRPC,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}
	defer conn.Close()

	client := agentpb.NewAgentGatewayClient(conn)

	stream, err := client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect stream: %w", err)
	}

	a.log.Info("connected to controller",
		zap.String("addr", a.cfg.Agent.ControllerGRPC),
		zap.String("pillar", a.cfg.Agent.Pillar),
	)

	// Goroutine: send heartbeats
	go a.heartbeatLoop(ctx, stream)

	// Goroutine: receive task assignments
	errCh := make(chan error, 1)
	go func() { errCh <- a.receiveLoop(ctx, stream) }()

	// Wait for signal or error
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		a.log.Info("shutting down", zap.String("signal", sig.String()))
		return nil
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

func (a *Agent) heartbeatLoop(ctx context.Context, stream agentpb.AgentGateway_ConnectClient) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	nodeID := a.cfg.Agent.NodeID
	if nodeID == "" {
		nodeID, _ = os.Hostname()
	}

	for {
		select {
		case <-ticker.C:
			hb := &agentpb.AgentMessage{
				Payload: &agentpb.AgentMessage_Heartbeat{
					Heartbeat: &agentpb.Heartbeat{
						AgentId:       nodeID,
						Pillar:        a.cfg.Agent.Pillar,
						TimestampUnix: time.Now().Unix(),
					},
				},
			}
			var caps agentpb.AgentCapabilities
			if a.compute != nil {
				caps = a.compute.AdvertisedCapabilities()
			}
			if a.storage != nil {
				storageCaps := a.storage.AdvertisedCapabilities()
				caps.StorageBackends = storageCaps.StorageBackends
			}
			if a.compute != nil || a.storage != nil {
				hb.Payload.(*agentpb.AgentMessage_Heartbeat).Heartbeat.Capabilities = &caps
			}
			if a.compute != nil {
				usage := a.compute.ResourceUsage()
				hb.Payload.(*agentpb.AgentMessage_Heartbeat).Heartbeat.Resources = &usage
			}
			if err := stream.Send(hb); err != nil {
				a.log.Warn("heartbeat send failed", zap.Error(err))
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (a *Agent) receiveLoop(ctx context.Context, stream agentpb.AgentGateway_ConnectClient) error {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream recv: %w", err)
		}

		switch p := msg.Payload.(type) {
		case *agentpb.ControllerMessage_TaskAssignment:
			go a.handleTask(ctx, stream, p.TaskAssignment)
		case *agentpb.ControllerMessage_DrainRequest:
			a.log.Info("drain requested",
				zap.String("reason", p.DrainRequest.Reason),
				zap.Int32("grace_seconds", p.DrainRequest.GraceSeconds),
			)
		}
	}
}

func (a *Agent) handleTask(ctx context.Context, stream agentpb.AgentGateway_ConnectClient, task *agentpb.TaskAssignment) {
	a.log.Info("handling task", zap.String("task_id", task.TaskId), zap.String("type", task.TaskType))

	var result *agentpb.TaskResult

	switch a.cfg.Agent.Pillar {
	case "compute":
		if a.compute != nil {
			result = a.compute.Execute(ctx, task)
		}
	case "network":
		if a.network != nil {
			result = a.network.Execute(ctx, task)
		}
	case "storage":
		if a.storage != nil {
			result = a.storage.Execute(ctx, task)
		}
	default:
		result = &agentpb.TaskResult{
			TaskId:       task.TaskId,
			Success:      false,
			ErrorMessage: fmt.Sprintf("pillar %q cannot handle task type %q", a.cfg.Agent.Pillar, task.TaskType),
		}
	}

	if result == nil {
		return
	}

	msg := &agentpb.AgentMessage{
		Payload: &agentpb.AgentMessage_TaskResult{TaskResult: result},
	}
	if err := stream.Send(msg); err != nil {
		a.log.Warn("failed to send task result", zap.Error(err))
	}
}
