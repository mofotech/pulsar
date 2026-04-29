package controller

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/agomez/pulsar/internal/config"
	"github.com/agomez/pulsar/internal/controller/reconciler"
	"github.com/agomez/pulsar/internal/controller/registry"
	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/internal/store/postgres"
)

type Server struct {
	cfg      *config.Config
	log      *zap.Logger
	registry *registry.AgentRegistry
	etcd     *etcd.Client
	db       *postgres.DB
}

func NewServer(cfg *config.Config, log *zap.Logger) (*Server, error) {
	etcdClient, err := etcd.NewClient(cfg.Controller.Etcd.Endpoints)
	if err != nil {
		return nil, fmt.Errorf("etcd: %w", err)
	}

	db, err := postgres.New(cfg.Controller.Postgres.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}

	if err := db.Migrate(context.Background(), log); err != nil {
		return nil, fmt.Errorf("db migrate: %w", err)
	}

	reg := registry.NewAgentRegistry(etcdClient, log)

	return &Server{
		cfg:      cfg,
		log:      log,
		registry: reg,
		etcd:     etcdClient,
		db:       db,
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start gRPC server (receives agent connections)
	grpcSrv, err := s.startGRPC()
	if err != nil {
		return fmt.Errorf("grpc: %w", err)
	}

	// Start stale-agent cleanup sweep across all pillars.
	s.registry.StartCleanup(ctx)

	// Start orphan reconciler — garbage-collects infrastructure resources that
	// exist on agents but are no longer tracked in etcd.
	rec := reconciler.New(s.etcd, s.registry, s.log, 0 /* use default 5m interval */)
	s.registry.RegisterTaskResultHandler("reconciler", rec.HandleTaskResult)
	rec.Start(ctx)

	// Start REST API server
	httpSrv := s.startHTTP()

	s.log.Info("controller started",
		zap.String("http", s.cfg.Controller.ListenHTTP),
		zap.String("grpc", s.cfg.Controller.ListenGRPC),
	)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		s.log.Info("shutting down", zap.String("signal", sig.String()))
	case <-ctx.Done():
	}

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()

	grpcSrv.GracefulStop()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		s.log.Error("http shutdown error", zap.Error(err))
	}
	s.etcd.Close()
	s.db.Close()

	return nil
}

func (s *Server) startGRPC() (*grpc.Server, error) {
	lis, err := net.Listen("tcp", s.cfg.Controller.ListenGRPC)
	if err != nil {
		return nil, err
	}

	srv := grpc.NewServer()
	s.registry.RegisterGRPC(srv)

	go func() {
		if err := srv.Serve(lis); err != nil {
			s.log.Error("grpc serve error", zap.Error(err))
		}
	}()
	return srv, nil
}

func (s *Server) startHTTP() *http.Server {
	router := newRouter(s.cfg, s.log, s.etcd, s.db, s.registry)
	srv := &http.Server{
		Addr:         s.cfg.Controller.ListenHTTP,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("http serve error", zap.Error(err))
		}
	}()
	return srv
}
