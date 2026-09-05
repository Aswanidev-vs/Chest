package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

const (
	Version   = "v0.1.0"
	CommitSHA = "dev"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print CHEST version and build information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("CHEST %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
		},
	}
}
