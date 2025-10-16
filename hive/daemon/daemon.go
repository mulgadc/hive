package daemon

import (
	"bufio"
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/kdomanski/iso9660"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	gateway_ec2_instance "github.com/mulgadc/hive/hive/gateway/ec2/instance"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/s3client"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/mulgadc/viperblock/types"
	"github.com/mulgadc/viperblock/viperblock"
	"github.com/mulgadc/viperblock/viperblock/backends/s3"
	"github.com/nats-io/nats.go"
)

// EC2Request represents the EC2 launch request structure
type EC2Request struct {
	Action         string   `json:"Action"`
	ImageID        string   `json:"ImageId"`
	InstanceType   string   `json:"InstanceType"`
	KeyName        string   `json:"KeyName"`
	SecurityGroups []string `json:"SecurityGroups"`
	SubnetID       string   `json:"SubnetId"`
	MaxCount       int      `json:"MaxCount"`
	MinCount       int      `json:"MinCount"`
	Version        string   `json:"Version"`

	// Extended
	BlockDeviceMapping []BlockDeviceMapping `json:"BlockDeviceMapping"`

	UserData string `json:"UserData"`
}

type BlockDeviceMapping struct {
	DeviceName string `json:"DeviceName"`
	EBS        EBS    `json:"EBS"`
}

type EBS struct {
	DeleteOnTermination      bool
	Encrypted                bool
	Iops                     int
	KmsKeyId                 string
	OutpostArn               string
	SnapshotId               string
	Throughput               int
	VolumeInitializationRate int
	VolumeSize               int
	VolumeType               string
}

// InstanceType represents the resource requirements for an EC2 instance type
type InstanceType struct {
	Name         string
	VCPUs        int
	MemoryGB     float64
	Architecture string // e.g., "x86_64", "arm64"
}

// ResourceManager handles the allocation and tracking of system resources
type ResourceManager struct {
	mu            sync.RWMutex
	availableVCPU int
	availableMem  float64
	allocatedVCPU int
	allocatedMem  float64
	instanceTypes map[string]InstanceType
}

// Daemon represents the main daemon service
type Daemon struct {
	config      *config.Config
	natsConn    *nats.Conn
	resourceMgr *ResourceManager
	ctx         context.Context
	cancel      context.CancelFunc
	shutdownWg  sync.WaitGroup

	// Local VM Instances
	Instances vm.Instances

	// NAT Subscriptions
	natsSubscriptions map[string]*nats.Subscription

	mu sync.Mutex
}

const cloudInitUserDataTemplate = `#cloud-config
users:
  - name: {{.Username}}
    shell: /bin/bash
    groups:
      - sudo
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    ssh_authorized_keys:
      - {{.SSHKey}}

hostname: {{.Hostname}}
manage_etc_hosts: true

{{if .UserDataCloudConfig}}

# custom userdata cloud-config
{{.UserDataCloudConfig}}

{{end}}

{{if .UserDataScript}}
write_files:
  - path: /tmp/cloud-init-startup.sh
    permissions: '0755'
    content: |
{{.UserDataScript}}

runcmd:
  - [ "/bin/bash", "/tmp/cloud-init-startup.sh" ]
{{end}}
`

const cloudInitMetaTemplate = `# meta-data
instance-id: {{.InstanceID}}
local-hostname: {{.Hostname}}
`

type CloudInitData struct {
	Username            string
	SSHKey              string
	Hostname            string
	UserDataCloudConfig string
	UserDataScript      string
}

type CloudInitMetaData struct {
	InstanceID string
	Hostname   string
}

// getSystemMemory returns the total system memory in GB
func getSystemMemory() (float64, error) {
	switch runtime.GOOS {
	case "darwin":
		// macOS: use sysctl
		cmd := exec.Command("sysctl", "-n", "hw.memsize")
		output, err := cmd.Output()
		if err != nil {
			return 0, fmt.Errorf("failed to get system memory on macOS: %w", err)
		}
		memBytes, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse memory size on macOS: %w", err)
		}
		return float64(memBytes) / (1024 * 1024 * 1024), nil

	case "linux":
		// Linux: read from /proc/meminfo
		cmd := exec.Command("grep", "MemTotal", "/proc/meminfo")
		output, err := cmd.Output()
		if err != nil {
			return 0, fmt.Errorf("failed to read /proc/meminfo: %w", err)
		}

		// Parse the output (format: "MemTotal:       16384 kB")
		fields := strings.Fields(string(output))
		if len(fields) < 3 {
			return 0, fmt.Errorf("unexpected /proc/meminfo format")
		}

		memKB, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse memory size from /proc/meminfo: %w", err)
		}

		// Convert KB to GB
		return float64(memKB) / (1024 * 1024), nil

	default:
		return 0, fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

// NewResourceManager creates a new resource manager with system capabilities
func NewResourceManager() *ResourceManager {
	// Get system CPU cores
	numCPU := runtime.NumCPU()

	// Get system memory (in GB)
	totalMemGB, err := getSystemMemory()
	if err != nil {
		log.Printf("Warning: Failed to get system memory: %v, using default of 8GB", err)
		totalMemGB = 8.0 // Default to 8GB if we can't get the actual memory
	}

	// Define supported instance types
	// TODO: Determine host capabilities (x86_64 vs arm64) and adjust available instance types accordingly
	instanceTypes := map[string]InstanceType{
		// x86_64
		"t3.nano":    {Name: "t3.nano", VCPUs: 2, MemoryGB: 0.5, Architecture: "x86_64"},
		"t3.micro":   {Name: "t3.micro", VCPUs: 2, MemoryGB: 1.0, Architecture: "x86_64"},
		"t3.small":   {Name: "t3.small", VCPUs: 2, MemoryGB: 2.0, Architecture: "x86_64"},
		"t3.medium":  {Name: "t3.medium", VCPUs: 2, MemoryGB: 4.0, Architecture: "x86_64"},
		"t3.large":   {Name: "t3.large", VCPUs: 2, MemoryGB: 8.0, Architecture: "x86_64"},
		"t3.xlarge":  {Name: "t3.xlarge", VCPUs: 4, MemoryGB: 16.0, Architecture: "x86_64"},
		"t3.2xlarge": {Name: "t3.2xlarge", VCPUs: 8, MemoryGB: 32.0, Architecture: "x86_64"},

		// ARM
		"t4g.nano":    {Name: "t4g.nano", VCPUs: 2, MemoryGB: 0.5, Architecture: "arm64"},
		"t4g.micro":   {Name: "t4g.micro", VCPUs: 2, MemoryGB: 1.0, Architecture: "arm64"},
		"t4g.small":   {Name: "t4g.small", VCPUs: 2, MemoryGB: 2.0, Architecture: "arm64"},
		"t4g.medium":  {Name: "t4g.medium", VCPUs: 2, MemoryGB: 4.0, Architecture: "arm64"},
		"t4g.large":   {Name: "t4g.large", VCPUs: 2, MemoryGB: 8.0, Architecture: "arm64"},
		"t4g.xlarge":  {Name: "t4g.xlarge", VCPUs: 4, MemoryGB: 16.0, Architecture: "arm64"},
		"t4g.2xlarge": {Name: "t4g.2xlarge", VCPUs: 8, MemoryGB: 32.0, Architecture: "arm64"},
	}

	log.Printf("System resources: %d vCPUs, %.2f GB RAM (detected on %s)",
		numCPU, totalMemGB, runtime.GOOS)

	return &ResourceManager{
		availableVCPU: numCPU,
		availableMem:  totalMemGB,
		instanceTypes: instanceTypes,
	}
}

