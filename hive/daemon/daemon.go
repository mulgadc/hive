package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/gofiber/fiber/v2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	gateway_ec2_instance "github.com/mulgadc/hive/hive/gateway/ec2/instance"
	handlers_ec2_image "github.com/mulgadc/hive/hive/handlers/ec2/image"
	handlers_ec2_instance "github.com/mulgadc/hive/hive/handlers/ec2/instance"
	handlers_ec2_key "github.com/mulgadc/hive/hive/handlers/ec2/key"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/mulgadc/viperblock/viperblock"
	"github.com/nats-io/nats.go"
	"github.com/pelletier/go-toml/v2"
)

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

// ResourceManager handles the allocation and tracking of system resources
type ResourceManager struct {
	mu            sync.RWMutex
	availableVCPU int
	availableMem  float64
	allocatedVCPU int
	allocatedMem  float64
	instanceTypes map[string]*ec2.InstanceTypeInfo
}

// Daemon represents the main daemon service
type Daemon struct {
	node            string
	clusterConfig   *config.ClusterConfig
	config          *config.Config
	natsConn        *nats.Conn
	resourceMgr     *ResourceManager
	instanceService *handlers_ec2_instance.InstanceServiceImpl
	keyService      *handlers_ec2_key.KeyServiceImpl
	imageService    *handlers_ec2_image.ImageServiceImpl
	ctx             context.Context
	cancel          context.CancelFunc
	shutdownWg      sync.WaitGroup

	// Local VM Instances
	Instances vm.Instances

	// NAT Subscriptions
	natsSubscriptions map[string]*nats.Subscription

	// Cluster manager
	clusterApp *fiber.App
	startTime  time.Time
	configPath string

	mu sync.Mutex
}

// cpuToInstanceFamily maps CPU model patterns to AWS instance family prefixes
var cpuToInstanceFamily = map[string]string{
	"EPYC":     "m8a", // AMD EPYC processors
	"Xeon":     "m7i", // Intel Xeon processors
	"ARM":      "m8g", // ARM-based processors
	"Apple":    "m8g", // Apple Silicon (ARM-based)
	"Graviton": "m8g", // AWS Graviton
}

// getInstanceFamilyFromCPU returns the AWS instance family based on CPU model
func getInstanceFamilyFromCPU(cpuModel string) string {
	cpuUpper := strings.ToUpper(cpuModel)
	for pattern, family := range cpuToInstanceFamily {
		if strings.Contains(cpuUpper, strings.ToUpper(pattern)) {
			return family
		}
	}
	// Default fallback based on architecture
	if runtime.GOARCH == "arm64" {
		return "t4g"
	}
	return "t3" // fallback for unknown x86_64
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

// getCPUModel returns the CPU model name for the host system
func getCPUModel() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		// macOS: use sysctl
		cmd := exec.Command("sysctl", "-n", "machdep.cpu.brand_string")
		output, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("failed to get CPU model on macOS: %w", err)
		}
		return strings.TrimSpace(string(output)), nil

	case "linux":
		// Linux: read from /proc/cpuinfo
		file, err := os.Open("/proc/cpuinfo")
		if err != nil {
			return "", fmt.Errorf("failed to open /proc/cpuinfo: %w", err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "model name") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					return strings.TrimSpace(parts[1]), nil
				}
			}
		}
		return "", fmt.Errorf("model name not found in /proc/cpuinfo")

	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

// generateInstanceTypes creates the instance type map for the detected CPU family
func generateInstanceTypes(family, arch string) map[string]*ec2.InstanceTypeInfo {
	sizes := []struct {
		suffix   string
		vcpus    int
		memoryGB float64
	}{
		{"tiny", 1, 0.5},
		{"nano", 2, 0.5},
		{"micro", 2, 1.0},
		{"small", 2, 2.0},
		{"medium", 2, 4.0},
		{"large", 2, 8.0},
		{"xlarge", 4, 16.0},
		{"2xlarge", 8, 32.0},
	}

	instanceTypes := make(map[string]*ec2.InstanceTypeInfo)
	for _, size := range sizes {
		name := fmt.Sprintf("%s.%s", family, size.suffix)
		instanceTypes[name] = &ec2.InstanceTypeInfo{
			InstanceType: aws.String(name),
			VCpuInfo: &ec2.VCpuInfo{
				DefaultVCpus: aws.Int64(int64(size.vcpus)),
			},
			MemoryInfo: &ec2.MemoryInfo{
				SizeInMiB: aws.Int64(int64(size.memoryGB * 1024)),
			},
			ProcessorInfo: &ec2.ProcessorInfo{
				SupportedArchitectures: []*string{aws.String(arch)},
			},
			CurrentGeneration:             aws.Bool(true),
			BurstablePerformanceSupported: aws.Bool(strings.HasPrefix(name, "t")),
			Hypervisor:                    aws.String("kvm"),
			SupportedVirtualizationTypes:  []*string{aws.String("hvm")},
			SupportedRootDeviceTypes:      []*string{aws.String("ebs")},
		}
	}
	return instanceTypes
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

	// Get CPU model for instance family detection
	cpuModel, err := getCPUModel()
	if err != nil {
		log.Printf("Warning: Failed to get CPU model: %v, using default", err)
		cpuModel = "Unknown"
	}

	// Determine instance family from CPU model
	instanceFamily := getInstanceFamilyFromCPU(cpuModel)

	// Determine architecture
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}

	// Generate instance types based on detected CPU family
	instanceTypes := generateInstanceTypes(instanceFamily, arch)

	log.Printf("System resources: %d vCPUs, %.2f GB RAM, CPU: %s, Family: %s (detected on %s)",
		numCPU, totalMemGB, cpuModel, instanceFamily, runtime.GOOS)

	return &ResourceManager{
		availableVCPU: numCPU,
		availableMem:  totalMemGB,
		instanceTypes: instanceTypes,
	}
}

