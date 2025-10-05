package viperblockd

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/nbd"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/viperblock/viperblock"
	"github.com/mulgadc/viperblock/viperblock/backends/s3"

	"github.com/nats-io/nats.go"
)

var serviceName = "viperblock"

type MountedVolume struct {
	Name   string
	Port   int
	Socket string
	PID    int
}

type Config struct {
	ConfigPath     string
	PluginPath     string
	Debug          bool
	NatsHost       string
	MountedVolumes []MountedVolume
	S3Host         string
	Bucket         string
	Region         string
	AccessKey      string
	SecretKey      string
	BaseDir        string

	mu sync.Mutex
}

type Service struct {
	Config *Config
}

//  nbdkit -p 10812 --pidfile /tmp/vb-vol-1.pid ./lib/nbdkit-viperblock-plugin.so -v -f size=67108864 volume=vol-2 bucket=predastore region=ap-southeast-2 access_key="X" secret_key="Y" base_dir="/tmp/vb/" host="https://127.0.0.1:8443" cache_size=0

func New(config interface{}) (svc *Service, err error) {
	svc = &Service{
		Config: config.(*Config),
	}

	return svc, nil
}

func (svc *Service) Start() (int, error) {

	utils.WritePidFile(serviceName, os.Getpid())
	err := launchService(svc.Config)

	if err != nil {
		slog.Error("Failed to launch service", "err", err)
		return 0, err
	}

	return os.Getpid(), nil
}

func (svc *Service) Stop() (err error) {

	err = utils.StopProcess(serviceName)
	return err
}

func (svc *Service) Status() (string, error) {
	return "", nil
}

func (svc *Service) Shutdown() (err error) {
	return svc.Stop()
}

func (svc *Service) Reload() (err error) {
	return nil
}

