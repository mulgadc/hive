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
	"log/slog"
	"os"
	"strings"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/service"
	"github.com/mulgadc/hive/hive/services/nats"
	"github.com/mulgadc/hive/hive/services/predastore"
	"github.com/mulgadc/hive/hive/services/viperblockd"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage Hive services",
}

var predastoreCmd = &cobra.Command{
	Use:   "predastore",
	Short: "Manage the predastore service",
}

var viperblockCmd = &cobra.Command{
	Use:   "viperblock",
	Short: "Manage the viperblock service",
}

var natsCmd = &cobra.Command{
	Use:   "nats",
	Short: "Manage the nats service",
}

var hiveCmd = &cobra.Command{
	Use:   "hive",
	Short: "Manage the hive service",
}

var awsgwCmd = &cobra.Command{
	Use:   "awsgw",
	Short: "Manage the awsgw (AWS gateway) service",
}

var predastoreStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the predastore service",
	Run: func(cmd *cobra.Command, args []string) {
		// Add your start logic here
		fmt.Println("Starting predastore service...")

		// Get the port from the flags
		port := viper.GetInt("port")
		host := viper.GetString("host")
		basePath := viper.GetString("base-path")
		debug := viper.GetBool("debug")

		// Required, no default
		if basePath == "" {
			fmt.Println("Base path is not set")
			return
		}

		configPath := viper.GetString("config-path")

		if configPath == "" {
			fmt.Println("Config path is not set")
			return
		}

		tlsCert := viper.GetString("tls-cert")

		if tlsCert == "" {
			fmt.Println("TLS cert is not set")
			return
		}

		tlsKey := viper.GetString("tls-key")

		if tlsKey == "" {
			fmt.Println("TLS key is not set")
			return
		}

		service, err := service.New("predastore", &predastore.Config{
			Port:       port,
			Host:       host,
			BasePath:   basePath,
			ConfigPath: configPath,
			Debug:      debug,
			TlsCert:    tlsCert,
			TlsKey:     tlsKey,
		})

		if err != nil {
			fmt.Println("Error starting predastore service:", err)
			return
		}

		service.Start()

		fmt.Println("Predastore service started", service)
	},
}

var predastoreStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the predastore service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Stopping predastore service...")

		service, err := service.New("predastore", &predastore.Config{})

		if err != nil {
			fmt.Println("Error stopping predastore service:", err)
			return
		}

		service.Stop()

		fmt.Println("Predastore service stopped")

	},
}

var predastoreStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get status of the predastore service",
	Run: func(cmd *cobra.Command, args []string) {
		// Add your status logic here
		fmt.Println("Predastore service status: ...")
	},
}