// GetInstanceTypeInfos returns all instance types as ec2.InstanceTypeInfo for AWS API compatibility
func (rm *ResourceManager) GetInstanceTypeInfos() []*ec2.InstanceTypeInfo {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var infos []*ec2.InstanceTypeInfo
	for _, it := range rm.instanceTypes {
		infos = append(infos, it)
	}
	return infos
}

// GetAvailableInstanceTypeInfos returns instance types based on total host capacity.
// If showCapacity is true, it returns multiple entries representing available slots.
// If showCapacity is false, it returns each supported type only once.
func (rm *ResourceManager) GetAvailableInstanceTypeInfos(showCapacity bool) []*ec2.InstanceTypeInfo {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var infos []*ec2.InstanceTypeInfo

	for _, it := range rm.instanceTypes {
		vCPUs := int64(0)
		if it.VCpuInfo != nil && it.VCpuInfo.DefaultVCpus != nil {
			vCPUs = *it.VCpuInfo.DefaultVCpus
		}
		memoryGB := float64(0)
		if it.MemoryInfo != nil && it.MemoryInfo.SizeInMiB != nil {
			memoryGB = float64(*it.MemoryInfo.SizeInMiB) / 1024.0
		}

		if vCPUs == 0 || memoryGB == 0 {
			continue
		}

		// Calculate how many instances of this type can fit based on TOTAL physical capacity
		countVCPU := rm.availableVCPU / int(vCPUs)
		countMem := int(rm.availableMem / memoryGB)

		// Use the minimum of CPU slots and Memory slots
		count := countVCPU
		if countMem < count {
			count = countMem
		}

		if showCapacity {
			// Add to the list as many times as it can fit
			for i := 0; i < count; i++ {
				infos = append(infos, it)
			}
		} else if count > 0 {
			// Just add it once if it fits at least once
			infos = append(infos, it)
		}
	}

	slog.Info("GetAvailableInstanceTypeInfos", "total_types", len(rm.instanceTypes), "total_available_slots", len(infos),
		"hostVCPU", rm.availableVCPU, "hostMem", rm.availableMem, "showCapacity", showCapacity)

	return infos
}

// SetConfigPath sets the configuration file path for cluster management
func (d *Daemon) SetConfigPath(path string) {
	d.configPath = path
}

