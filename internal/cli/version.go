package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var Version = "v0.4.1"

func getCommitSHA() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			if len(setting.Value) > 7 {
				return setting.Value[:7]
			}
			return setting.Value
		}
	}
	return ""
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print CHEST version and build information",
		Run: func(cmd *cobra.Command, args []string) {
			sha := getCommitSHA()
			if sha != "" {
				fmt.Printf("CHEST %s (%s) (%s/%s)\n", Version, sha, runtime.GOOS, runtime.GOARCH)
			} else {
				fmt.Printf("CHEST %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
			}
		},
	}
}
