/*
Copyright © 2025 Mulga Defense Corporation

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"fmt"
	"os"

	"github.com/mulgadc/hive/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile   string
	appConfig *config.Config
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "hive",
	Short: "Hive - A NATS-based daemon service",
	Long: `Hive is a daemon service that connects to NATS and subscribes to EC2 events.
It can be configured via config file, environment variables, or command line flags.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (optional)")

	// Authentication (access_key, secret)
	rootCmd.PersistentFlags().String("access-key", "", "AWS access key (overrides config file and env)")
	rootCmd.PersistentFlags().String("secret-key", "", "AWS secret key (overrides config file and env)")
	rootCmd.PersistentFlags().String("host", "", "AWS Endpoint (overrides config file and env)")

	// Viperblock config
	rootCmd.PersistentFlags().String("base-dir", "", "Viperblock base directory (overrides config file and env)")

	// NATS specific flags
	rootCmd.PersistentFlags().String("nats-host", "", "NATS server host (overrides config file and env)")
	rootCmd.PersistentFlags().String("nats-token", "", "NATS authentication token (overrides config file and env)")
	rootCmd.PersistentFlags().String("nats-subject", "", "NATS subscription subject (overrides config file and env)")

	// Bind flags to viper
	viper.BindPFlag("access_key", rootCmd.PersistentFlags().Lookup("access-key"))
	viper.BindPFlag("secret_key", rootCmd.PersistentFlags().Lookup("secret-key"))
	viper.BindPFlag("host", rootCmd.PersistentFlags().Lookup("host"))
	viper.BindPFlag("base-dir", rootCmd.PersistentFlags().Lookup("base-dir"))

	viper.BindPFlag("nats.host", rootCmd.PersistentFlags().Lookup("nats-host"))
	viper.BindPFlag("nats.acl.token", rootCmd.PersistentFlags().Lookup("nats-token"))
	viper.BindPFlag("nats.sub.subject", rootCmd.PersistentFlags().Lookup("nats-subject"))

}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	var err error

	// Load configuration
	appConfig, err = config.LoadConfig(cfgFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		fmt.Fprintln(os.Stderr, "Continuing with environment variables and defaults...")
	}
}
