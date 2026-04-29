package version

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Set via -ldflags at build time:
//   go build -ldflags "-X github.com/agomez/pulsar/internal/version.Version=v0.1.0"
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("pulsar %s (commit: %s, built: %s)\n", Version, GitCommit, BuildDate)
		},
	}
}