func launchService(cfg *Config) (err error) {

	// Connect to NATS
	nc, err := nats.Connect(cfg.NatsHost)

	if err != nil {
		slog.Error("Failed to connect to NATS:", "err", err)
		return err
	}

	// Subscribe to the viperblock.mount subject
	fmt.Println("Connected. Waiting for EBS events")

	// TODO: Support volume delete and predastore delete bucket
	nc.Subscribe("ebs.delete", func(msg *nats.Msg) {
		slog.Info("Received message", "data", string(msg.Data))

		// Parse the message
		var ebsRequest config.EBSDeleteRequest
		err := json.Unmarshal(msg.Data, &ebsRequest)
		if err != nil {
			slog.Error("Failed to unmarshal message", "err", err)
			return
		}

		/*

			s3cfg := s3.S3Config{
				VolumeName: ebsRequest.Volume,
				Bucket:     cfg.Bucket,
				Region:     cfg.Region,
				AccessKey:  cfg.AccessKey,
				SecretKey:  cfg.SecretKey,
				Host:       cfg.S3Host,
			}

			vbconfig := viperblock.VB{
				VolumeName: ebsRequest.Volume,
				VolumeSize: 1, // Workaround, calculated on LoadState()
				BaseDir:    cfg.BaseDir,
				Cache: viperblock.Cache{
					Config: viperblock.CacheConfig{
						Size: 0,
					},
				},
				VolumeConfig: viperblock.VolumeConfig{},
			}

			vb, err := viperblock.New(vbconfig, "s3", s3cfg)

		*/

	})

	nc.Subscribe("ebs.unmount", func(msg *nats.Msg) {
		slog.Info("Received message", "data", string(msg.Data))

		// Parse the message
		var ebsRequest config.EBSRequest
		err := json.Unmarshal(msg.Data, &ebsRequest)
		if err != nil {
			slog.Error("Failed to unmarshal message", "err", err)
			return
		}

		// Find the volume in the mounted volumes
		var ebsResponse config.EBSUnMountResponse
		var match bool
		cfg.mu.Lock()
		for _, volume := range cfg.MountedVolumes {

			// TODO: Confirm KVM/QEMU is not using the volume first.
			if volume.Name == ebsRequest.Name {
				ebsResponse = config.EBSUnMountResponse{
					Volume:  volume.Name,
					Mounted: false,
				}

				utils.KillProcess(volume.PID)
				match = true
			}
		}

		cfg.mu.Unlock()

		if !match {
			ebsResponse = config.EBSUnMountResponse{
				Volume: ebsRequest.Name,
				Error:  fmt.Sprintf("Volume %s not found", ebsRequest.Name),
			}
		}

		// Marshal the response
		response, err := json.Marshal(ebsResponse)
		if err != nil {
			slog.Error("Failed to marshal response", "err", err)
			return
		}

		msg.Respond(response)
		nc.Publish("ebs.unmount.response", response)
	})

	nc.Subscribe("ebs.mount", func(msg *nats.Msg) {
		slog.Info("Received message:", "data", string(msg.Data))

		// Parse the message
		var ebsRequest config.EBSRequest
		err := json.Unmarshal(msg.Data, &ebsRequest)
		if err != nil {
			slog.Error("Failed to unmarshal message", "err", err)
			return
		}

		fmt.Println("Request =>", ebsRequest)

		var ebsResponse config.EBSMountResponse
		ebsResponse.Mounted = false

		s3cfg := s3.S3Config{
			VolumeName: ebsRequest.Name,
			Bucket:     cfg.Bucket,
			Region:     cfg.Region,
			AccessKey:  cfg.AccessKey,
			SecretKey:  cfg.SecretKey,
			Host:       cfg.S3Host,
		}

		vbconfig := viperblock.VB{
			VolumeName: ebsRequest.Name,
			VolumeSize: 1, // Workaround, calculated on LoadState()
			BaseDir:    cfg.BaseDir,
			Cache: viperblock.Cache{
				Config: viperblock.CacheConfig{
					Size: 0,
				},
			},
			VolumeConfig: viperblock.VolumeConfig{},
		}

		vb, err := viperblock.New(vbconfig, "s3", s3cfg)

		if err != nil {
			ebsResponse.Error = fmt.Sprintf("Failed to connect to Viperblock store: %v", err)
			// Marshal and send error response immediately
			response, _ := json.Marshal(ebsResponse)
			msg.Respond(response)
			nc.Publish("ebs.mount.response", response)
			return
		}

		if cfg.Debug {
			vb.SetDebug(true)
		}

		// Initialize the backend
		err = vb.Backend.Init()

		if err != nil {
			ebsResponse.Error = err.Error()
			// Marshal and send error response immediately
			response, _ := json.Marshal(ebsResponse)
			msg.Respond(response)
			nc.Publish("ebs.mount.response", response)
			return
		}

		// Next, connect to the volume and confirm the state
		// First, fetch the state from the remote backend
		err = vb.LoadState()

		if err != nil {
			ebsResponse.Error = err.Error()
			// Marshal and send error response immediately
			response, _ := json.Marshal(ebsResponse)
			msg.Respond(response)
			nc.Publish("ebs.mount.response", response)
			return
		}

		// Next, mount the volume using nbdkit

		// Step 1: Find a free port
		nbdUri, err := viperblock.FindFreePort()

		if err != nil {
			ebsResponse.Error = err.Error()
			// Marshal and send error response immediately
			response, _ := json.Marshal(ebsResponse)
			msg.Respond(response)
			nc.Publish("ebs.mount.response", response)
			return
		}

		// Mount the volume
		nbdPort := strings.Split(nbdUri, ":")

		// Port is the last
		port, err := strconv.Atoi(nbdPort[len(nbdPort)-1])
		if err != nil {
			slog.Error("Failed to convert port to int", "err", err)
			return
		}

		fmt.Println("NBD URI =>", nbdUri)
		fmt.Println("PORT =>", port)

		// Execute nbdkit

		nbdPidFile, err := utils.GeneratePidFile(fmt.Sprintf("nbdkit-vol-%s", ebsRequest.Name))
		if err != nil {
			slog.Error("Failed to generate nbdkit pid file:", "err", err)
			return
		}

		nbdConfig := nbd.NBDKitConfig{
			Port:       port,
			PidFile:    nbdPidFile,
			PluginPath: cfg.PluginPath,
			BaseDir:    cfg.BaseDir,
			Host:       cfg.S3Host,
			Verbose:    true,
			Size:       int64(vb.GetVolumeSize()),
			Volume:     ebsRequest.Name,
			Bucket:     cfg.Bucket,
			Region:     cfg.Region,
			AccessKey:  cfg.AccessKey,
			SecretKey:  cfg.SecretKey,
		}

		// Create a unique error channel for this specific mount request
		processChan := make(chan int, 1)
		exitChan := make(chan int, 1)

		// TODO: Improve, use a process manager to track the (multiple) nbdkit process
		go func() {
			fmt.Println("Executing nbdkit")

			cmd, err := nbdConfig.Execute()
			pid := cmd.Process.Pid

			if err != nil {
				slog.Error("Failed to execute nbdkit", "err", err)
				// Signal error (no PID) to parent goroutine
				processChan <- 0
				return
			}

			// Signal successful startup w/ PID
			processChan <- pid

			err = cmd.Wait()

			if err != nil {
				slog.Error("Failed to wait for nbdkit", "err", err)
				exitChan <- 1
				return
			}

			exitCode := cmd.ProcessState.ExitCode()

			exitChan <- exitCode

			fmt.Println("NBDKit exited,, code", exitCode)
		}()

		// Wait for startup result
		pid := <-processChan

		if pid == 0 {
			ebsResponse.Error = "Failed to start nbdkit"
			// Marshal and send error response immediately
			response, _ := json.Marshal(ebsResponse)
			msg.Respond(response)
			nc.Publish("ebs.mount.response", response)
			return
		}

		// Wait for 1 second to confirm nbdkit is running
		time.Sleep(1 * time.Second)

		// Check if nbdkit exited immediately with an error
		select {
		case exitErr := <-exitChan:
			if exitErr != 0 {
				ebsResponse.Error = fmt.Sprintf("nbdkit failed: %v", exitErr)
				response, _ := json.Marshal(ebsResponse)
				msg.Respond(response)
				nc.Publish("ebs.mount.response", response)
				return
			}
		default:
			// nbdkit is still running after 1 second, which means it started successfully
			fmt.Println("NBDKit started successfully and is running")
		}

		ebsResponse.Mounted = true
		ebsResponse.URI = nbdUri

		cfg.mu.Lock()
		cfg.MountedVolumes = append(cfg.MountedVolumes, MountedVolume{
			Name: ebsRequest.Name,
			Port: port,
			PID:  pid,
		})
		cfg.mu.Unlock()

		// Marshal the response
		response, err := json.Marshal(ebsResponse)
		if err != nil {
			slog.Error("Failed to marshal response", "err", err)
			return
		}

		msg.Respond(response)

		nc.Publish("ebs.mount.response", response)

		fmt.Println("Response =>", string(response))
	})

	// Create a channel to receive shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down gracefully...")

	nc.Close()

	// Close all nbdkit processes
	cfg.mu.Lock()
	for _, volume := range cfg.MountedVolumes {
		fmt.Println("Killing nbdkit process", volume.PID)
		utils.KillProcess(volume.PID)
	}
	cfg.mu.Unlock()

	/*
		opts := &server.Options{}

		// Initialize new server with options
		ns, err := server.NewServer(opts)

		if err != nil {
			panic(err)
		}

		go func() {
			ns.Start()
		}()

		// Wait for server to be ready for connections
		if !ns.ReadyForConnections(4 * time.Second) {
			panic("not ready for connection")
		}

		// Create a channel to receive shutdown signals
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		// Wait for shutdown signal
		<-sigChan
		log.Println("Shutting down gracefully...")

		// Graceful shutdown with timeout
		//ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		//defer cancel()

		// Shutdown the server, use a context to timeout after 4 secs
		ns.Shutdown()

		log.Println("Server stopped")

	*/

	return nil

}
