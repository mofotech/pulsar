package agent

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/config"
)

func NewCommand() *cobra.Command {
	var (
		cfgFile string
		pillar  string
	)

	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run a Pulsar agent (compute, network, or storage)",
		Long:  "Connects to the controller gRPC gateway and executes pillar-specific tasks.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}

			if pillar != "" {
				cfg.Agent.Pillar = pillar
			}
			if cfg.Agent.Pillar == "" {
				return fmt.Errorf("--pillar is required (compute|network|storage)")
			}

			log, err := buildLogger(cfg.Logging)
			if err != nil {
				return fmt.Errorf("logger: %w", err)
			}
			defer log.Sync() //nolint:errcheck

			a := NewAgent(cfg, log)
			return a.Run(context.Background())
		},
	}

	cmd.Flags().StringVarP(&cfgFile, "config", "c", "", "Path to config file")
	cmd.Flags().StringVar(&pillar, "pillar", "", "Pillar to run: compute | network | storage")
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