// NewDaemon creates a new daemon instance
func NewDaemon(cfg *config.Config) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())

	// If WalDir is not set, use BaseDir
	if cfg.WalDir == "" {
		cfg.WalDir = cfg.BaseDir
	}

	return &Daemon{
		config:            cfg,
		resourceMgr:       NewResourceManager(),
		ctx:               ctx,
		cancel:            cancel,
		Instances:         vm.Instances{VMS: make(map[string]*vm.VM)},
		natsSubscriptions: make(map[string]*nats.Subscription),
	}
}

// Start initializes and starts the daemon
func (d *Daemon) Start() error {

	var err error

	// Connect to NATS with options
	opts := []nats.Option{
		nats.Token(d.config.NATS.ACL.Token),
		nats.ReconnectWait(time.Second),
		nats.MaxReconnects(-1), // Infinite reconnects
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			log.Printf("NATS disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("NATS reconnected to %s", nc.ConnectedUrl())
		}),
	}

	d.natsConn, err = nats.Connect(d.config.NATS.Host, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	log.Printf("Connected to NATS server at %s", d.config.NATS.Host)

	// Load existing state for VMs
	// Load state from disk
	err = d.LoadState()
	if err != nil {
		slog.Warn("Failed to load state from disk, continuing with empty state", "error", err)
	} else {

		slog.Info("Loaded state from disk", "instance count", len(d.Instances.VMS))

		//d.mu.Lock()
		// Ensure mutex is usable after unmarshalling
		d.Instances.Mu = sync.Mutex{}

		for i := range d.Instances.VMS {
			instance := d.Instances.VMS[i]
			instance.EBSRequests.Mu = sync.Mutex{}
			instance.QMPClient = &qmp.QMPClient{}
			d.Instances.VMS[i] = instance
			//d.Instances.VMS[i].EBSRequests.Mu = sync.Mutex{}
			//			d.Instances.VMS[i].QMPClient.Mu = sync.Mutex{}

			if instance.Attributes.StopInstance {
				slog.Info("Instance flagged as user initiated stop, skipping", "instance", instance.ID)

			} else if instance.Status != "terminated" {
				slog.Info("Launching instance", "instance", instance.ID)
				err = d.LaunchInstance(instance)
				if err != nil {
					slog.Error("Failed to launch instance:", "err", err)
				}
			} else {
				slog.Info("Instance state is terminated, skipping", "instance", instance.ID)
			}

		}
		//d.mu.Unlock()

		// Launch running instances

	}

	log.Printf("Subscribing to subject pattern: %s", "ec2.launch")

	// Subscribe to EC2 events with queue group
	d.natsSubscriptions["ec2.launch"], err = d.natsConn.QueueSubscribe("ec2.launch", "hive-workers", d.handleEC2Launch)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.launch: %w", err)
	}

	// Subscribe to EC2 start instance events
	// TODO: The instance state needs to be shared, not pinned to a single node.
	// TODO: Handle this in a more generic function to group similar commands (start, stop, launch)
	// Subscribe to EC2 events with queue group
	d.natsSubscriptions["ec2.startinstances"], err = d.natsConn.QueueSubscribe("ec2.startinstances", "hive-workers", d.handleEC2StartInstances)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.launch: %w", err)
	}

	// Setup graceful shutdown
	d.setupShutdown()

	// Create a channel to keep the main goroutine alive
	done := make(chan struct{})

	// Wait for shutdown signal
	go func() {
		d.shutdownWg.Wait()
		close(done)
	}()

	// Keep the main goroutine alive until shutdown
	<-done
	return nil
}

// Write the state to disk
func (d *Daemon) WriteState() error {
	d.Instances.Mu.Lock()
	defer d.Instances.Mu.Unlock()

	// Pretty print JSON with indent
	jsonData, err := json.MarshalIndent(d.Instances, "", "  ")

	if err != nil {
		return err
	}

	// Write the state to disk
	configPath := fmt.Sprintf("%s/%s", d.config.BaseDir, "instances.json")
	err = os.WriteFile(configPath, jsonData, 0644)
	if err != nil {
		return err
	}

	return nil
}

// Initalise VMs from state
func (d *Daemon) InitaliseVMs() {

	/*
		d.Instances.Mu.Lock()
		defer d.Instances.Mu.Unlock()

		// Step 1: Loop through each instance
		for i := range d.Instances.VMS {
			instance := d.Instances.VMS[i]

			// Step 2: Mount each EBS volume
			for _, ebsRequest := range instance.EBSRequests.Requests {
				instance.EBSRequests.Mu.Lock()
				defer instance.EBSRequests.Mu.Unlock()

			}

			d.Instances.VMS[i] = instance

		}
	*/

	// Step 2: Loop through each instance and start it
}

// Load state from disk
func (d *Daemon) LoadState() error {
	configPath := fmt.Sprintf("%s/%s", d.config.BaseDir, "instances.json")
	jsonData, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(jsonData, &d.Instances); err != nil {
		return err
	}

	return nil
}

// NATS events

func (d *Daemon) handleEC2StartInstances(msg *nats.Msg) {

	var ec2StartInstance config.EC2StartInstancesRequest

	if err := json.Unmarshal(msg.Data, &ec2StartInstance); err != nil {
		log.Printf("Error unmarshaling EC2 describe request: %v", err)
		return
	}

	slog.Info("EC2 Start Instance Request", "instanceId", ec2StartInstance.InstanceID)

	var ec2StartInstanceResponse config.EC2StartInstancesResponse

	// Check if the instance is running on this node
	d.Instances.Mu.Lock()
	defer d.Instances.Mu.Unlock()

	instance, ok := d.Instances.VMS[ec2StartInstance.InstanceID]

	if !ok {
		slog.Error("EC2 Describe Request - Instance not found", "instanceId", ec2StartInstanceResponse.InstanceID)
		ec2StartInstanceResponse.InstanceID = ec2StartInstance.InstanceID
		ec2StartInstanceResponse.Error = fmt.Sprintf("Instance %s not found", ec2StartInstanceResponse.InstanceID)
		ec2StartInstanceResponse.Respond(msg)
		return
	}

	// Launch the instance

	err := d.LaunchInstance(instance)

	if err != nil {
		ec2StartInstanceResponse.Error = err.Error()
	} else {
		ec2StartInstanceResponse.InstanceID = instance.ID
		ec2StartInstanceResponse.Status = instance.Status
	}

	ec2StartInstanceResponse.Respond(msg)

}

