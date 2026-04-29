package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/agomez/pulsar/internal/agent"
	"github.com/agomez/pulsar/internal/controller"
	"github.com/agomez/pulsar/internal/version"
)

var rootCmd = &cobra.Command{
	Use:   "pulsar",
	Short: "Pulsar compute orchestration platform",
	Long:  "Pulsar is a lightweight IaaS platform with compute, network, and storage pillars.",
}

func init() {
	rootCmd.AddCommand(controller.NewCommand())
	rootCmd.AddCommand(agent.NewCommand())
	rootCmd.AddCommand(version.NewCommand())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