// NewDaemon creates a new daemon instance
func NewDaemon(cfg *config.ClusterConfig) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())

	// If WalDir is not set, use BaseDir
	config := cfg.Nodes[cfg.Node]
	if cfg.Nodes[cfg.Node].WalDir == "" {
		config.WalDir = config.BaseDir

		cfg.Nodes[cfg.Node] = config
	}

	return &Daemon{
		node:              cfg.Node,
		clusterConfig:     cfg,
		config:            &config,
		resourceMgr:       NewResourceManager(),
		ctx:               ctx,
		cancel:            cancel,
		Instances:         vm.Instances{VMS: make(map[string]*vm.VM)},
		natsSubscriptions: make(map[string]*nats.Subscription),
		startTime:         time.Now(),
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

	// Create instance service for handling EC2 instance operations
	d.instanceService = handlers_ec2_instance.NewInstanceServiceImpl(d.config, d.resourceMgr.instanceTypes, d.natsConn, &d.Instances)

	// Create key service for handling EC2 key pair operations
	d.keyService = handlers_ec2_key.NewKeyServiceImpl(d.config)

	// Create image service for handling EC2 AMI operations
	d.imageService = handlers_ec2_image.NewImageServiceImpl(d.config)

	log.Printf("Subscribing to subject pattern: %s", "ec2.launch")

	// Subscribe to EC2 events with queue group (legacy topic for backward compatibility)
	/*
		d.natsSubscriptions["ec2.launch"], err = d.natsConn.QueueSubscribe("ec2.launch", "hive-workers", d.handleEC2RunInstances)

		if err != nil {
			return fmt.Errorf("failed to subscribe to NATS ec2.launch: %w", err)
		}
	*/

	log.Printf("Subscribing to subject pattern: %s", "ec2.RunInstances")

	// Subscribe to EC2 RunInstances with queue group (AWS Action name format - recommended)
	d.natsSubscriptions["ec2.RunInstances"], err = d.natsConn.QueueSubscribe("ec2.RunInstances", "hive-workers", d.handleEC2RunInstances)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.RunInstances: %w", err)
	}

	log.Printf("Subscribing to subject pattern: %s", "ec2.CreateKeyPair")

	// Subscribe to EC2 CreateKeyPair with queue group
	d.natsSubscriptions["ec2.CreateKeyPair"], err = d.natsConn.QueueSubscribe("ec2.CreateKeyPair", "hive-workers", d.handleEC2CreateKeyPair)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.CreateKeyPair: %w", err)
	}

	log.Printf("Subscribing to subject pattern: %s", "ec2.DeleteKeyPair")

	// Subscribe to EC2 DeleteKeyPair with queue group
	d.natsSubscriptions["ec2.DeleteKeyPair"], err = d.natsConn.QueueSubscribe("ec2.DeleteKeyPair", "hive-workers", d.handleEC2DeleteKeyPair)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.DeleteKeyPair: %w", err)
	}

	log.Printf("Subscribing to subject pattern: %s", "ec2.DescribeKeyPairs")

	// Subscribe to EC2 DescribeKeyPairs with queue group
	d.natsSubscriptions["ec2.DescribeKeyPairs"], err = d.natsConn.QueueSubscribe("ec2.DescribeKeyPairs", "hive-workers", d.handleEC2DescribeKeyPairs)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.DescribeKeyPairs: %w", err)
	}

	log.Printf("Subscribing to subject pattern: %s", "ec2.ImportKeyPair")

	// Subscribe to EC2 ImportKeyPair with queue group
	d.natsSubscriptions["ec2.ImportKeyPair"], err = d.natsConn.QueueSubscribe("ec2.ImportKeyPair", "hive-workers", d.handleEC2ImportKeyPair)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.ImportKeyPair: %w", err)
	}

	log.Printf("Subscribing to subject pattern: %s", "ec2.DescribeImages")

	// Subscribe to EC2 DescribeImages with queue group
	d.natsSubscriptions["ec2.DescribeImages"], err = d.natsConn.QueueSubscribe("ec2.DescribeImages", "hive-workers", d.handleEC2DescribeImages)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.DescribeImages: %w", err)
	}

	log.Printf("Subscribing to subject pattern: %s", "ec2.DescribeInstances")

	// Subscribe to EC2 DescribeInstances - no queue group for multi-node fan-out
	d.natsSubscriptions["ec2.DescribeInstances"], err = d.natsConn.Subscribe("ec2.DescribeInstances", d.handleEC2DescribeInstances)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.DescribeInstances: %w", err)
	}

	log.Printf("Subscribing to subject pattern: %s", "ec2.DescribeInstanceTypes")

	// Subscribe to EC2 DescribeInstanceTypes - no queue group for multi-node fan-out
	d.natsSubscriptions["ec2.DescribeInstanceTypes"], err = d.natsConn.Subscribe("ec2.DescribeInstanceTypes", d.handleEC2DescribeInstanceTypes)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.DescribeInstanceTypes: %w", err)
	}

	// Subscribe to EC2 start instance events
	// TODO: The instance state needs to be shared, not pinned to a single node.
	// TODO: Handle this in a more generic function to group similar commands (start, stop, launch)
	// Subscribe to EC2 events with queue group
	d.natsSubscriptions["ec2.startinstances"], err = d.natsConn.QueueSubscribe("ec2.startinstances", "hive-workers", d.handleEC2StartInstances)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.launch: %w", err)
	}

	// Subscribe to health check for this node
	healthSubject := fmt.Sprintf("hive.admin.%s.health", d.node)
	log.Printf("Subscribing to health check: %s", healthSubject)

	d.natsSubscriptions[healthSubject], err = d.natsConn.Subscribe(healthSubject, d.handleHealthCheck)
	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS %s: %w", healthSubject, err)
	}

	// Start cluster manager HTTP server
	if err := d.ClusterManager(); err != nil {
		return fmt.Errorf("failed to start cluster manager: %w", err)
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

// computeConfigHash computes SHA256 hash of the shared cluster config (excluding node-specific fields)
func (d *Daemon) computeConfigHash() (string, error) {
	// Only hash the shared cluster data, not the node-specific top-level field
	sharedData := config.SharedClusterData{
		Epoch:   d.clusterConfig.Epoch,
		Version: d.clusterConfig.Version,
		Nodes:   d.clusterConfig.Nodes,
	}

	configJSON, err := json.Marshal(sharedData)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(configJSON)
	return hex.EncodeToString(hash[:]), nil
}

// saveClusterConfig writes the cluster config to disk in TOML format
func (d *Daemon) saveClusterConfig() error {
	if d.configPath == "" {
		return fmt.Errorf("config path not set")
	}

	// Marshal to TOML
	configTOML, err := toml.Marshal(d.clusterConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal config to TOML: %w", err)
	}

	// Write to config file
	if err := os.WriteFile(d.configPath, configTOML, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	slog.Info("Cluster config saved", "path", d.configPath, "epoch", d.clusterConfig.Epoch)
	return nil
}

// ClusterManager starts the HTTP cluster management server
func (d *Daemon) ClusterManager() error {

	// Get daemon host from config
	daemonHost := d.config.Daemon.Host
	if daemonHost == "" {
		return fmt.Errorf("daemon.host not configured")
	}

	// Create Fiber app
	d.clusterApp = fiber.New(fiber.Config{
		DisableStartupMessage: true,
		AppName:               "Hive Cluster Manager",
	})

	// Health endpoint - responds to HTTP and NATS
	d.clusterApp.Get("/health", func(c *fiber.Ctx) error {
		configHash, err := d.computeConfigHash()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{
				"error": "failed to compute config hash",
			})
		}

		response := config.NodeHealthResponse{
			Node:       d.node,
			Status:     "running",
			ConfigHash: configHash,
			Epoch:      d.clusterConfig.Epoch,
			Uptime:     int64(time.Since(d.startTime).Seconds()),
		}

		return c.JSON(response)
	})

	// Join endpoint - accepts new nodes joining the cluster
	d.clusterApp.Post("/join", func(c *fiber.Ctx) error {
		var req config.NodeJoinRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).JSON(config.NodeJoinResponse{
				Success: false,
				Message: "invalid request body",
			})
		}

		slog.Info("Node join request received", "node", req.Node, "region", req.Region, "az", req.AZ)

		// Validate request
		if req.Node == "" || req.Region == "" || req.AZ == "" {
			return c.Status(400).JSON(config.NodeJoinResponse{
				Success: false,
				Message: "node, region, and az are required",
			})
		}

		// Check if node already exists
		if _, exists := d.clusterConfig.Nodes[req.Node]; exists {
			return c.Status(409).JSON(config.NodeJoinResponse{
				Success: false,
				Message: fmt.Sprintf("node %s already exists in cluster", req.Node),
			})
		}

		// Add new node to cluster config
		d.mu.Lock()
		newNodeConfig := config.Config{
			Node:    req.Node,
			Region:  req.Region,
			AZ:      req.AZ,
			DataDir: req.DataDir,
			Daemon: config.DaemonConfig{
				Host: req.DaemonHost,
			},
			// Copy shared config from current node
			NATS:       d.config.NATS,
			Predastore: d.config.Predastore,
			AWSGW:      d.config.AWSGW,
			AccessKey:  d.config.AccessKey,
			SecretKey:  d.config.SecretKey,
			BaseDir:    req.DataDir,
		}

		d.clusterConfig.Nodes[req.Node] = newNodeConfig
		d.clusterConfig.Epoch++ // Increment epoch for version tracking
		d.mu.Unlock()

		// Save updated config
		if err := d.saveClusterConfig(); err != nil {
			slog.Error("Failed to save cluster config", "error", err)
			return c.Status(500).JSON(config.NodeJoinResponse{
				Success: false,
				Message: "failed to save cluster config",
			})
		}

		configHash, _ := d.computeConfigHash()

		slog.Info("Node joined cluster", "node", req.Node, "epoch", d.clusterConfig.Epoch)

		// Send only shared cluster data (exclude node-specific top-level fields)
		sharedData := &config.SharedClusterData{
			Epoch:   d.clusterConfig.Epoch,
			Version: d.clusterConfig.Version,
			Nodes:   d.clusterConfig.Nodes,
		}

		return c.JSON(config.NodeJoinResponse{
			Success:     true,
			Message:     fmt.Sprintf("node %s successfully joined cluster", req.Node),
			SharedData:  sharedData,
			ConfigHash:  configHash,
			JoiningNode: req.Node,
		})
	})

	// Get cluster config endpoint
	d.clusterApp.Get("/config", func(c *fiber.Ctx) error {
		configHash, _ := d.computeConfigHash()

		return c.JSON(fiber.Map{
			"config":      d.clusterConfig,
			"config_hash": configHash,
		})
	})

	// Start HTTP server in goroutine
	go func() {
		slog.Info("Starting cluster manager", "host", daemonHost)
		if err := d.clusterApp.Listen(daemonHost); err != nil {
			slog.Error("Cluster manager failed to start", "error", err)
		}
	}()

	return nil
}

