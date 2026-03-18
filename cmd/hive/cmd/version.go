package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Version variables set via ldflags at build time.
// Example: go build -ldflags "-X github.com/mulgadc/spinifex/cmd/hive/cmd.Version=v1.0.0"
var (
	Version = "dev"
	Commit  = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the Hive version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("hive %s (%s) %s/%s\n", Version, Commit, runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
