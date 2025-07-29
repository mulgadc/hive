package viperblockd

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mulgadc/hive/hive/config"
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

type NBDKitConfig struct {
	Port       int    `json:"port"`
	PidFile    string `json:"pid_file"`
	PluginPath string `json:"plugin_path"`
	Verbose    bool   `json:"verbose"`
	Foreground bool   `json:"foreground"`
	Size       int64  `json:"size"`
	Volume     string `json:"volume"`
	Bucket     string `json:"bucket"`
	Region     string `json:"region"`
	AccessKey  string `json:"access_key"`
	SecretKey  string `json:"secret_key"`
	BaseDir    string `json:"base_dir"`
	Host       string `json:"host"`
	CacheSize  int    `json:"cache_size"`
}

func New(config interface{}) (svc *Service, err error) {
	svc = &Service{
		Config: config.(*Config),
	}

	return svc, nil
}

func (svc *Service) Start() (int, error) {

	utils.WritePidFile(serviceName)
	err := launchService(svc.Config)

	if err != nil {
		slog.Error("Failed to launch service: %v", err)
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
		slog.Error("Failed to connect to NATS: %v", err)
		return err
	}

	// Subscribe to the viperblock.mount subject
	fmt.Println("Connected. Waiting for EBS events")

	nc.Subscribe("ebs.mount", func(msg *nats.Msg) {
		slog.Info("Received message: %s", string(msg.Data))

		// Parse the message
		var ebsRequest config.EBSRequest
		err := json.Unmarshal(msg.Data, &ebsRequest)
		if err != nil {
			slog.Error("Failed to unmarshal message: %v", err)
			return
		}

		fmt.Println("Request =>", ebsRequest)

		var ebsResponse config.EBSResponse
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
			VolumeSize: 1,
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
			slog.Error("Failed to convert port to int: %v", err)
			return
		}

		fmt.Println("NBD URI =>", nbdUri)
		fmt.Println("PORT =>", port)

		// Execute nbdkit
		nbdConfig := NBDKitConfig{
			Port:       port,
			PidFile:    fmt.Sprintf("/tmp/vb-vol-%s.pid", ebsRequest.Name),
			PluginPath: "/home/ben/Development/mulga/viperblock/lib/nbdkit-viperblock-plugin.so",
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
				slog.Error("Failed to execute nbdkit: %v", err)
				// Signal error (no PID) to parent goroutine
				processChan <- 0
				return
			}

			// Signal successful startup w/ PID
			processChan <- pid

			err = cmd.Wait()

			if err != nil {
				slog.Error("Failed to wait for nbdkit: %v", err)
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
			slog.Error("Failed to marshal response: %v", err)
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

func (cfg *NBDKitConfig) Execute() (*exec.Cmd, error) {
	args := []string{
		"-f", // foreground required for Golang plugin via nbdkit
		"-p", strconv.Itoa(cfg.Port),
		"--pidfile", cfg.PidFile,
		cfg.PluginPath,
	}

	if cfg.Verbose {
		args = append(args, "-v")
	}

	// Add plugin-specific arguments
	pluginArgs := []string{
		fmt.Sprintf("size=%d", cfg.Size),
		fmt.Sprintf("volume=%s", cfg.Volume),
		fmt.Sprintf("bucket=%s", cfg.Bucket),
		fmt.Sprintf("region=%s", cfg.Region),
		fmt.Sprintf("access_key=%s", cfg.AccessKey),
		fmt.Sprintf("secret_key=%s", cfg.SecretKey),
		fmt.Sprintf("base_dir=%s", cfg.BaseDir),
		fmt.Sprintf("host=%s", cfg.Host),
		fmt.Sprintf("cache_size=%d", cfg.CacheSize),
	}

	args = append(args, pluginArgs...)

	cmd := exec.Command("nbdkit", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd, cmd.Start()
}
