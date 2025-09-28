/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"log"

	"github.com/mulgadc/hive/hive/daemon"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// daemonCmd represents the daemon command
var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start the Hive daemon service",
	Long: `Start the Hive daemon service that listens for EC2 launch events
and manages local resources for instance creation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if appConfig == nil {
			return fmt.Errorf("configuration not loaded")
		}

		// Overwrite defaults (CLI first, config second, env third)
		baseDir := viper.GetString("base-dir")

		if baseDir != "" {
			fmt.Println("Overwriting base-dir to:", baseDir)
			appConfig.BaseDir = baseDir
		}

		d := daemon.NewDaemon(appConfig)
		log.Println("Starting Hive daemon...")
		return d.Start()
	},
}

func init() {
	rootCmd.AddCommand(daemonCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	//daemonCmd.Flags().String("nats", "nats://0.0.0.0:4222", "NATs server address")
}
