/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mulgadc/hive/hive/service"
	"github.com/mulgadc/hive/hive/services/nats"
	"github.com/mulgadc/hive/hive/services/predastore"
	"github.com/mulgadc/hive/hive/services/viperblockd"
	"github.com/spf13/cobra"
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

var predastoreStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the predastore service",
	Run: func(cmd *cobra.Command, args []string) {
		// Add your start logic here
		fmt.Println("Starting predastore service...")

		// Get the port from the flags
		port, err := cmd.Flags().GetInt("port")
		if err != nil {
			fmt.Println("Error getting port:", err)
			return
		}

		host, err := cmd.Flags().GetString("host")
		if err != nil {
			fmt.Println("Error getting host:", err)
			return
		}

		basePath, err := cmd.Flags().GetString("base-path")
		if err != nil {
			fmt.Println("Error getting base-path:", err)
			return
		}

		configPath, err := cmd.Flags().GetString("config-path")
		if err != nil {
			fmt.Println("Error getting config-path:", err)
			return
		}

		debug, err := cmd.Flags().GetBool("debug")
		if err != nil {
			fmt.Println("Error getting debug:", err)
			return
		}

		// TLS
		tlsCert, err := cmd.Flags().GetString("tls-cert")
		if err != nil {
			fmt.Println("Error getting tls-cert:", err)
			return
		}

		tlsKey, err := cmd.Flags().GetString("tls-key")
		if err != nil {
			fmt.Println("Error getting tls-key:", err)
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

		fmt.Println("Predastore service stopped", service)

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

		natsHost, _ := cmd.Flags().GetString("nats-host")

		if natsHost == "" {
			err := fmt.Errorf("nats-host must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		s3Host, _ := cmd.Flags().GetString("s3-host")

		if s3Host == "" {
			err := fmt.Errorf("s3-host must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		s3Bucket, _ := cmd.Flags().GetString("s3-bucket")

		if s3Bucket == "" {
			err := fmt.Errorf("s3-bucket must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		s3Region, _ := cmd.Flags().GetString("s3-region")

		if s3Region == "" {
			err := fmt.Errorf("s3-region must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		accessKey, _ := cmd.Flags().GetString("access-key")
		if accessKey == "" {
			err := fmt.Errorf("access-key must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		secretKey, _ := cmd.Flags().GetString("secret-key")
		if secretKey == "" {
			err := fmt.Errorf("secret-key must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		baseDir, _ := cmd.Flags().GetString("base-dir")
		if baseDir == "" {
			err := fmt.Errorf("base-dir must be defined")
			slog.Error(err.Error())
			os.Exit(1)
		}

		pluginPath, _ := cmd.Flags().GetString("plugin-path")

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

		// Get configuration from flags or use defaults
		port, _ := cmd.Flags().GetInt("port")
		host, _ := cmd.Flags().GetString("host")
		debug, _ := cmd.Flags().GetBool("debug")
		dataDir, _ := cmd.Flags().GetString("data-dir")
		jetStream, _ := cmd.Flags().GetBool("jetstream")

		// Set defaults
		if port == 0 {
			port = 4222
		}
		if host == "" {
			host = "0.0.0.0"
		}

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

		fmt.Println("Nats service stopped", service)
	},
}

var natsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get status of the nats service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Nats service status: ...")
	},
}

func init() {
	rootCmd.AddCommand(serviceCmd)

	serviceCmd.AddCommand(predastoreCmd)

	predastoreCmd.PersistentFlags().Int("port", 8443, "Predastore (S3) port")
	predastoreCmd.PersistentFlags().String("host", "0.0.0.0", "Predastore (S3) host")
	predastoreCmd.PersistentFlags().String("base-path", "", "Predastore (S3) base path")
	predastoreCmd.PersistentFlags().String("config-path", "", "Predastore (S3) config path")
	predastoreCmd.PersistentFlags().Bool("debug", false, "Predastore (S3) debug")

	// TLS
	predastoreCmd.PersistentFlags().String("tls-cert", "", "Predastore (S3) TLS certificate")
	predastoreCmd.PersistentFlags().String("tls-key", "", "Predastore (S3) TLS key")

	predastoreCmd.AddCommand(predastoreStartCmd)
	predastoreCmd.AddCommand(predastoreStopCmd)
	predastoreCmd.AddCommand(predastoreStatusCmd)

	serviceCmd.AddCommand(viperblockCmd)

	viperblockCmd.PersistentFlags().String("s3-host", "0.0.0.0:8443", "Predastore (S3) host URI")
	viperblockCmd.PersistentFlags().String("s3-bucket", "predastore", "Predastore (S3) bucket")
	viperblockCmd.PersistentFlags().String("s3-region", "ap-southeast-2", "Predastore (S3) region")
	viperblockCmd.PersistentFlags().String("plugin-path", "/opt/hive/lib/nbdkit-viperblock-plugin.so", "Pathname to the nbdkit viperblockplugin")

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
	natsCmd.PersistentFlags().String("host", "0.0.0.0", "NATS server host")
	natsCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")
	natsCmd.PersistentFlags().String("data-dir", "", "NATS data directory")
	natsCmd.PersistentFlags().Bool("jetstream", false, "Enable JetStream")
}
