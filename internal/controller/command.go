package controller

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/config"
)

func NewCommand() *cobra.Command {
	var cfgFile string

	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Run the Pulsar control plane",
		Long:  "Starts the REST API server and gRPC gateway that agents connect to.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}

			log, err := buildLogger(cfg.Logging)
			if err != nil {
				return fmt.Errorf("logger: %w", err)
			}
			defer log.Sync() //nolint:errcheck

			srv, err := NewServer(cfg, log)
			if err != nil {
				return err
			}
			return srv.Run(context.Background())
		},
	}

	cmd.Flags().StringVarP(&cfgFile, "config", "c", "", "Path to config file (default: /etc/pulsar/pulsar.yaml)")
	return cmd
}

func buildLogger(cfg config.LoggingConfig) (*zap.Logger, error) {
	var zapCfg zap.Config
	if cfg.Level == "debug" {
		zapCfg = zap.NewDevelopmentConfig()
	} else {
		zapCfg = zap.NewProductionConfig()
	}

	level, err := zap.ParseAtomicLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	zapCfg.Level = level

	if cfg.Format == "console" {
		zapCfg.Encoding = "console"
	}

	return zapCfg.Build()
}