func (d *Daemon) handleEC2Describe(msg *nats.Msg) {

	var ec2DescribeRequest config.EC2DescribeRequest

	if err := json.Unmarshal(msg.Data, &ec2DescribeRequest); err != nil {
		log.Printf("Error unmarshaling EC2 describe request: %v", err)
		return
	}

	slog.Info("EC2 Describe Request", "instanceId", ec2DescribeRequest.InstanceID)

	var ec2DescribeResponse config.EC2DescribeResponse

	// Check if the instance is running on this node
	d.Instances.Mu.Lock()
	defer d.Instances.Mu.Unlock()

	instance, ok := d.Instances.VMS[ec2DescribeRequest.InstanceID]

	if !ok {
		slog.Error("EC2 Describe Request - Instance not found", "instanceId", ec2DescribeRequest.InstanceID)
		ec2DescribeResponse.InstanceID = ec2DescribeRequest.InstanceID
		ec2DescribeResponse.Error = fmt.Sprintf("Instance %s not found", ec2DescribeRequest.InstanceID)
		ec2DescribeResponse.Respond(msg)
		return
	}

	ec2DescribeResponse.InstanceID = instance.ID
	ec2DescribeResponse.Status = instance.Status
	ec2DescribeResponse.Respond(msg)

}

func (d *Daemon) SendQMPCommand(q *qmp.QMPClient, cmd qmp.QMPCommand, instanceId string) (*qmp.QMPResponse, error) {

	// Confirm QMP client is initialized
	if q.Encoder == nil || q.Decoder == nil {
		return nil, fmt.Errorf("QMP client is not initialized")
	}

	// Lock the QMP client
	q.Mu.Lock()
	defer q.Mu.Unlock()

	if err := q.Encoder.Encode(cmd); err != nil {
		return nil, fmt.Errorf("encode error: %w", err)
	}

	for {
		var msg map[string]any
		if err := q.Decoder.Decode(&msg); err != nil {
			return nil, fmt.Errorf("decode error: %w", err)
		}

		if _, ok := msg["event"]; ok {
			slog.Info("QMP event", "event", msg["event"])

			var updatedStatus string

			if msg["event"] == "STOP" {
				updatedStatus = "stopped"
			} else if msg["event"] == "RESUME" {
				updatedStatus = "resuming"
			} else if msg["event"] == "RESET" {
				updatedStatus = "restarting"
			} else if msg["event"] == "POWERDOWN" {
				updatedStatus = "powering_down"
			}

			if updatedStatus != "" {

				// Update the instance status
				d.Instances.Mu.Lock()
				instance, ok := d.Instances.VMS[instanceId]
				if !ok {
					slog.Info("QMP Status - Instance not found", "id", instanceId)
					continue
				}

				instance.Status = updatedStatus

				d.Instances.VMS[instanceId] = instance
				d.Instances.Mu.Unlock()
			}

			// Optional: handle events here
			continue
		}
		if errObj, ok := msg["error"].(map[string]interface{}); ok {
			return nil, fmt.Errorf("QMP error: %s: %s", errObj["class"], errObj["desc"])
		}
		if _, ok := msg["return"]; ok {
			respBytes, _ := json.Marshal(msg)
			var resp qmp.QMPResponse
			if err := json.Unmarshal(respBytes, &resp); err != nil {
				return nil, fmt.Errorf("unmarshal error: %w", err)
			}
			return &resp, nil
		}
	}
}

// handleEC2Events processes incoming EC2 events
func (d *Daemon) handleEC2Events(msg *nats.Msg) {

	var command qmp.Command
	if err := json.Unmarshal(msg.Data, &command); err != nil {
		log.Printf("Error unmarshaling QMP command: %v", err)
		return
	}

	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	instance, ok := d.Instances.VMS[command.ID]

	if !ok {
		// TODO: Improve, return error
		slog.Warn("Instance is not running on this node", "id", command.ID)
		msg.Respond(nil)
		return
	}

	// Send the command to the instance
	resp, err := d.SendQMPCommand(instance.QMPClient, command.QMPCommand, instance.ID)

	if err != nil {
		slog.Error("Failed to send QMP command", "err", err)
		return
	}

	slog.Debug("RAW QMP Response", "resp", string(resp.Return))

	// Unmarshal the response
	target, ok := qmp.CommandResponseTypes[command.QMPCommand.Execute]
	if !ok {
		slog.Warn("Unhandled QMP command", "cmd", command.QMPCommand.Execute)
		return
	}

	if err := json.Unmarshal(resp.Return, target); err != nil {
		slog.Error("Failed to unmarshal QMP response", "cmd", command.QMPCommand.Execute, "err", err)
		return
	}

	msg.Respond(resp.Return)

	// Update the instance attributes
	d.Instances.Mu.Lock()
	instance.Attributes = command.Attributes
	d.Instances.Mu.Unlock()

}

// handleEC2Launch processes incoming EC2 launch requests
func (d *Daemon) handleEC2Launch(msg *nats.Msg) {
	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	// Initialize runInstancesInput before unmarshaling into it
	runInstancesInput := &ec2.RunInstancesInput{}
	var reservation ec2.Reservation

	//var ec2Req EC2Request
	//var ec2Response config.EC2Response
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(runInstancesInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match RunInstancesInput")
		return
	}

	// Validate inputs
	err := gateway_ec2_instance.ValidateRunInstancesInput(runInstancesInput)

	if err != nil {
		slog.Error("handleEC2Launch validation failed", "err", awserrors.ErrorValidationError)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorValidationError)
		msg.Respond(errResp)
		return

	}

	slog.Info("Processing RunInstances request for instance type", "instanceType", *runInstancesInput.InstanceType)

	// Check if instance type is supported
	instanceType, exists := d.resourceMgr.instanceTypes[*runInstancesInput.InstanceType]
	if !exists {
		slog.Error("handleEC2Launch instance lookup", "err", awserrors.ErrorInvalidInstanceType, "InstanceType", *runInstancesInput.InstanceType)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInvalidInstanceType)
		msg.Respond(errResp)
		return
	}

	// Check if we have enough resources
	if !d.resourceMgr.canAllocate(instanceType) {
		slog.Error("handleEC2Launch canAllocate", "err", awserrors.ErrorInsufficientInstanceCapacity, "InstanceType", *runInstancesInput.InstanceType)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInsufficientInstanceCapacity)
		msg.Respond(errResp)
		return
	}

	// Allocate resources
	if err := d.resourceMgr.allocate(instanceType); err != nil {
		slog.Error("handleEC2Launch allocate", "err", awserrors.ErrorInsufficientInstanceCapacity, "InstanceType", *runInstancesInput.InstanceType)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInsufficientInstanceCapacity)
		msg.Respond(errResp)
		return
	}

	// Create a new VM instance
	slog.Info("Launching EC2 instance", "instance", instanceType)

	reservation, err = d.launchEC2Instance(runInstancesInput, msg)

	if err != nil {
		slog.Error("handleEC2Launch launchEC2Instance", "err", awserrors.ErrorServerInternal)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		d.resourceMgr.deallocate(instanceType)
		return
	}

	// TODO: Confirm and cleanup, launchEC2Instance responds
	//jsonResponse, err := json.Marshal(reservation)
	//msg.Respond(jsonResponse)

	slog.Info("handleEC2Launch launched", "instanceId", reservation.Instances[0].InstanceId)

}