// Write the state to disk
func (d *Daemon) WriteState() error {
	d.Instances.Mu.Lock()
	defer d.Instances.Mu.Unlock()

	// Create a struct without the mutex to avoid copying the lock
	state := struct {
		VMS map[string]*vm.VM `json:"vms"`
	}{
		VMS: d.Instances.VMS,
	}

	// Pretty print JSON with indent
	jsonData, err := json.MarshalIndent(state, "", "  ")

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
		slog.Error("EC2 Start Request - Instance not found", "instanceId", ec2StartInstanceResponse.InstanceID)
		ec2StartInstanceResponse.InstanceID = ec2StartInstance.InstanceID
		ec2StartInstanceResponse.Error = awserrors.ErrorInvalidInstanceIDNotFound
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

			switch msg["event"] {
			case "STOP":
				updatedStatus = "stopped"
			case "RESUME":
				updatedStatus = "resuming"
			case "RESET":
				updatedStatus = "restarting"
			case "POWERDOWN":
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
		if errObj, ok := msg["error"].(map[string]any); ok {
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

// handleEC2Events processes incoming EC2 events (start, stop, terminate)
func (d *Daemon) handleEC2Events(msg *nats.Msg) {

	var command qmp.Command
	var resp *qmp.QMPResponse
	var err error

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

	// Start an instance
	if command.Attributes.StartInstance {
		slog.Info("Starting instance", "id", command.ID)

		// Launch the instance infrastructure (QEMU, QMP, NATS subscriptions)
		err := d.LaunchInstance(instance)

		if err != nil {
			slog.Error("handleEC2RunInstances LaunchInstance failed", "err", err)
			// TODO: Confirm LaunchInstances does this - Free the resource
			instanceType := d.resourceMgr.instanceTypes[instance.InstanceType]
			if instanceType != nil {
				d.resourceMgr.deallocate(instanceType)
			}
			return
		}

		slog.Info("handleEC2RunInstances launched", "instanceId", instance.ID)

		resp = &qmp.QMPResponse{
			Return: fmt.Appendf(nil, `{"status":"running","instanceId":"%s"}`, instance.ID),
		}

	} else {

		// Send the command to the instance
		resp, err = d.SendQMPCommand(instance.QMPClient, command.QMPCommand, instance.ID)

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

	}

	// If a terminate command, clean up resources
	if command.Attributes.TerminateInstance {
		slog.Info("Terminating instance", "id", command.ID)

		// Improve, need to pass a map
		terminateInstance := make(map[string]*vm.VM)
		terminateInstance[instance.ID] = instance
		err = d.stopInstance(terminateInstance, true)

		if err != nil {
			slog.Error("Failed to terminate instance", "err", err)
			return
		}

		// Last, delete the instance volumes

		// Free resources
		instanceType := d.resourceMgr.instanceTypes[instance.InstanceType]
		if instanceType != nil {
			d.resourceMgr.deallocate(instanceType)
		}

		// Remove instance from state
		d.Instances.Mu.Lock()
		delete(d.Instances.VMS, instance.ID)
		d.Instances.Mu.Unlock()

		slog.Info("Instance terminated", "id", command.ID)
	} else {

		// Update the instance attributes
		d.Instances.Mu.Lock()
		instance.Attributes = command.Attributes
		d.Instances.Mu.Unlock()

	}

	// Write the state to disk
	err = d.WriteState()
	if err != nil {
		slog.Error("Failed to write state to disk", "err", err)
	}

	// Respond to NATS
	msg.Respond(resp.Return)

}

// handleEC2RunInstances processes incoming EC2 RunInstances requests
func (d *Daemon) handleEC2RunInstances(msg *nats.Msg) {
	slog.Debug("Received message on subject", "subject", msg.Subject)
	slog.Debug("Message data", "data", string(msg.Data))

	// Initialize runInstancesInput before unmarshaling into it
	runInstancesInput := &ec2.RunInstancesInput{}
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
		slog.Error("handleEC2RunInstances validation failed", "err", awserrors.ErrorValidationError)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorValidationError)
		msg.Respond(errResp)
		return

	}

	slog.Info("Processing RunInstances request for instance type", "instanceType", *runInstancesInput.InstanceType)

	// Check if instance type is supported
	instanceType, exists := d.resourceMgr.instanceTypes[*runInstancesInput.InstanceType]
	if !exists {
		slog.Error("handleEC2RunInstances instance lookup", "err", awserrors.ErrorInvalidInstanceType, "InstanceType", *runInstancesInput.InstanceType)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInvalidInstanceType)
		msg.Respond(errResp)
		return
	}

	// Check if we have enough resources
	if !d.resourceMgr.canAllocate(instanceType) {
		slog.Error("handleEC2RunInstances canAllocate", "err", awserrors.ErrorInsufficientInstanceCapacity, "InstanceType", *runInstancesInput.InstanceType)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInsufficientInstanceCapacity)
		msg.Respond(errResp)
		return
	}

	// Allocate resources
	if err := d.resourceMgr.allocate(instanceType); err != nil {
		slog.Error("handleEC2RunInstances allocate", "err", awserrors.ErrorInsufficientInstanceCapacity, "InstanceType", *runInstancesInput.InstanceType)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInsufficientInstanceCapacity)
		msg.Respond(errResp)
		return
	}

	// Delegate to service for business logic (volume creation, cloud-init, etc.)
	instanceTypeName := ""
	if instanceType.InstanceType != nil {
		instanceTypeName = *instanceType.InstanceType
	}
	slog.Info("Launching EC2 instance", "instanceType", instanceTypeName)

	instance, reservation, err := d.instanceService.RunInstances(runInstancesInput)

	if err != nil {
		slog.Error("handleEC2RunInstances service.RunInstances failed", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		d.resourceMgr.deallocate(instanceType)
		return
	}

	// Respond to NATS immediately with reservation (instance is provisioning)
	jsonResponse, err := json.Marshal(reservation)
	if err != nil {
		slog.Error("handleEC2RunInstances failed to marshal reservation", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		d.resourceMgr.deallocate(instanceType)
		return
	}
	msg.Respond(jsonResponse)

	// Next, prepare the root volume, cloud-init, EFI drives via NBD (AMI clone to new volume)
	err = d.instanceService.GenerateVolumes(runInstancesInput, instance)
	if err != nil {
		slog.Error("handleEC2RunInstances GenerateVolumes failed", "err", err)
		d.resourceMgr.deallocate(instanceType)
		return
	}

	// Launch the instance infrastructure (QEMU, QMP, NATS subscriptions), this can take sometime
	err = d.LaunchInstance(instance)

	if err != nil {
		slog.Error("handleEC2RunInstances LaunchInstance failed", "err", err)
		d.resourceMgr.deallocate(instanceType)
		return
	}

	slog.Info("handleEC2RunInstances launched", "instanceId", reservation.Instances[0].InstanceId)

}

// handleEC2CreateKeyPair processes incoming EC2 CreateKeyPair requests
func (d *Daemon) handleEC2CreateKeyPair(msg *nats.Msg) {
	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	// Initialize createKeyPairInput before unmarshaling into it
	createKeyPairInput := &ec2.CreateKeyPairInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(createKeyPairInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match CreateKeyPairInput")
		return
	}

	slog.Info("Processing CreateKeyPair request", "keyName", *createKeyPairInput.KeyName)

	// Delegate to key service for business logic (key generation, S3 storage)
	output, err := d.keyService.CreateKeyPair(createKeyPairInput)

	if err != nil {
		slog.Error("handleEC2CreateKeyPair service.CreateKeyPair failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with CreateKeyPairOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2CreateKeyPair failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2CreateKeyPair completed", "keyName", *output.KeyName, "fingerprint", *output.KeyFingerprint)
}

// handleEC2DeleteKeyPair processes incoming EC2 DeleteKeyPair requests
func (d *Daemon) handleEC2DeleteKeyPair(msg *nats.Msg) {
	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	// Initialize deleteKeyPairInput before unmarshaling into it
	deleteKeyPairInput := &ec2.DeleteKeyPairInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(deleteKeyPairInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DeleteKeyPairInput")
		return
	}

	// Log which identifier was provided
	if deleteKeyPairInput.KeyPairId != nil {
		slog.Info("Processing DeleteKeyPair request", "keyPairId", *deleteKeyPairInput.KeyPairId)
	} else if deleteKeyPairInput.KeyName != nil {
		slog.Info("Processing DeleteKeyPair request", "keyName", *deleteKeyPairInput.KeyName)
	}

	// Delegate to key service for business logic (S3 deletion)
	output, err := d.keyService.DeleteKeyPair(deleteKeyPairInput)

	if err != nil {
		slog.Error("handleEC2DeleteKeyPair service.DeleteKeyPair failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with DeleteKeyPairOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DeleteKeyPair failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DeleteKeyPair completed")
}

func (d *Daemon) stopInstance(instances map[string]*vm.VM, deleteVolume bool) error {

	// Signal to shutdown each VM
	var wg sync.WaitGroup

	// Run asynchronously within a worker group
	for _, instance := range instances {

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

			// If flagged for termination (delete Volume)
			if deleteVolume {
				for _, ebsRequest := range instance.EBSRequests.Requests {

					// Send the volume payload as JSON
					ebsDeleteRequest, err := json.Marshal(ebsRequest)

					if err != nil {
						slog.Error("Failed to marshal volume payload", "err", err)
						continue
					}

					msg, err := d.natsConn.Request("ebs.delete", ebsDeleteRequest, 30*time.Second)
					if err != nil {
						slog.Error("Failed to delete volume", "name", ebsRequest.Name, "id", instance.ID, "err", err)
					} else {
						slog.Info("Deleted Viperblock volume", "id", instance.ID, "data", string(msg.Data))
					}
				}
			}

		}()
	}

	// Wait for all shutdowns to finish
	wg.Wait()

	// Unsubscribe from NATS subjects that match instances
	for _, instance := range instances {
		slog.Info("Unsubscribing from NATS subject", "instance", instance.ID)
		d.natsSubscriptions[fmt.Sprintf("ec2.cmd.%s", instance.ID)].Unsubscribe()
		// TODO: Remove redundant subscription if not used
		//d.natsSubscriptions[fmt.Sprintf("ec2.describe.%s", instance.ID)].Unsubscribe()
	}
	return nil

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

		// Pass instances to terminate
		d.stopInstance(d.Instances.VMS, false)

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

		// Shutdown cluster manager
		if d.clusterApp != nil {
			log.Println("Shutting down cluster manager...")
			if err := d.clusterApp.Shutdown(); err != nil {
				log.Printf("Error shutting down cluster manager: %v", err)
			}
		}

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
					//d.natsSubscriptions[fmt.Sprintf("ec2.describe.%s", instance.ID)].Unsubscribe()

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

	// TODO: Replaced with describe-instances with Inbox subscription
	/*
		d.natsSubscriptions[fmt.Sprintf("ec2.describe.%s", instance.ID)], err = d.natsConn.QueueSubscribe(fmt.Sprintf("ec2.describe.%s", instance.ID), "hive-events", d.handleEC2Describe)

		if err != nil {
			slog.Error("Failed to subscribe to NATS ec2.describe", "id", instance.ID, "err", err)
			return err
		}
	*/

	// Step 9: Update the instance metadata for running state and volume attached
	// Marshal to a JSON file
	// Update state
	d.Instances.Mu.Lock()
	// Update to running state
	instance.Status = "running"

	// Update EC2 Instance state for API compatibility
	if instance.Instance != nil {
		instance.Instance.State.SetCode(16) // 16 = running
		instance.Instance.State.SetName("running")
	}

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

	instanceType := d.resourceMgr.instanceTypes[instance.InstanceType]
	if instanceType == nil {
		return fmt.Errorf("instance type %s not found", instance.InstanceType)
	}

	vCPUs := int(0)
	if instanceType.VCpuInfo != nil && instanceType.VCpuInfo.DefaultVCpus != nil {
		vCPUs = int(*instanceType.VCpuInfo.DefaultVCpus)
	}
	memoryMiB := int64(0)
	if instanceType.MemoryInfo != nil && instanceType.MemoryInfo.SizeInMiB != nil {
		memoryMiB = *instanceType.MemoryInfo.SizeInMiB
	}
	architecture := "x86_64"
	if instanceType.ProcessorInfo != nil && len(instanceType.ProcessorInfo.SupportedArchitectures) > 0 && instanceType.ProcessorInfo.SupportedArchitectures[0] != nil {
		architecture = *instanceType.ProcessorInfo.SupportedArchitectures[0]
	}

	instance.Config = vm.Config{
		Name:         instance.ID,
		Daemonize:    true,
		PIDFile:      pidFile,
		EnableKVM:    true, // If available, if kvm fails, will use cpu max
		NoGraphic:    true,
		MachineType:  "q35",
		Serial:       "pty",
		CPUType:      "host", // If available, if kvm fails, will use cpu max
		Memory:       int(memoryMiB),
		CPUCount:     vCPUs,
		Architecture: architecture,
	}

	// Loop through each volume in volumes
	instance.EBSRequests.Mu.Lock()

	for _, v := range instance.EBSRequests.Requests {

		drive := vm.Drive{}

		drive.File = v.NBDURI
		// Cleanup hostname to point to nbd://localhost from [::]
		// TODO: Make NBD host config defined, or remote NBD server if not running locally.
		// TODO: Add socket support for nbdkit, much faster than TCP
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

	// TODO: Toggle SSH local port forwarding based on config (debugging use)
	sshDebugPort, err := viperblock.FindFreePort()
	if err != nil {
		slog.Error("Failed to find free port", "err", err)
		return err
	}

	// Just the ipv4 port required
	sshDebugPort = strings.Replace(sshDebugPort, "[::]:", "", 1)

	// TODO: Make configurable
	instance.Config.NetDevs = append(instance.Config.NetDevs, vm.NetDev{
		Value: fmt.Sprintf("user,id=net0,hostfwd=tcp:127.0.0.1:%s-:22", sshDebugPort),
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

	// Temp, wait for nbdkit to start
	time.Sleep(2 * time.Second)

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

		VMstderr, err := cmd.StderrPipe()
		if err != nil {
			slog.Error("Failed to pipe STDERR VM", "err", err)
			processChan <- 0
			return
		}

		err = cmd.Start()

		if err != nil {
			slog.Error("Failed to start VM", "err", err)
			processChan <- 0
			return
		}

		slog.Info("VM started successfully", "pid", cmd.Process.Pid)

		// TODO: Consider workaround using QMP
		//  (QEMU) query-chardev
		// {"return": [{"frontend-open": true, "filename": "vc", "label": "parallel0"}, {"frontend-open": true, "filename": "unix:/run/user/1000/qmp-i-150340b52b20c0b43.sock,server=on", "label": "compat_monitor0"}, {"frontend-open": true, "filename": "pty:/dev/pts/9", "label": "serial0"}]}

		go func() {
			// TODO: Add a timeout to the scanner
			scanner := bufio.NewScanner(VMstdout)

			slog.Info("QEMU stdout reader started")

			re := regexp.MustCompile(`/dev/pts/(\d+)`)

			for scanner.Scan() {
				line := scanner.Text()
				slog.Info("[qemu]", "line", line)

				matches := re.FindStringSubmatch(line)
				if len(matches) == 2 {
					ptsInt, err := strconv.Atoi(matches[1])
					slog.Info("Extracted pts from QEMU output", "pts", ptsInt)

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

		// --- reader for STDERR ---
		go func() {
			scanner := bufio.NewScanner(VMstderr)
			slog.Info("QEMU stderr reader started")

			for scanner.Scan() {
				line := scanner.Text()
				slog.Error("[qemu-stderr]", "line", line)
			}
		}()

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
		// TODO: Improve, pts can be returned as "0" which is valid
		// "Extracted pts from QEMU output" pts=0
		slog.Error("Failed to get pts from QEMU output", "pts", pts)
		//return fmt.Errorf("failed to get pts")
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
func (rm *ResourceManager) canAllocate(instanceType *ec2.InstanceTypeInfo) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	vCPUs := int64(0)
	if instanceType.VCpuInfo != nil && instanceType.VCpuInfo.DefaultVCpus != nil {
		vCPUs = *instanceType.VCpuInfo.DefaultVCpus
	}
	memoryGB := float64(0)
	if instanceType.MemoryInfo != nil && instanceType.MemoryInfo.SizeInMiB != nil {
		memoryGB = float64(*instanceType.MemoryInfo.SizeInMiB) / 1024.0
	}

	return rm.availableVCPU-rm.allocatedVCPU >= int(vCPUs) &&
		rm.availableMem-rm.allocatedMem >= memoryGB
}

// allocate reserves resources for an instance
func (rm *ResourceManager) allocate(instanceType *ec2.InstanceTypeInfo) error {

	if !rm.canAllocate(instanceType) {
		instanceTypeName := ""
		if instanceType.InstanceType != nil {
			instanceTypeName = *instanceType.InstanceType
		}
		return fmt.Errorf("insufficient resources for instance type %s", instanceTypeName)
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	vCPUs := int64(0)
	if instanceType.VCpuInfo != nil && instanceType.VCpuInfo.DefaultVCpus != nil {
		vCPUs = *instanceType.VCpuInfo.DefaultVCpus
	}
	memoryGB := float64(0)
	if instanceType.MemoryInfo != nil && instanceType.MemoryInfo.SizeInMiB != nil {
		memoryGB = float64(*instanceType.MemoryInfo.SizeInMiB) / 1024.0
	}

	rm.allocatedVCPU += int(vCPUs)
	rm.allocatedMem += memoryGB
	return nil
}

// deallocate releases resources for an instance
func (rm *ResourceManager) deallocate(instanceType *ec2.InstanceTypeInfo) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	vCPUs := int64(0)
	if instanceType.VCpuInfo != nil && instanceType.VCpuInfo.DefaultVCpus != nil {
		vCPUs = *instanceType.VCpuInfo.DefaultVCpus
	}
	memoryGB := float64(0)
	if instanceType.MemoryInfo != nil && instanceType.MemoryInfo.SizeInMiB != nil {
		memoryGB = float64(*instanceType.MemoryInfo.SizeInMiB) / 1024.0
	}

	rm.allocatedVCPU -= int(vCPUs)
	rm.allocatedMem -= memoryGB
}

// handleEC2DescribeKeyPairs processes incoming EC2 DescribeKeyPairs requests
func (d *Daemon) handleEC2DescribeKeyPairs(msg *nats.Msg) {
	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	// Initialize describeKeyPairsInput before unmarshaling into it
	describeKeyPairsInput := &ec2.DescribeKeyPairsInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(describeKeyPairsInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DescribeKeyPairsInput")
		return
	}

	slog.Info("Processing DescribeKeyPairs request")

	// Delegate to key service for business logic (S3 listing)
	output, err := d.keyService.DescribeKeyPairs(describeKeyPairsInput)

	if err != nil {
		slog.Error("handleEC2DescribeKeyPairs service.DescribeKeyPairs failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with DescribeKeyPairsOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeKeyPairs failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DescribeKeyPairs completed", "count", len(output.KeyPairs))
}

// handleEC2ImportKeyPair processes incoming EC2 ImportKeyPair requests
func (d *Daemon) handleEC2ImportKeyPair(msg *nats.Msg) {
	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	// Initialize importKeyPairInput before unmarshaling into it
	importKeyPairInput := &ec2.ImportKeyPairInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(importKeyPairInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match ImportKeyPairInput")
		return
	}

	// Log which key is being imported (avoid logging the actual key material)
	if importKeyPairInput.KeyName != nil {
		slog.Info("Processing ImportKeyPair request", "keyName", *importKeyPairInput.KeyName)
	}

	// Delegate to key service for business logic (key parsing, S3 storage)
	output, err := d.keyService.ImportKeyPair(importKeyPairInput)

	if err != nil {
		slog.Error("handleEC2ImportKeyPair service.ImportKeyPair failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with ImportKeyPairOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2ImportKeyPair failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2ImportKeyPair completed", "keyName", *output.KeyName, "fingerprint", *output.KeyFingerprint)
}

// handleEC2DescribeImages processes incoming EC2 DescribeImages requests
func (d *Daemon) handleEC2DescribeImages(msg *nats.Msg) {
	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	// Initialize describeImagesInput before unmarshaling into it
	describeImagesInput := &ec2.DescribeImagesInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(describeImagesInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DescribeImagesInput")
		return
	}

	slog.Info("Processing DescribeImages request")

	// Delegate to image service for business logic (S3 listing)
	output, err := d.imageService.DescribeImages(describeImagesInput)

	if err != nil {
		slog.Error("handleEC2DescribeImages service.DescribeImages failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with DescribeImagesOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeImages failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DescribeImages completed", "count", len(output.Images))
}

// handleEC2DescribeInstanceTypes processes incoming EC2 DescribeInstanceTypes requests
// This handler responds with instance types that can currently be provisioned on this node
// based on available resources (CPU and memory not already allocated to running instances)
func (d *Daemon) handleEC2DescribeInstanceTypes(msg *nats.Msg) {
	log.Printf("Received message on subject: %s", msg.Subject)

	// Initialize input
	describeInput := &ec2.DescribeInstanceTypesInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(describeInput, msg.Data)
	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DescribeInstanceTypesInput")
		return
	}

	slog.Info("Processing DescribeInstanceTypes request from this node")

	// Check if "capacity" filter is set to "true"
	showCapacity := false
	for _, f := range describeInput.Filters {
		if f.Name != nil && *f.Name == "capacity" {
			for _, v := range f.Values {
				if v != nil && *v == "true" {
					showCapacity = true
					break
				}
			}
		}
	}

	// Get instance types based on capacity and the showCapacity flag
	availableTypes := d.resourceMgr.GetAvailableInstanceTypeInfos(showCapacity)

	// Filter by requested instance types if specified
	var filteredTypes []*ec2.InstanceTypeInfo
	if len(describeInput.InstanceTypes) > 0 {
		requestedTypes := make(map[string]bool)
		for _, t := range describeInput.InstanceTypes {
			if t != nil {
				requestedTypes[*t] = true
			}
		}
		for _, info := range availableTypes {
			if info.InstanceType != nil && requestedTypes[*info.InstanceType] {
				filteredTypes = append(filteredTypes, info)
			}
		}
	} else {
		filteredTypes = availableTypes
	}

	// Create the response
	output := &ec2.DescribeInstanceTypesOutput{
		InstanceTypes: filteredTypes,
	}

	// Respond to NATS
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeInstanceTypes failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DescribeInstanceTypes completed", "count", len(filteredTypes))
}

// handleEC2DescribeInstances processes incoming EC2 DescribeInstances requests
// This handler responds with all instances running on this node
func (d *Daemon) handleEC2DescribeInstances(msg *nats.Msg) {
	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	// Initialize describeInstancesInput before unmarshaling into it
	describeInstancesInput := &ec2.DescribeInstancesInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(describeInstancesInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DescribeInstancesInput")
		return
	}

	slog.Info("Processing DescribeInstances request from this node")

	// Build response with reservations from instances on this node
	var reservations []*ec2.Reservation

	d.Instances.Mu.Lock()
	defer d.Instances.Mu.Unlock()

	// Filter instances if specific instance IDs were requested
	instanceIDFilter := make(map[string]bool)
	if len(describeInstancesInput.InstanceIds) > 0 {
		for _, id := range describeInstancesInput.InstanceIds {
			if id != nil {
				instanceIDFilter[*id] = true
			}
		}
	}

	// Iterate through all instances on this node
	for _, instance := range d.Instances.VMS {
		// Skip if filtering by instance IDs and this instance is not in the filter
		if len(instanceIDFilter) > 0 && !instanceIDFilter[instance.ID] {
			continue
		}

		// Use stored reservation metadata if available
		if instance.Reservation != nil && instance.Instance != nil {
			// Create a copy of the reservation with updated instance state
			reservation := *instance.Reservation

			// Update the instance state to current state
			instanceCopy := *instance.Instance
			instanceCopy.State = &ec2.InstanceState{}

			// Map internal status to EC2 state codes
			switch instance.Status {
			case "pending", "provisioning":
				instanceCopy.State.SetCode(0)
				instanceCopy.State.SetName("pending")
			case "running":
				instanceCopy.State.SetCode(16)
				instanceCopy.State.SetName("running")
			case "stopped":
				instanceCopy.State.SetCode(80)
				instanceCopy.State.SetName("stopped")
			case "terminated":
				instanceCopy.State.SetCode(48)
				instanceCopy.State.SetName("terminated")
			default:
				instanceCopy.State.SetCode(0)
				instanceCopy.State.SetName("pending")
			}

			reservation.Instances = []*ec2.Instance{&instanceCopy}
			reservations = append(reservations, &reservation)
		}
	}

	// Create the response
	output := &ec2.DescribeInstancesOutput{
		Reservations: reservations,
	}

	// Respond to NATS with DescribeInstancesOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeInstances failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DescribeInstances completed", "count", len(reservations))
}

// handleHealthCheck processes NATS health check requests
func (d *Daemon) handleHealthCheck(msg *nats.Msg) {
	configHash, err := d.computeConfigHash()
	if err != nil {
		slog.Error("Failed to compute config hash for health check", "error", err)
		configHash = "error"
	}

	response := config.NodeHealthResponse{
		Node:       d.node,
		Status:     "running",
		ConfigHash: configHash,
		Epoch:      d.clusterConfig.Epoch,
		Uptime:     int64(time.Since(d.startTime).Seconds()),
	}

	jsonResponse, err := json.Marshal(response)
	if err != nil {
		slog.Error("handleHealthCheck failed to marshal response", "err", err)
		return
	}

	msg.Respond(jsonResponse)
	slog.Debug("Health check responded", "node", d.node, "epoch", d.clusterConfig.Epoch)
}
