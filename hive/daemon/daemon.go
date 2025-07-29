package daemon

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/kdomanski/iso9660"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/s3client"
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
	Name     string
	VCPUs    int
	MemoryGB float64
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
	instanceTypes := map[string]InstanceType{
		"t3.nano":   {Name: "t3.nano", VCPUs: 2, MemoryGB: 0.5},
		"t3.micro":  {Name: "t3.micro", VCPUs: 2, MemoryGB: 1.0},
		"t3.small":  {Name: "t3.small", VCPUs: 2, MemoryGB: 2.0},
		"t3.medium": {Name: "t3.medium", VCPUs: 2, MemoryGB: 4.0},
		"t3.large":  {Name: "t3.large", VCPUs: 2, MemoryGB: 8.0},
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
	return &Daemon{
		config:      cfg,
		resourceMgr: NewResourceManager(),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start initializes and starts the daemon
func (d *Daemon) Start() error {
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

	var err error
	d.natsConn, err = nats.Connect(d.config.NATS.Host, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	log.Printf("Connected to NATS server at %s", d.config.NATS.Host)
	log.Printf("Subscribing to subject pattern: %s", d.config.NATS.Sub.Subject)

	// Subscribe to EC2 events with queue group
	sub, err := d.natsConn.QueueSubscribe(d.config.NATS.Sub.Subject, "hive-workers", d.handleEC2Message)
	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS: %w", err)
	}

	// Setup graceful shutdown
	d.setupShutdown(sub)

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

// handleEC2Message processes incoming EC2 messages
func (d *Daemon) handleEC2Message(msg *nats.Msg) {
	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	var ec2Req EC2Request
	if err := json.Unmarshal(msg.Data, &ec2Req); err != nil {
		log.Printf("Error unmarshaling EC2 request: %v", err)
		return
	}

	// Only handle RunInstances action
	if ec2Req.Action != "RunInstances" {
		log.Printf("Ignoring non-RunInstances action: %s", ec2Req.Action)
		return
	}

	log.Printf("Processing RunInstances request for instance type: %s", ec2Req.InstanceType)

	// Check if instance type is supported
	instanceType, exists := d.resourceMgr.instanceTypes[ec2Req.InstanceType]
	if !exists {
		log.Printf("Unsupported instance type: %s", ec2Req.InstanceType)
		return
	}

	// Check if we have enough resources
	if !d.resourceMgr.canAllocate(instanceType) {
		log.Printf("Insufficient resources for instance type: %s", ec2Req.InstanceType)
		return
	}

	// Allocate resources
	if err := d.resourceMgr.allocate(instanceType); err != nil {
		log.Printf("Failed to allocate resources: %v", err)
		return
	}

	log.Printf("Launching EC2 instance: %+v", ec2Req)

	if err := d.launchEC2Instance(ec2Req); err != nil {
		log.Printf("Failed to launch EC2 instance: %v", err)

		d.resourceMgr.deallocate(instanceType)
		return
	}

	// Acknowledge the message
	if err := msg.Ack(); err != nil {
		log.Printf("Error acknowledging message: %v", err)
	}
}

// setupShutdown configures graceful shutdown handling
func (d *Daemon) setupShutdown(sub *nats.Subscription) {
	d.shutdownWg.Add(1)
	go func() {
		defer d.shutdownWg.Done()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

		<-sigChan
		log.Println("Received shutdown signal, cleaning up...")

		// Cancel context
		d.cancel()

		// Unsubscribe from NATS
		if err := sub.Unsubscribe(); err != nil {
			log.Printf("Error unsubscribing from NATS: %v", err)
		}

		// Close NATS connection
		d.natsConn.Close()

		// Wait for any ongoing operations to complete
		// TODO: Implement cleanup of running instances
		log.Println("Shutdown complete")
	}()
}

func (d *Daemon) launchEC2Instance(ec2Req EC2Request) error {

	// Validate input
	var size int = 4 * 1024 * 1024 * 1024 // 4GB default size
	var deviceName string
	var volumeType string
	var iops int

	var imageId string
	var snapshotId string

	var ebsRequests []config.EBSRequest

	if len(ec2Req.BlockDeviceMapping) > 0 {
		size = ec2Req.BlockDeviceMapping[0].EBS.VolumeSize
		deviceName = ec2Req.BlockDeviceMapping[0].DeviceName
		volumeType = ec2Req.BlockDeviceMapping[0].EBS.VolumeType
		iops = ec2Req.BlockDeviceMapping[0].EBS.Iops
	}

	// Check if the image starts with ami-
	if strings.HasPrefix(ec2Req.ImageID, "ami-") {
		// Generate a random number to append to the volume ID ( 8 digits )
		randomNumber := rand.Intn(100_000_000)

		imageId = viperblock.GenerateVolumeID("vol", fmt.Sprintf("%d-%s", randomNumber, ec2Req.ImageID), "predastore", time.Now().Unix())
		snapshotId = ec2Req.ImageID
	} else {
		imageId = ec2Req.ImageID
	}

	// Pre-flight, confirm if the instance is already running (TODO)

	// CONFIRM: All Viperblock AMI and volumes stored in a system S3 bucket, vs the individual users account.

	// Step 1: Confirm if the volume already exists

	cfg := s3.S3Config{
		VolumeName: imageId,
		VolumeSize: uint64(size),
		Bucket:     "predastore",
		Region:     "ap-southeast-2",
		AccessKey:  d.config.AccessKey,
		SecretKey:  d.config.SecretKey,
		Host:       d.config.Host,
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
		BaseDir:    d.config.BaseDir,
		Cache: viperblock.Cache{
			Config: viperblock.CacheConfig{
				Size: 0,
			},
		},
		VolumeConfig: volumeConfig,
	}

	vb, err := viperblock.New(vbconfig, "s3", cfg)
	if err != nil {
		slog.Error("Failed to connect to Viperblock store: %v", err)
		return err
	}

	vb.SetDebug(true)

	// Initialize the backend
	err = vb.Backend.Init()

	if err != nil {
		slog.Error("Failed to initialize backend: %v", err)
		return err
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
			log.Fatalf("Failed to load WAL: %v", err)
		}

		// Open the block to object WAL
		err = vb.OpenWAL(&vb.BlockToObjectWAL, fmt.Sprintf("%s/%s", vb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALBlock, vb.BlockToObjectWAL.WallNum.Load(), vb.GetVolume())))

		if err != nil {
			log.Fatalf("Failed to load block WAL: %v", err)
		}

		amiCfg := s3.S3Config{
			VolumeName: ec2Req.ImageID,
			VolumeSize: uint64(size),
			Bucket:     "predastore",
			Region:     "ap-southeast-2",
			AccessKey:  d.config.AccessKey,
			SecretKey:  d.config.SecretKey,
			Host:       d.config.Host,
		}

		amiVbConfig := viperblock.VB{
			VolumeName: ec2Req.ImageID,
			VolumeSize: uint64(size),
			BaseDir:    d.config.BaseDir,
			Cache: viperblock.Cache{
				Config: viperblock.CacheConfig{
					Size: 0,
				},
			},
			VolumeConfig: volumeConfig,
		}

		amiVb, err := viperblock.New(amiVbConfig, "s3", amiCfg)

		if err != nil {
			slog.Error("Failed to connect to Viperblock store for AMI: %v", err)
			return err
		}

		// Initialize the backend
		fmt.Println("Initializing AMI Viperblock store backend")
		err = amiVb.Backend.Init()

		if err != nil {
			slog.Error("Could not connect to AMI Viperblock store: %v", err)
			return err
		}

		fmt.Println("Loading state for AMI Viperblock store")
		err = amiVb.LoadState()

		if err != nil {
			slog.Error("Could not load state for AMI Viperblock store: %v", err)
			return err
		}

		err = amiVb.LoadBlockState()

		if err != nil {
			slog.Error("Failed to load block state: %v", err)
			return err
		}

		fmt.Println("Starting to clone AMI to new volume")

		var block uint64 = 0
		nullBlock := make([]byte, vb.BlockSize)

		// Read each block from the AMI, write to our new volume, skipping null blocks

		for {
			//fmt.Println("Reading block", block)

			if block*uint64(vb.BlockSize) >= amiVb.VolumeSize {
				fmt.Println("Reached end of AMI")
				break
			}

			// Read 1MB
			data, err := amiVb.ReadAt(block*uint64(vb.BlockSize), uint64(vb.BlockSize)*1024)

			if err != nil && err != viperblock.ZeroBlock {
				slog.Error("Failed to read block from AMI source: %v", err)
				return err
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
					fmt.Println("Flush", "block", block)
					vb.Flush()
					vb.WriteWALToChunk(true)
				}
			}

		}

		fmt.Println("Closing")

		err = vb.Close()

		if err != nil {
			log.Fatalf("Failed to close Viperblock store: %v", err)
		}

		err = vb.RemoveLocalFiles()

		if err != nil {
			slog.Error("Failed to remove local files: %v", err)
		}

		// New volume is cloned.

	}

	// Append root volume
	ebsRequests = append(ebsRequests, config.EBSRequest{
		Name: vbconfig.VolumeName,
		Boot: true,
	})

	//var walNum uint64

	// Step 3: Create the EFI partition if it does not yet exist

	efiVolumeName := fmt.Sprintf("%s-efi", imageId)
	efiSize := 64 * 1024 * 1024 // 64MB

	efiCfg := s3.S3Config{
		VolumeName: efiVolumeName,
		VolumeSize: uint64(efiSize),
		Bucket:     "predastore",
		Region:     "ap-southeast-2",
		AccessKey:  d.config.AccessKey,
		SecretKey:  d.config.SecretKey,
		Host:       d.config.Host,
	}

	efiVbConfig := viperblock.VB{
		VolumeName: efiVolumeName,
		VolumeSize: uint64(efiSize),
		BaseDir:    d.config.BaseDir,
		Cache: viperblock.Cache{
			Config: viperblock.CacheConfig{
				Size: 0,
			},
		},
		VolumeConfig: volumeConfig,
	}

	efiVb, err := viperblock.New(efiVbConfig, "s3", efiCfg)

	efiVb.SetDebug(true)

	if err != nil {
		slog.Error("Failed to connect to Viperblock store for AMI: %v", err)
		return err
	}

	// Initialize the backend
	fmt.Println("Initializing EFI Viperblock store backend")
	err = efiVb.Backend.Init()

	fmt.Println("Complete EFI Viperblock init", "error", err)

	if err != nil {
		slog.Error("Failed to initialize EFI Viperblock store backend: %v", err)
		return err
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
			log.Fatalf("Failed to load WAL: %v", err)
		}

		// Open the block to object WAL
		err = vb.OpenWAL(&efiVb.BlockToObjectWAL, fmt.Sprintf("%s/%s", efiVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALBlock, efiVb.BlockToObjectWAL.WallNum.Load(), efiVb.GetVolume())))

		if err != nil {
			log.Fatalf("Failed to load block WAL: %v", err)
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
		slog.Error("Failed to close EFI Viperblock store: %v", err)
	}

	err = efiVb.RemoveLocalFiles()

	if err != nil {
		slog.Error("Failed to remove local files: %v", err)
	}

	ebsRequests = append(ebsRequests, config.EBSRequest{
		Name: efiVb.VolumeName,
		Boot: false,
	})

	// Step 4: Create the cloud-init volume, with the specified SSH key and attributes

	keyName := ec2Req.KeyName
	userData := ec2Req.UserData

	if keyName != "" || userData != "" {

		slog.Info("Creating cloud-init volume")

		cloudInitVolumeName := fmt.Sprintf("%s-cloudinit", imageId)
		cloudInitSize := 1 * 1024 * 1024 // 1MB

		cloudInitCfg := s3.S3Config{
			VolumeName: cloudInitVolumeName,
			VolumeSize: uint64(cloudInitSize),
			Bucket:     "predastore",
			Region:     "ap-southeast-2",
			AccessKey:  d.config.AccessKey,
			SecretKey:  d.config.SecretKey,
			Host:       d.config.Host,
		}

		cloudInitVbConfig := viperblock.VB{
			VolumeName: cloudInitVolumeName,
			VolumeSize: uint64(cloudInitSize),
			BaseDir:    d.config.BaseDir,
			Cache: viperblock.Cache{
				Config: viperblock.CacheConfig{
					Size: 0,
				},
			},
			VolumeConfig: volumeConfig,
		}

		cloudInitVb, err := viperblock.New(cloudInitVbConfig, "s3", cloudInitCfg)

		cloudInitVb.SetDebug(true)

		if err != nil {
			slog.Error("Failed to connect to Viperblock store for AMI: %v", err)
			return err
		}

		// Initialize the backend
		fmt.Println("Initializing cloud-init Viperblock store backend")
		err = cloudInitVb.Backend.Init()

		// Load the state from the remote backend
		//err = vb.LoadState()
		_, err = cloudInitVb.LoadStateRequest("")

		// Step 2: If launching from an AMI and the volume doesn't exist, clone the AMI to our new volume

		if err != nil {

			slog.Info("Volume does not yet exist, creating from cloud-init disk ...")

			// Open the chunk WAL
			err = cloudInitVb.OpenWAL(&cloudInitVb.WAL, fmt.Sprintf("%s/%s", cloudInitVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALChunk, cloudInitVb.WAL.WallNum.Load(), cloudInitVb.GetVolume())))

			if err != nil {
				log.Fatalf("Failed to load WAL: %v", err)
			}

			// Open the block to object WAL
			err = cloudInitVb.OpenWAL(&cloudInitVb.BlockToObjectWAL, fmt.Sprintf("%s/%s", cloudInitVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALBlock, cloudInitVb.BlockToObjectWAL.WallNum.Load(), cloudInitVb.GetVolume())))

			if err != nil {
				log.Fatalf("Failed to load block WAL: %v", err)
			}

			// Create the cloud-init disk
			writer, err := iso9660.NewWriter()
			if err != nil {
				slog.Error("failed to create writer: %s", err)
				return err
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
				Host:      d.config.Host,
				Bucket:    "predastore",
				Region:    "ap-southeast-2",
			})

			err = s3c.Init()

			if err != nil {
				log.Fatalf("failed to initialize S3 client: %v", err)
			}

			sshKey, err := s3c.Read(fmt.Sprintf("/ssh/%s", keyName))
			if err != nil {
				log.Fatalf("failed to read SSH key: %v", err)
			}

			userData := CloudInitData{
				Username: "ec2-user",
				SSHKey:   string(sshKey), // provided ssh key
				Hostname: hostname,
			}

			var buf bytes.Buffer
			t := template.Must(template.New("cloud-init").Parse(cloudInitUserDataTemplate))

			if err := t.Execute(&buf, userData); err != nil {
				log.Fatalf("failed to render template: %v", err)
			}

			fmt.Println("user-data", buf.String())

			// Add user-data
			err = writer.AddFile(&buf, "user-data")
			if err != nil {
				log.Fatalf("failed to add file: %s", err)
			}

			// Add meta-data
			metaData := CloudInitMetaData{
				InstanceID: instanceId,
				Hostname:   hostname,
			}

			t = template.Must(template.New("meta-data").Parse(cloudInitMetaTemplate))

			buf.Reset()

			if err := t.Execute(&buf, metaData); err != nil {
				log.Fatalf("failed to render template: %v", err)
			}

			fmt.Println("meta-data", buf.String())

			err = writer.AddFile(&buf, "meta-data")
			if err != nil {
				log.Fatalf("failed to add file: %s", err)
			}

			// Store temp file
			tempFile, err := os.CreateTemp("", "cloud-init-*.iso")

			slog.Info("Created temp ISO file", "file", tempFile.Name())

			outputFile, err := os.OpenFile(tempFile.Name(), os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
			if err != nil {
				log.Fatalf("failed to create file: %s", err)
			}

			// Requires cidata volume label for cloud-init to recognize
			err = writer.WriteTo(outputFile, "cidata")

			if err != nil {
				log.Fatalf("failed to write ISO image: %s", err)
			}

			err = writer.Cleanup()

			if err != nil {
				log.Fatalf("failed to cleanup writer: %s", err)
			}

			err = outputFile.Close()

			if err != nil {
				log.Fatalf("failed to close output file: %s", err)
			}

			isoData, err := os.ReadFile(tempFile.Name())

			if err != nil {
				log.Fatalf("failed to read ISO image: %s", err)
			}

			err = cloudInitVb.WriteAt(0, isoData)

			if err != nil {
				log.Fatalf("failed to write ISO image to viperblock volume: %s", err)
			}

			// Flush
			cloudInitVb.Flush()
			cloudInitVb.WriteWALToChunk(true)

			// Remove the temp ISO file

			err = os.Remove(tempFile.Name())

			if err != nil {
				slog.Error("Failed to remove temp file: %v", err)
			}

		}

		err = cloudInitVb.Close()

		if err != nil {
			slog.Error("Failed to close cloud-init Viperblock store: %v", err)
		}

		err = cloudInitVb.RemoveLocalFiles()

		if err != nil {
			slog.Error("Failed to remove local files: %v", err)
		}

		ebsRequests = append(ebsRequests, config.EBSRequest{
			Name: cloudInitCfg.VolumeName,
			Boot: false,
		})

	}

	// Step 5: Mount each volume via NBD, confirm running as expected for pre-flight checks.
	// TODO: Run a goroutine for each volume

	// Connect to NATS
	nc, err := nats.Connect(d.config.NATS.Host)

	if err != nil {
		slog.Error("Failed to connect to NATS: %v", err)
		return err
	}

	// Loop through each volume in volumes
	for k, v := range ebsRequests {

		fmt.Println(k, v)

		// Send the volume payload as JSON
		payload, err := json.Marshal(v)

		if err != nil {
			slog.Error("Failed to marshal volume payload: %v", err)
			return err
		}

		reply, err := nc.Request("ebs.mount", payload, 10*time.Second)

		if err != nil {
			log.Fatalln(err)
		}

		fmt.Println(string(reply.Data))

	}

	/*
		reply, err := nc.Request("ebs.mount", []byte(), 4*time.Second)

		if err != nil {
			log.Fatalln(err)
		}
	*/

	// Step 6: Launch the instance via QEMU/KVM

	// Step 7: Update the instance metadata for running state and volume attached

	// Step 8: Return the unique instance ID on success

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