func (d *Daemon) setupShutdown() {
	d.shutdownWg.Add(1)
	go func() {
		defer d.shutdownWg.Done()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

		<-sigChan
		log.Println("Received shutdown signal, cleaning up...")

		// Cancel context
		d.cancel()

		// Signal to shutdown each VM
		var wg sync.WaitGroup

		for _, instance := range d.Instances.VMS {
			instance := instance // capture loop variable
			wg.Add(1)

			go func() {
				defer wg.Done()

				// Send shutdown command
				_, err := d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{Execute: "system_powerdown"}, instance.ID)

				if err != nil {
					slog.Error("Failed to send system_powerdown", "id", instance.ID, "err", err)
					return
				}

				// Wait for PID file removal
				err = utils.WaitForPidFileRemoval(instance.ID, 60*time.Second)

				if err != nil {
					slog.Error("Timeout waiting for PID file removal", "id", instance.ID, "err", err)

					// Try force killing the process
					pid, err := utils.ReadPidFile(instance.ID)
					if err != nil {
						slog.Error("Failed to read PID file", "id", instance.ID, "err", err)
					} else {
						slog.Info("Killing process", "pid", pid, "id", instance.ID)
						// Send SIG directly if QMP fails
						utils.KillProcess(pid)
					}
				}

				// Unmount all EBS volumes
				instance.EBSRequests.Mu.Lock()
				defer instance.EBSRequests.Mu.Unlock()

				for _, ebsRequest := range instance.EBSRequests.Requests {

					// Send the volume payload as JSON
					ebsUnMountRequest, err := json.Marshal(ebsRequest)

					if err != nil {
						slog.Error("Failed to marshal volume payload", "err", err)
						continue
					}

					msg, err := d.natsConn.Request("ebs.unmount", ebsUnMountRequest, 30*time.Second)
					if err != nil {
						slog.Error("Failed to unmount volume", "name", ebsRequest.Name, "id", instance.ID, "err", err)
					} else {
						slog.Info("Unmounted Viperblock volume", "id", instance.ID, "data", string(msg.Data))
					}
				}
			}()
		}

		// Wait for all shutdowns to finish
		wg.Wait()

		// Final cleanup
		for _, sub := range d.natsSubscriptions {
			// Unsubscribe from each subscription
			log.Printf("Unsubscribing from NATS: %s", sub.Subject)
			if err := sub.Unsubscribe(); err != nil {
				log.Printf("Error unsubscribing from NATS: %v", err)
			}

		}

		// Close NATS connection
		d.natsConn.Close()

		// Write the state to disk
		err := d.WriteState()
		if err != nil {
			slog.Error("Failed to write state to disk", "err", err)
		}

		// Wait for any ongoing operations to complete
		// TODO: Implement cleanup of running instances
		log.Println("Shutdown complete")
	}()
}