// Repeat for viperblock
var viperblockStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the viperblock service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting viperblock service...")

		natsHost := viper.GetString("nats-host")

		if natsHost == "" {
			err := fmt.Errorf("nats-host must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		s3Host := viper.GetString("s3-host")

		if s3Host == "" {
			err := fmt.Errorf("s3-host must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		s3Bucket := viper.GetString("s3-bucket")

		if s3Bucket == "" {
			err := fmt.Errorf("s3-bucket must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		s3Region := viper.GetString("s3-region")

		if s3Region == "" {
			err := fmt.Errorf("s3-region must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		accessKey := viper.GetString("access-key")
		if accessKey == "" {
			err := fmt.Errorf("access-key must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		secretKey := viper.GetString("secret-key")
		if secretKey == "" {
			err := fmt.Errorf("secret-key must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		baseDir := viper.GetString("base-dir")
		if baseDir == "" {
			err := fmt.Errorf("base-dir must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		pluginPath := viper.GetString("plugin-path")

		if pluginPath == "" {
			err := fmt.Errorf("plugin-path must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		// Check plugin path exists
		if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
			err := fmt.Errorf("plugin-path does not exist: %s", pluginPath)
			slog.Error(err.Error())
			os.Exit(1)
		}

		service, err := service.New("viperblock", &viperblockd.Config{
			NatsHost:   natsHost,
			PluginPath: pluginPath,
			S3Host:     s3Host,
			Bucket:     s3Bucket,
			Region:     s3Region,
			AccessKey:  accessKey,
			SecretKey:  secretKey,
			BaseDir:    baseDir,
		})

		if err != nil {
			fmt.Println("Error starting viperblock service:", err)
			return
		}

		_, err = service.Start()

		if err != nil {
			fmt.Println("Error starting viperblock service:", err)
			return
		}

		fmt.Println("Viperblock service started", service)
	},
}

var viperblockStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the viperblock service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Stopping viperblock service...")

		service, err := service.New("viperblock", &viperblockd.Config{})

		if err != nil {
			fmt.Println("Error stopping viperblock service:", err)
			return
		}

		service.Stop()

		fmt.Println("Viperblock service stopped")

	},
}

var viperblockStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get status of the viperblock service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Viperblock service status: ...")
	},
}

// Repeat for nats
var natsStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the nats service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting nats service...")

		port := viper.GetInt("port")
		host := viper.GetString("host")
		debug := viper.GetBool("debug")
		dataDir := viper.GetString("data-dir")
		jetStream := viper.GetBool("jetstream")

		service, err := service.New("nats", &nats.Config{
			Port:      port,
			Host:      host,
			Debug:     debug,
			DataDir:   dataDir,
			JetStream: jetStream,
		})

		if err != nil {
			fmt.Println("Error starting nats service:", err)
			return
		}

		service.Start()
		fmt.Println("NATS service started")
	},
}

var natsStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the nats service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Stopping nats service...")

		service, err := service.New("nats", &nats.Config{})

		if err != nil {
			fmt.Println("Error stopping nats service:", err)
			return
		}

		service.Stop()

		fmt.Println("Nats service stopped")
	},
}

var natsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get status of the nats service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Nats service status: ...")
	},
}

// Repeat for hive
var hiveStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the hive service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting hive service...")

		cfgFile := viper.GetString("config")

		if cfgFile == "" {
			fmt.Println("Config file is not set")
			return
		}

		// TODO: Support ENV vars, CLI, otherwise revert to config.LoadConfig()
		appConfig, err := config.LoadConfig(cfgFile)

		if err != nil {
			fmt.Println("Error loading config file:", err)
			return
		}

		// Overwrite defaults (CLI first, config second, env third)
		baseDir := viper.GetString("base-dir")

		if baseDir != "" {
			fmt.Println("Overwriting base-dir to:", baseDir)
			appConfig.BaseDir = baseDir
		}

		// Overwrite defaults (CLI first, config second, env third)
		walDir := viper.GetString("wal-dir")

		if walDir != "" {
			fmt.Println("Overwriting wal-dir to:", walDir)
			appConfig.WalDir = walDir
		}

		service, err := service.New("hive", appConfig)

		if err != nil {
			fmt.Println("Error starting hive service:", err)
			return
		}

		service.Start()
		fmt.Println("HIVE service started")
	},
}

var hiveStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the hive service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Stopping hive service...")

		service, err := service.New("hive", &config.Config{})

		if err != nil {
			fmt.Println("Error stopping hive service:", err)
			return
		}

		service.Stop()

		fmt.Println("Hive service stopped")
	},
}

var hiveStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get status of the hive service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Hive service status: ...")
	},
}

// AWS GW

var awsgwStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the awsgw service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting awsgw service...")

		cfgFile := viper.GetString("config")

		if cfgFile == "" {
			fmt.Println("Config file is not set")
			return
		}

		// TODO: Support ENV vars, CLI, otherwise revert to config.LoadConfig()
		appConfig, err := config.LoadConfig(cfgFile)

		if err != nil {
			fmt.Println("Error loading config file:", err)
			return
		}

		// Overwrite defaults (CLI first, config second, env third)
		awsgwHost := viper.GetString("host")
		if awsgwHost != "" {
			fmt.Println("Overwriting awsgw host to:", awsgwHost)
			appConfig.AWSGW.Host = awsgwHost
		}

		awsgwTlsCert := viper.GetString("tls-cert")
		if awsgwTlsCert != "" {
			fmt.Println("Overwriting awsgw tls-cert to:", awsgwTlsCert)
			appConfig.AWSGW.TLSCert = awsgwTlsCert
		}

		awsgwTlsKey := viper.GetString("tls-key")

		if awsgwTlsKey != "" {
			fmt.Println("Overwriting awsgw tls-key to:", awsgwTlsKey)
			appConfig.AWSGW.TLSKey = awsgwTlsKey
		}

		service, err := service.New("awsgw", appConfig)

		if err != nil {
			fmt.Println("Error starting awsgw service:", err)
			return
		}

		service.Start()
		fmt.Println("AWSGW service started")
	},
}

var awsgwStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the awsgw service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Stopping awsgw service...")

		service, err := service.New("awsgw", &config.Config{})

		if err != nil {
			fmt.Println("Error stopping awsgw service:", err)
			return
		}

		service.Stop()

		fmt.Println("AWSGW service stopped")
	},
}

var awsgwStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get status of the awsgw service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("AWSGW service status: ...")
	},
}

func init() {

	viper.SetEnvPrefix("HIVE") // Prefix for environment variables
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	viper.AutomaticEnv() // Read environment variables automatically

	rootCmd.AddCommand(serviceCmd)

	serviceCmd.AddCommand(predastoreCmd)

	// Predastore Port
	predastoreCmd.PersistentFlags().Int("port", 8443, "Predastore (S3) port")
	viper.BindEnv("port", "HIVE_PREDASTORE_PORT")
	viper.BindPFlag("port", predastoreCmd.PersistentFlags().Lookup("port"))

	// Predastore Host
	predastoreCmd.PersistentFlags().String("host", "0.0.0.0", "Predastore (S3) host")
	viper.BindEnv("host", "HIVE_PREDASTORE_HOST")
	viper.BindPFlag("host", predastoreCmd.PersistentFlags().Lookup("host"))

	// Base path
	predastoreCmd.PersistentFlags().String("base-path", "", "Predastore (S3) base path")
	viper.BindEnv("base-path", "HIVE_PREDASTORE_BASE_PATH")
	viper.BindPFlag("base-path", predastoreCmd.PersistentFlags().Lookup("base-path"))

	// Predastore Config Path
	predastoreCmd.PersistentFlags().String("config-path", "", "Predastore (S3) config path")
	viper.BindEnv("config-path", "HIVE_PREDASTORE_CONFIG_PATH")
	viper.BindPFlag("config-path", predastoreCmd.PersistentFlags().Lookup("config-path"))

	// Predastore Debug
	predastoreCmd.PersistentFlags().Bool("debug", false, "Predastore (S3) debug")
	viper.BindEnv("debug", "HIVE_PREDASTORE_DEBUG")
	viper.BindPFlag("debug", predastoreCmd.PersistentFlags().Lookup("debug"))

	// Predastore TLS Cert
	predastoreCmd.PersistentFlags().String("tls-cert", "", "Predastore (S3) TLS certificate")
	viper.BindEnv("tls-cert", "HIVE_PREDASTORE_TLS_CERT")
	viper.BindPFlag("tls-cert", predastoreCmd.PersistentFlags().Lookup("tls-cert"))

	// Predastore TLS Key
	predastoreCmd.PersistentFlags().String("tls-key", "", "Predastore (S3) TLS key")
	viper.BindEnv("tls-key", "HIVE_PREDASTORE_TLS_KEY")
	viper.BindPFlag("tls-key", predastoreCmd.PersistentFlags().Lookup("tls-key"))

	predastoreCmd.AddCommand(predastoreStartCmd)
	predastoreCmd.AddCommand(predastoreStopCmd)
	predastoreCmd.AddCommand(predastoreStatusCmd)

	serviceCmd.AddCommand(viperblockCmd)

	viperblockCmd.PersistentFlags().String("s3-host", "0.0.0.0:8443", "Predastore (S3) host URI")
	viper.BindEnv("s3-host", "HIVE_VIPERBLOCK_S3_HOST")
	viper.BindPFlag("s3-host", predastoreCmd.PersistentFlags().Lookup("s3-host"))

	viperblockCmd.PersistentFlags().String("s3-bucket", "predastore", "Predastore (S3) bucket")
	viper.BindEnv("s3-bucket", "HIVE_VIPERBLOCK_S3_BUCKET")
	viper.BindPFlag("s3-bucket", predastoreCmd.PersistentFlags().Lookup("s3-bucket"))

	viperblockCmd.PersistentFlags().String("s3-region", "ap-southeast-2", "Predastore (S3) region")
	viper.BindEnv("s3-region", "HIVE_VIPERBLOCK_S3_REGION")
	viper.BindPFlag("s3-region", predastoreCmd.PersistentFlags().Lookup("s3-region"))

	viperblockCmd.PersistentFlags().String("plugin-path", "/opt/hive/lib/nbdkit-viperblock-plugin.so", "Pathname to the nbdkit viperblockplugin")
	viper.BindEnv("plugin-path", "HIVE_VIPERBLOCK_PLUGIN_PATH")
	viper.BindPFlag("plugin-path", predastoreCmd.PersistentFlags().Lookup("plugin-path"))

	viperblockCmd.AddCommand(viperblockStartCmd)
	viperblockCmd.AddCommand(viperblockStopCmd)
	viperblockCmd.AddCommand(viperblockStatusCmd)

	// Nats
	serviceCmd.AddCommand(natsCmd)

	natsCmd.AddCommand(natsStartCmd)
	natsCmd.AddCommand(natsStopCmd)
	natsCmd.AddCommand(natsStatusCmd)

	// Add NATS flags
	natsCmd.PersistentFlags().Int("port", 4222, "NATS server port")
	viper.BindEnv("port", "HIVE_NATS_PORT")
	viper.BindPFlag("port", natsCmd.PersistentFlags().Lookup("port"))

	natsCmd.PersistentFlags().String("host", "0.0.0.0", "NATS server host")
	viper.BindEnv("host", "HIVE_NATS_HOST")
	viper.BindPFlag("host", natsCmd.PersistentFlags().Lookup("host"))

	natsCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")
	viper.BindEnv("debug", "HIVE_NATS_DEBUG")
	viper.BindPFlag("debug", natsCmd.PersistentFlags().Lookup("debug"))

	natsCmd.PersistentFlags().String("data-dir", "", "NATS data directory")
	viper.BindEnv("data-dir", "HIVE_NATS_DATA_DIR")
	viper.BindPFlag("data-dir", natsCmd.PersistentFlags().Lookup("data-dir"))

	natsCmd.PersistentFlags().Bool("jetstream", false, "Enable JetStream")
	viper.BindEnv("jetstream", "HIVE_NATS_JETSTREAM")
	viper.BindPFlag("jetstream", natsCmd.PersistentFlags().Lookup("jetstream"))

	// Hive
	serviceCmd.AddCommand(hiveCmd)

	hiveCmd.AddCommand(hiveStartCmd)
	hiveCmd.AddCommand(hiveStopCmd)
	hiveCmd.AddCommand(hiveStatusCmd)

	hiveCmd.PersistentFlags().String("wal-dir", "", "Write-ahead log (WAL) directory. Place on high-speed NVMe disk, or tmpfs for development.")
	viper.BindEnv("wal-dir", "HIVE_WAL_DIR")
	viper.BindPFlag("wal-dir", hiveCmd.PersistentFlags().Lookup("wal-dir"))

	// AWS GW
	serviceCmd.AddCommand(awsgwCmd)

	awsgwCmd.PersistentFlags().String("host", "0.0.0.0:9999", "AWS Gateway server host")
	viper.BindEnv("host", "HIVE_AWSGW_HOST")
	viper.BindPFlag("host", awsgwCmd.PersistentFlags().Lookup("host"))

	// AWS GW TLS Cert
	awsgwCmd.PersistentFlags().String("tls-cert", "", "AWS Gateway TLS certificate")
	viper.BindEnv("tls-cert", "HIVE_AWSGW_TLS_CERT")
	viper.BindPFlag("tls-cert", awsgwCmd.PersistentFlags().Lookup("tls-cert"))

	// AWS GW TLS Key
	awsgwCmd.PersistentFlags().String("tls-key", "", "AWS Gateway TLS key")
	viper.BindEnv("tls-key", "HIVE_AWSGW_TLS_KEY")
	viper.BindPFlag("tls-key", awsgwCmd.PersistentFlags().Lookup("tls-key"))

	awsgwCmd.PersistentFlags().Bool("debug", false, "AWS Gateway Debug")
	viper.BindEnv("debug", "HIVE_AWSGW_DEBUG")
	viper.BindPFlag("debug", awsgwCmd.PersistentFlags().Lookup("debug"))

	awsgwCmd.AddCommand(awsgwStartCmd)
	awsgwCmd.AddCommand(awsgwStopCmd)
	awsgwCmd.AddCommand(awsgwStatusCmd)

}