// TODO: Support multiple instances in the single request
func (d *Daemon) launchEC2Instance(runInstancesInput *ec2.RunInstancesInput, msg *nats.Msg) (reservation ec2.Reservation, err error) {

	err = gateway_ec2_instance.ValidateRunInstancesInput(runInstancesInput)

	if err != nil {
		return
	}

	// Validate input
	instanceType := d.resourceMgr.instanceTypes[*runInstancesInput.InstanceType]

	if instanceType.Name == "" {
		return reservation, errors.New(awserrors.ErrorInvalidInstanceType)
	}

	var size int = 4 * 1024 * 1024 * 1024 // 4GB default size
	var deviceName string
	var volumeType string
	var iops int

	var imageId string
	var snapshotId string

	instanceId := vm.GenerateEC2InstanceID()

	// Add state for our new instance
	var instance = &vm.VM{
		ID:           instanceId,
		Status:       "provisioning",
		InstanceType: *runInstancesInput.InstanceType,
	}

	// Add reservation ID
	reservation.SetReservationId(vm.GenerateEC2ReservationID())

	// TODO: Loop through multiple instance creation based on MinCount / MaxCount
	// Respond with the instance ID and status, polling required to track status
	reservation.Instances = make([]*ec2.Instance, 1)
	// Initialize the instance before setting fields
	reservation.Instances[0] = &ec2.Instance{
		State: &ec2.InstanceState{},
	}
	// TODO: Consider using Set methods
	reservation.Instances[0].SetInstanceId(instance.ID)
	reservation.Instances[0].State.SetCode(0)
	reservation.Instances[0].State.SetName("pending")

	// Respond to NATS immediately, to reduce lag while the instance is provisioning.
	jsonResponse, err := json.Marshal(reservation)
	// TODO: Consider multiple instance types
	msg.Respond(jsonResponse)

	// TODO: Support multiple mounts per request
	if len(reservation.Instances[0].BlockDeviceMappings) > 0 {

		size = int(*runInstancesInput.BlockDeviceMappings[0].Ebs.VolumeSize)
		deviceName = *runInstancesInput.BlockDeviceMappings[0].DeviceName
		volumeType = *runInstancesInput.BlockDeviceMappings[0].Ebs.VolumeType
		iops = int(*runInstancesInput.BlockDeviceMappings[0].Ebs.Iops)

	}

	// Check if the image starts with ami-
	if strings.HasPrefix(*runInstancesInput.ImageId, "ami-") {
		// Generate a random number to append to the volume ID ( 8 digits )
		randomNumber := rand.Intn(100_000_000)

		imageId = viperblock.GenerateVolumeID("vol", fmt.Sprintf("%d-%s", randomNumber, *runInstancesInput.ImageId), "predastore", time.Now().Unix())
		snapshotId = *runInstancesInput.ImageId
	} else {
		// Allow creating an instance with an existing ImageID
		imageId = *runInstancesInput.ImageId
	}

	// Pre-flight, confirm if the instance is already running (TODO)

	// CONFIRM: All Viperblock AMI and volumes stored in a system S3 bucket, vs the individual users account.

	// Step 1: Confirm if the volume already exists

	cfg := s3.S3Config{
		VolumeName: imageId,
		VolumeSize: uint64(size),
		Bucket:     d.config.Predastore.Bucket,
		Region:     d.config.Predastore.Region,
		AccessKey:  d.config.AccessKey,
		SecretKey:  d.config.SecretKey,
		Host:       d.config.Predastore.Host,
	}

	volumeConfig := viperblock.VolumeConfig{
		VolumeMetadata: viperblock.VolumeMetadata{
			VolumeID:   imageId,
			SizeGiB:    uint64(size / 1024 / 1024 / 1024),
			CreatedAt:  time.Now(),
			DeviceName: deviceName,
			VolumeType: volumeType,
			IOPS:       iops,
			SnapshotID: snapshotId,
		},
	}

	vbconfig := viperblock.VB{
		VolumeName: imageId,
		VolumeSize: uint64(size),
		BaseDir:    d.config.WalDir,
		Cache: viperblock.Cache{
			Config: viperblock.CacheConfig{
				Size: 0,
			},
		},
		VolumeConfig: volumeConfig,
	}

	vb, err := viperblock.New(vbconfig, "s3", cfg)
	if err != nil {
		slog.Error("Failed to connect to Viperblock store", "err", err)
		return reservation, errors.New(awserrors.ErrorServerInternal)
	}

	vb.SetDebug(true)

	// Initialize the backend
	err = vb.Backend.Init()

	if err != nil {
		slog.Error("Failed to initialize backend", "err", err)
		return reservation, errors.New(awserrors.ErrorServerInternal)
	}

	// Load the state from the remote backend
	//err = vb.LoadState()
	_, err = vb.LoadStateRequest("")

	// Step 2: If launching from an AMI and the volume doesn't exist, clone the AMI to our new volume

	if err != nil {

		slog.Info("Volume does not yet exist, creating from EFI ...")

		// Open the chunk WAL
		err = vb.OpenWAL(&vb.WAL, fmt.Sprintf("%s/%s", vb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALChunk, vb.WAL.WallNum.Load(), vb.GetVolume())))

		if err != nil {
			slog.Error("Failed to load WAL", "err", err)
			return reservation, errors.New(awserrors.ErrorServerInternal)
		}

		// Open the block to object WAL
		err = vb.OpenWAL(&vb.BlockToObjectWAL, fmt.Sprintf("%s/%s", vb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALBlock, vb.BlockToObjectWAL.WallNum.Load(), vb.GetVolume())))

		if err != nil {
			slog.Error("Failed to load block WAL", "err", err)
			return reservation, errors.New(awserrors.ErrorServerInternal)
		}

		amiCfg := s3.S3Config{
			VolumeName: *runInstancesInput.ImageId,
			VolumeSize: uint64(size),
			Bucket:     d.config.Predastore.Bucket,
			Region:     d.config.Predastore.Region,
			AccessKey:  d.config.AccessKey,
			SecretKey:  d.config.SecretKey,
			Host:       d.config.Predastore.Host,
		}

		amiVbConfig := viperblock.VB{
			VolumeName: *runInstancesInput.ImageId,
			VolumeSize: uint64(size),
			BaseDir:    d.config.WalDir,
			Cache: viperblock.Cache{
				Config: viperblock.CacheConfig{
					Size: 0,
				},
			},
			VolumeConfig: volumeConfig,
		}

		amiVb, err := viperblock.New(amiVbConfig, "s3", amiCfg)

		if err != nil {
			slog.Error("Failed to connect to Viperblock store for AMI", "err", err)
			return reservation, errors.New(awserrors.ErrorServerInternal)

		}

		// Initialize the backend
		slog.Debug("Initializing AMI Viperblock store backend")
		err = amiVb.Backend.Init()

		if err != nil {
			slog.Error("Could not connect to AMI Viperblock store", "err", err)
			return reservation, errors.New(awserrors.ErrorServerInternal)
		}

		slog.Debug("Loading state for AMI Viperblock store")
		err = amiVb.LoadState()

		if err != nil {
			slog.Error("Could not load state for AMI Viperblock store", "err", err)
			return reservation, errors.New(awserrors.ErrorServerInternal)

		}

		err = amiVb.LoadBlockState()

		if err != nil {
			slog.Error("Failed to load block state", "err", err)
			return reservation, errors.New(awserrors.ErrorServerInternal)

		}

		slog.Debug("Starting to clone AMI to new volume")

		var block uint64 = 0
		nullBlock := make([]byte, vb.BlockSize)

		// Read each block from the AMI, write to our new volume, skipping null blocks

		for {

			if block*uint64(vb.BlockSize) >= amiVb.VolumeSize {
				slog.Debug("Reached end of AMI")
				break
			}

			// Read 1MB
			data, err := amiVb.ReadAt(block*uint64(vb.BlockSize), uint64(vb.BlockSize)*1024)

			if err != nil && err != viperblock.ZeroBlock {
				slog.Error("Failed to read block from AMI source", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)

			}

			numBlocks := len(data) / int(vb.BlockSize)

			// Write individual blocks to the new volume
			for i := 0; i < numBlocks; i++ {

				// Check if the input is a Zero block
				if bytes.Equal(data[i*int(vb.BlockSize):(i+1)*int(vb.BlockSize)], nullBlock) {
					//fmt.Printf("Null block found at %d, skipping\n", block)
					block++
					continue
				}

				vb.WriteAt(block*uint64(vb.BlockSize), data[i*int(vb.BlockSize):(i+1)*int(vb.BlockSize)])
				block++

				// Flush every 4MB
				if block%uint64(vb.BlockSize) == 0 {
					slog.Debug("Flush", "block", block)
					vb.Flush()
					vb.WriteWALToChunk(true)
				}
			}

		}

		err = vb.Close()

		if err != nil {
			slog.Error("Failed to close Viperblock store", "err", err)
			return reservation, errors.New(awserrors.ErrorServerInternal)
		}

		err = vb.RemoveLocalFiles()

		if err != nil {
			slog.Warn("Failed to remove local files", "err", err)
		}

		// New volume is cloned.

	}

	// Append root volume
	instance.EBSRequests.Mu.Lock()
	instance.EBSRequests.Requests = append(instance.EBSRequests.Requests, config.EBSRequest{
		Name: vbconfig.VolumeName,
		Boot: true,
	})
	instance.EBSRequests.Mu.Unlock()

	//var walNum uint64

	// Step 3: Create the EFI partition if it does not yet exist

	efiVolumeName := fmt.Sprintf("%s-efi", imageId)
	efiSize := 64 * 1024 * 1024 // 64MB

	efiCfg := s3.S3Config{
		VolumeName: efiVolumeName,
		VolumeSize: uint64(efiSize),
		Bucket:     d.config.Predastore.Bucket,
		Region:     d.config.Predastore.Region,
		AccessKey:  d.config.AccessKey,
		SecretKey:  d.config.SecretKey,
		Host:       d.config.Predastore.Host,
	}

	efiVbConfig := viperblock.VB{
		VolumeName: efiVolumeName,
		VolumeSize: uint64(efiSize),
		BaseDir:    d.config.WalDir,
		Cache: viperblock.Cache{
			Config: viperblock.CacheConfig{
				Size: 0,
			},
		},
		VolumeConfig: volumeConfig,
	}

	efiVb, err := viperblock.New(efiVbConfig, "s3", efiCfg)

	if err != nil {
		slog.Error("Could not create EFI viperblock")
		return reservation, errors.New(awserrors.ErrorServerInternal)
	}

	efiVb.SetDebug(true)

	if err != nil {
		slog.Error("Failed to connect to Viperblock store for AMI", "err", err)
		return reservation, errors.New(awserrors.ErrorServerInternal)
	}

	// Initialize the backend
	slog.Debug("Initializing EFI Viperblock store backend")
	err = efiVb.Backend.Init()

	if err != nil {
		slog.Error("Failed to initialize EFI Viperblock store backend", "err", err)
		return reservation, errors.New(awserrors.ErrorServerInternal)
	}

	// Load the state from the remote backend
	//err = vb.LoadState()
	_, err = efiVb.LoadStateRequest("")

	slog.Info("LoadStateRequest", "error", err)

	// Step 2: If launching from an AMI and the volume doesn't exist, clone the AMI to our new volume

	if err != nil {

		slog.Info("Volume does not yet exist, creating from EFI disk ...")

		// Open the chunk WAL
		err = efiVb.OpenWAL(&efiVb.WAL, fmt.Sprintf("%s/%s", efiVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALChunk, efiVb.WAL.WallNum.Load(), efiVb.GetVolume())))

		if err != nil {
			slog.Error("Failed to load WAL", "err", err)
			return reservation, errors.New(awserrors.ErrorServerInternal)
		}

		// Open the block to object WAL
		err = vb.OpenWAL(&efiVb.BlockToObjectWAL, fmt.Sprintf("%s/%s", efiVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALBlock, efiVb.BlockToObjectWAL.WallNum.Load(), efiVb.GetVolume())))

		if err != nil {
			slog.Error("Failed to load block WAL", "err", err)
			return reservation, errors.New(awserrors.ErrorServerInternal)
		}

		// Write an empty block to the EFI volume
		efiVb.WriteAt(0, make([]byte, efiVb.BlockSize))

		// Flush
		efiVb.Flush()

	}

	slog.Info("Closing EFI")

	err = efiVb.Close()

	slog.Info("Close", "error", err)

	if err != nil {
		slog.Error("Failed to close EFI Viperblock store", "err", err)
	}

	err = efiVb.RemoveLocalFiles()

	if err != nil {
		slog.Error("Failed to remove local files", "err", err)
	}

	instance.EBSRequests.Mu.Lock()
	instance.EBSRequests.Requests = append(instance.EBSRequests.Requests, config.EBSRequest{
		Name: efiVb.VolumeName,
		Boot: false,
		EFI:  true,
	})
	instance.EBSRequests.Mu.Unlock()

	// Step 4: Create the cloud-init volume, with the specified SSH key and attributes

	keyName := *runInstancesInput.KeyName
	userData := *runInstancesInput.UserData

	if keyName != "" || userData != "" {

		slog.Info("Creating cloud-init volume")

		cloudInitVolumeName := fmt.Sprintf("%s-cloudinit", imageId)
		cloudInitSize := 1 * 1024 * 1024 // 1MB

		cloudInitCfg := s3.S3Config{
			VolumeName: cloudInitVolumeName,
			VolumeSize: uint64(cloudInitSize),
			Bucket:     d.config.Predastore.Bucket,
			Region:     d.config.Predastore.Region,
			AccessKey:  d.config.AccessKey,
			SecretKey:  d.config.SecretKey,
			Host:       d.config.Predastore.Host,
		}

		cloudInitVbConfig := viperblock.VB{
			VolumeName: cloudInitVolumeName,
			VolumeSize: uint64(cloudInitSize),
			BaseDir:    d.config.WalDir,
			Cache: viperblock.Cache{
				Config: viperblock.CacheConfig{
					Size: 0,
				},
			},
			VolumeConfig: volumeConfig,
		}

		cloudInitVb, err := viperblock.New(cloudInitVbConfig, "s3", cloudInitCfg)

		if err != nil {
			slog.Error("Could not create cloudinit viperblock")
			return reservation, errors.New(awserrors.ErrorServerInternal)
		}

		// TODO: Set debug flag config
		cloudInitVb.SetDebug(true)

		if err != nil {
			slog.Error("Failed to connect to Viperblock store for AMI", "err", err)
			return reservation, errors.New(awserrors.ErrorServerInternal)
		}

		// Initialize the backend
		slog.Debug("Initializing cloud-init Viperblock store backend")
		err = cloudInitVb.Backend.Init()

		if err != nil {
			slog.Error("Could not init backend")
			return reservation, errors.New(awserrors.ErrorServerInternal)
		}

		// Load the state from the remote backend
		//err = vb.LoadState()
		_, err = cloudInitVb.LoadStateRequest("")

		// Step 2: If launching from an AMI and the volume doesn't exist, clone the AMI to our new volume

		if err != nil {

			slog.Info("Volume does not yet exist, creating from cloud-init disk ...")

			// Open the chunk WAL
			err = cloudInitVb.OpenWAL(&cloudInitVb.WAL, fmt.Sprintf("%s/%s", cloudInitVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALChunk, cloudInitVb.WAL.WallNum.Load(), cloudInitVb.GetVolume())))

			if err != nil {
				slog.Error("Failed to load WAL", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			// Open the block to object WAL
			err = cloudInitVb.OpenWAL(&cloudInitVb.BlockToObjectWAL, fmt.Sprintf("%s/%s", cloudInitVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALBlock, cloudInitVb.BlockToObjectWAL.WallNum.Load(), cloudInitVb.GetVolume())))

			if err != nil {
				slog.Error("Failed to load block WAL", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			// Create the cloud-init disk
			writer, err := iso9660.NewWriter()
			if err != nil {
				slog.Error("failed to create writer", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			defer writer.Cleanup()

			// Inject our data into the cloud-init template.
			instanceId := generateInstanceID()
			hostname := generateHostname(instanceId)

			// Retrieve SSH pubkey from S3
			// Connect to S3 to retrieve EC2 attributes (e.g SSH key)
			s3c := s3client.New(s3client.S3Config{
				AccessKey: d.config.AccessKey,
				SecretKey: d.config.SecretKey,
				Host:      d.config.Predastore.Host,
				Bucket:    d.config.Predastore.Bucket,
				Region:    d.config.Predastore.Region,
			})

			err = s3c.Init()

			if err != nil {
				slog.Error("failed to initialize S3 client", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			sshKey, err := s3c.Read(fmt.Sprintf("/ssh/%s", keyName))
			if err != nil {
				slog.Error("failed to read SSH key", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			userData := CloudInitData{
				Username: "ec2-user",
				SSHKey:   string(sshKey), // provided ssh key
				Hostname: hostname,
			}

			var buf bytes.Buffer
			t := template.Must(template.New("cloud-init").Parse(cloudInitUserDataTemplate))

			if err := t.Execute(&buf, userData); err != nil {
				slog.Error("failed to render template", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			//slog.Debug("user-data", "data", buf.String())

			// Add user-data
			err = writer.AddFile(&buf, "user-data")
			if err != nil {
				slog.Error("failed to add file", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			// Add meta-data
			metaData := CloudInitMetaData{
				InstanceID: instanceId,
				Hostname:   hostname,
			}

			t = template.Must(template.New("meta-data").Parse(cloudInitMetaTemplate))

			buf.Reset()

			if err := t.Execute(&buf, metaData); err != nil {
				slog.Error("failed to render template", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			//slog.Debug("meta-data", buf.String())

			err = writer.AddFile(&buf, "meta-data")
			if err != nil {
				slog.Error("failed to add file", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			// Store temp file
			tempFile, err := os.CreateTemp("", "cloud-init-*.iso")

			if err != nil {
				slog.Error("Could not create cloud-init temp file")
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			slog.Info("Created temp ISO file", "file", tempFile.Name())

			outputFile, err := os.OpenFile(tempFile.Name(), os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
			if err != nil {
				slog.Error("failed to create file", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			// Requires cidata volume label for cloud-init to recognize
			err = writer.WriteTo(outputFile, "cidata")

			if err != nil {
				slog.Error("failed to write ISO image", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			err = writer.Cleanup()

			if err != nil {
				slog.Error("failed to cleanup writer", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			err = outputFile.Close()

			if err != nil {
				slog.Error("failed to close output file", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			isoData, err := os.ReadFile(tempFile.Name())

			if err != nil {
				slog.Error("failed to read ISO image:", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			err = cloudInitVb.WriteAt(0, isoData)

			if err != nil {
				slog.Error("failed to write ISO image to viperblock volume", "err", err)
				return reservation, errors.New(awserrors.ErrorServerInternal)
			}

			// Flush
			cloudInitVb.Flush()
			cloudInitVb.WriteWALToChunk(true)

			// Remove the temp ISO file

			err = os.Remove(tempFile.Name())

			if err != nil {
				slog.Error("Failed to remove temp file", "err", err)
			}

		}

		err = cloudInitVb.Close()

		if err != nil {
			slog.Error("Failed to close cloud-init Viperblock store", "err", err)
		}

		err = cloudInitVb.RemoveLocalFiles()

		if err != nil {
			slog.Error("Failed to remove local files", "err", err)
		}

		instance.EBSRequests.Mu.Lock()
		instance.EBSRequests.Requests = append(instance.EBSRequests.Requests, config.EBSRequest{
			Name:      cloudInitCfg.VolumeName,
			Boot:      false,
			CloudInit: true,
		})
		instance.EBSRequests.Mu.Unlock()

	}

	// Step 5: Mount each volume via NBD, confirm running as expected for pre-flight checks.
	// TODO: Run a goroutine for each volume

	err = d.LaunchInstance(instance)

	if err != nil {
		slog.Error("Could not launch instance", "err", err)
		return reservation, errors.New(awserrors.ErrorServerInternal)
	}

	// Step 10: Return the unique instance ID on success, ALREADY set.
	//ec2Response.InstanceID = instance.ID
	// TODO: Use AWS style hostname, ip-<a-b-c-d>.<region>.compute.internal
	//ec2Response.Hostname = instance.ID
	//ec2Response.Success = true

	return reservation, nil
}

func (d *Daemon) CreateQMPClient(instance *vm.VM) (err error) {

	// Create a new QMP client to communicate with the instance
	instance.QMPClient, err = qmp.NewQMPClient(instance.Config.QMPSocket)

	if err != nil {
		slog.Error("Could not connect to QMP")
		return err
	}

	// Send qmp_capabilities handshake to init
	_, err = d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{Execute: "qmp_capabilities"}, instance.ID)

	// Simple heartbeat to confirm QMP and the instance is running / healthy
	go func() {
		for {
			time.Sleep(30 * time.Second)
			slog.Info("QMP heartbeat", "instance", instance.ID)
			status, err := d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{Execute: "query-status"}, instance.ID)

			if err != nil {
				slog.Error("Failed to send QMP command", "err", err)

				// Check if the instance is stopping, mark as stopped
				d.Instances.Mu.Lock()
				defer d.Instances.Mu.Unlock()

				if instance.Status == "powering_down" {
					instance.Status = "stopped"

					// TODO: Improve, confirm QEMU PID removed
					slog.Info("QMP Status - Instance stopped, exiting heartbeat", "id", instance.ID)

					// TODO: Improve, move to SendQMPCommand
					// Unsubscribe from the NATS subject
					slog.Info("Unsubscribing from NATS subject", "instance", instance.ID)
					d.natsSubscriptions[fmt.Sprintf("ec2.cmd.%s", instance.ID)].Unsubscribe()
					d.natsSubscriptions[fmt.Sprintf("ec2.describe.%s", instance.ID)].Unsubscribe()

					// Close the QMP client connection
					slog.Info("Closing QMP client connection", "instance", instance.ID)
					instance.QMPClient.Conn.Close()

					// Exit the goroutine
					break
				}

				continue
			}

			slog.Info("QMP status", "status", string(status.Return))

		}
	}()

	if err != nil {
		slog.Error("Failed to create QMP client", "err", err)
		return err
	}

	return nil

}

func (d *Daemon) LaunchInstance(instance *vm.VM) (err error) {

	// First, confirm if the instance is already running
	pid, err := utils.ReadPidFile(instance.ID)

	if pid > 0 {
		process, err := os.FindProcess(pid)
		if err != nil {
			return err
		}

		// Send a 0 signal to confirm process is running
		err = process.Signal(syscall.Signal(0))
		if err != nil {
			slog.Error("Instance is already running", "InstanceID", instance.ID, "pid", pid)
			return errors.New("instance is already running")
		}
	}

	// Loop through each volume in volumes
	err = d.MountVolumes(instance)

	if err != nil {
		slog.Error("Failed to mount volumes", "err", err)
		return err
	}

	// Step 6: Launch the instance via QEMU/KVM
	err = d.StartInstance(instance)

	if err != nil {
		slog.Error("Failed to launch instance", "err", err)
		return err
	}

	// Step 7: Create QMP client to communicate with the instance
	err = d.CreateQMPClient(instance)

	if err != nil {
		slog.Error("Failed to create QMP client", "err", err)
		return err
	}

	// Step 8: Subscribe to start/stop/shutdown events
	d.mu.Lock()
	defer d.mu.Unlock()

	d.natsSubscriptions[instance.ID], err = d.natsConn.QueueSubscribe(fmt.Sprintf("ec2.cmd.%s", instance.ID), "hive-events", d.handleEC2Events)

	if err != nil {
		slog.Error("failed to subscribe to NATS", "err", err)
		return err
	}

	d.natsSubscriptions[fmt.Sprintf("ec2.describe.%s", instance.ID)], err = d.natsConn.QueueSubscribe(fmt.Sprintf("ec2.describe.%s", instance.ID), "hive-events", d.handleEC2Describe)

	if err != nil {
		slog.Error("Failed to subscribe to NATS ec2.describe", "id", instance.ID, "err", err)
		return err
	}

	// Step 9: Update the instance metadata for running state and volume attached
	// Marshal to a JSON file
	// Update state
	d.Instances.Mu.Lock()
	// Update to running state
	instance.Status = "running"

	d.Instances.VMS[instance.ID] = instance
	d.Instances.Mu.Unlock()

	err = d.WriteState()

	if err != nil {
		slog.Error("Failed to marshal launchVm", "err", err)
		return err
	}

	return nil
}

func (d *Daemon) StartInstance(instance *vm.VM) error {

	pidFile, err := utils.GeneratePidFile(instance.ID)

	if err != nil {
		slog.Error("Failed to generate PID file", "err", err)
		return err
	}

	var instanceType = d.resourceMgr.instanceTypes[instance.InstanceType]

	instance.Config = vm.Config{
		Name:         instance.ID,
		Daemonize:    true,
		PIDFile:      pidFile,
		EnableKVM:    true,
		NoGraphic:    false,
		MachineType:  "ubuntu",
		Serial:       "pty",
		CPUType:      "host",
		Memory:       int(instanceType.MemoryGB) * 1024,
		CPUCount:     instanceType.VCPUs,
		Architecture: instanceType.Architecture,
	}

	// Loop through each volume in volumes
	instance.EBSRequests.Mu.Lock()

	for _, v := range instance.EBSRequests.Requests {

		drive := vm.Drive{}

		drive.File = v.NBDURI
		// Cleanup hostname to point to nbd://localhost from [::]
		// TODO: Make NBD host config defined, or remote NBD server if not running locally.
		drive.File = strings.Replace(drive.File, "[::]", "nbd://127.0.0.1", 1)

		if v.Boot {
			drive.Format = "raw"
			drive.If = "none"
			drive.Media = "disk"
			drive.ID = "os"

			instance.Config.Devices = append(instance.Config.Devices, vm.Device{
				Value: fmt.Sprintf("virtio-blk-pci,drive=%s,bootindex=1", drive.ID),
			})
		}

		if v.CloudInit {
			drive.Format = "raw"
			drive.If = "virtio"
			drive.Media = "cdrom"
			drive.ID = "cloudinit"
		}

		// TODO: Add EFI support

		if v.EFI {
			continue
		}

		if v.EFI {
			drive.Format = "raw"
			drive.If = "pflash"
			drive.Media = "disk"
			drive.ID = "efi"
		}

		instance.Config.Drives = append(instance.Config.Drives, drive)
	}
	instance.EBSRequests.Mu.Unlock()

	instance.Config.NetDevs = append(instance.Config.NetDevs, vm.NetDev{
		Value: "user,id=net0",
	})

	instance.Config.Devices = append(instance.Config.Devices, vm.Device{
		Value: "virtio-rng-pci",
	})

	// Add NIC
	instance.Config.Devices = append(instance.Config.Devices, vm.Device{
		Value: "virtio-net-pci,netdev=net0",
	})

	// QMP socket
	qmpSocket, err := utils.GenerateSocketFile(fmt.Sprintf("qmp-%s", instance.ID))

	if err != nil {
		slog.Error("Failed to generate QMP socket", "err", err)
		return err
	}

	instance.Config.QMPSocket = qmpSocket

	// Create a unique error channel for this specific mount request
	processChan := make(chan int, 1)
	exitChan := make(chan int, 1)
	ptsChan := make(chan int, 1)

	go func() {
		cmd, err := instance.Config.Execute()

		if err != nil {
			slog.Error("Failed to execute VM", "err", err)
			processChan <- 0
			return
		}

		VMstdout, err := cmd.StdoutPipe()
		if err != nil {
			slog.Error("Failed to pipe STDERR VM", "err", err)
			processChan <- 0
			return
		}

		cmd.Start()

		// TODO: Consider workaround using QMP
		//  (QEMU) query-chardev
		// {"return": [{"frontend-open": true, "filename": "vc", "label": "parallel0"}, {"frontend-open": true, "filename": "unix:/run/user/1000/qmp-i-150340b52b20c0b43.sock,server=on", "label": "compat_monitor0"}, {"frontend-open": true, "filename": "pty:/dev/pts/9", "label": "serial0"}]}

		go func() {
			// TODO: Add a timeout to the scanner
			scanner := bufio.NewScanner(VMstdout)
			re := regexp.MustCompile(`/dev/pts/(\d+)`)

			for scanner.Scan() {
				line := scanner.Text()
				slog.Debug("[qemu stderr]", "line", line)

				matches := re.FindStringSubmatch(line)
				if len(matches) == 2 {
					ptsInt, err := strconv.Atoi(matches[1])

					if err != nil {
						slog.Error("Failed to convert pts to int:", "err", err)
						ptsChan <- 0
						return
					}

					ptsChan <- ptsInt // just the pts number, e.g., "9"
					return
				}

			}
		}()

		if err != nil {
			slog.Error("Failed to launch VM", "err", err)
			processChan <- 0
			return
		}

		processChan <- cmd.Process.Pid

		// Read the pts from launch

		err = cmd.Wait()

		if err != nil {
			slog.Error("Failed to wait for VM:", "err", err)
			exitChan <- 1
			return
		}

	}()

	// Wait for startup result
	pid := <-processChan

	if pid == 0 {
		return fmt.Errorf("failed to start qemu")
	}

	// Wait for 1 second to confirm nbdkit is running
	time.Sleep(1 * time.Second)

	// Fetch the pts
	pts := <-ptsChan

	if pts == 0 {
		return fmt.Errorf("failed to get pts")
	}

	// Check if nbdkit exited immediately with an error
	select {
	case exitErr := <-exitChan:
		if exitErr != 0 {
			errorMsg := fmt.Errorf("failed: %v", exitErr)
			slog.Error("Failed to launch qemu", "err", errorMsg)
			return errorMsg
		}
	default:
		// nbdkit is still running after 1 second, which means it started successfully
		slog.Info("QEMU started successfully and is running", "pts", pts)
	}

	// Confirm the instance has booted
	_, err = utils.ReadPidFile(instance.ID)

	if err != nil {
		slog.Error("Failed to read PID file", "err", err)
		return err
	}

	return nil
}

// MountVolumes mounts the volumes for an instance
func (d *Daemon) MountVolumes(instance *vm.VM) error {

	instance.EBSRequests.Mu.Lock()
	for k, v := range instance.EBSRequests.Requests {

		// Send the volume payload as JSON
		ebsMountRequest, err := json.Marshal(v)

		if err != nil {
			slog.Error("Failed to marshal volume payload", "err", err)
			return err
		}

		reply, err := d.natsConn.Request("ebs.mount", ebsMountRequest, 10*time.Second)

		slog.Debug("Mounting volume", "NBDURI", v.NBDURI)

		// TODO: Improve timeout handling
		if err != nil {
			slog.Error("Failed to request EBS mount", "err", err)
			return err
		}

		// Unmarshal the response
		var ebsMountResponse config.EBSMountResponse
		err = json.Unmarshal(reply.Data, &ebsMountResponse)

		if err != nil {
			slog.Error("Failed to unmarshal volume response:", "err", err)
			return err
		}

		if ebsMountResponse.Error == "" {

			slog.Debug("Mounted volume successfully", "response", ebsMountResponse.URI)

			// Append the NBD URI to the request
			instance.EBSRequests.Requests[k].NBDURI = ebsMountResponse.URI

		} else {
			slog.Error("Failed to mount volume", "error", ebsMountResponse.Error)
			return fmt.Errorf("failed to mount volume: %s", ebsMountResponse.Error)
		}

	}

	instance.EBSRequests.Mu.Unlock()

	return nil

}

// canAllocate checks if there are enough resources available
func (rm *ResourceManager) canAllocate(instanceType InstanceType) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	return rm.availableVCPU-rm.allocatedVCPU >= instanceType.VCPUs &&
		rm.availableMem-rm.allocatedMem >= instanceType.MemoryGB
}

// allocate reserves resources for an instance
func (rm *ResourceManager) allocate(instanceType InstanceType) error {

	if !rm.canAllocate(instanceType) {
		return fmt.Errorf("insufficient resources for instance type %s", instanceType.Name)
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.allocatedVCPU += instanceType.VCPUs
	rm.allocatedMem += instanceType.MemoryGB
	return nil
}

// deallocate releases resources for an instance
func (rm *ResourceManager) deallocate(instanceType InstanceType) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.allocatedVCPU -= instanceType.VCPUs
	rm.allocatedMem -= instanceType.MemoryGB
}

// generateInstanceID creates a unique EC2-style instance ID
func generateInstanceID() string {
	// Generate 8 random bytes (16 hex characters)
	randomBytes := make([]byte, 8)
	_, err := crand.Read(randomBytes)
	if err != nil {
		// Fallback to time-based ID if crypto/rand fails
		timestamp := time.Now().UnixNano()
		return fmt.Sprintf("i-%016x", timestamp)
	}

	// Convert to hex and format as EC2 instance ID
	return fmt.Sprintf("i-%s", hex.EncodeToString(randomBytes))
}

// generateHostname creates a hostname based on instance ID
func generateHostname(instanceID string) string {
	// Extract the unique part and create a hostname
	if len(instanceID) > 2 {
		uniquePart := instanceID[2:10] // Take first 8 chars after "i-"
		return fmt.Sprintf("hive-vm-%s", uniquePart)
	}
	return "hive-vm-unknown"
}
