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
	"path/filepath"
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
	handlers_ec2_volume "github.com/mulgadc/hive/hive/handlers/ec2/volume"
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
	volumeService   *handlers_ec2_volume.VolumeServiceImpl
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

	// JetStream manager for KV state storage (nil if JetStream disabled)
	jsManager *JetStreamManager

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
// It includes both the CPU-specific family and the burstable family (t3 for x86, t4g for ARM)
func generateInstanceTypes(family, arch string) map[string]*ec2.InstanceTypeInfo {
	sizes := []struct {
		suffix   string
		vcpus    int
		memoryGB float64
	}{
		{"nano", 2, 0.5},
		{"micro", 2, 1.0},
		{"small", 2, 2.0},
		{"medium", 2, 4.0},
		{"large", 2, 8.0},
		{"xlarge", 4, 16.0},
		{"2xlarge", 8, 32.0},
	}

	// Determine the burstable family based on architecture
	burstableFamily := "t3"
	if arch == "arm64" {
		burstableFamily = "t4g"
	}

	// Build list of families to generate: CPU-specific + burstable (if different)
	families := []struct {
		name      string
		burstable bool
	}{
		{family, false},
	}
	if family != burstableFamily {
		families = append(families, struct {
			name      string
			burstable bool
		}{burstableFamily, true})
	}

	instanceTypes := make(map[string]*ec2.InstanceTypeInfo)
	for _, fam := range families {
		for _, size := range sizes {
			name := fmt.Sprintf("%s.%s", fam.name, size.suffix)
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
				BurstablePerformanceSupported: aws.Bool(false),
				// BurstablePerformanceSupported: aws.Bool(fam.burstable),
				Hypervisor:                   aws.String("kvm"),
				SupportedVirtualizationTypes: []*string{aws.String("hvm")},
				SupportedRootDeviceTypes:     []*string{aws.String("ebs")},
			}
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

	// Determine burstable family for logging
	burstableFamily := "t3"
	if runtime.GOARCH == "arm64" {
		burstableFamily = "t4g"
	}
	log.Printf("System resources: %d vCPUs, %.2f GB RAM, CPU: %s, Families: %s + %s (detected on %s)",
		numCPU, totalMemGB, cpuModel, instanceFamily, burstableFamily, runtime.GOOS)

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

		remainingVCPU := rm.availableVCPU - rm.allocatedVCPU
		remainingMem := rm.availableMem - rm.allocatedMem

		// Calculate how many instances of this type can fit based on REMAINING host capacity
		countVCPU := remainingVCPU / int(vCPUs)
		countMem := int(remainingMem / memoryGB)

		// Use the minimum of CPU slots and Memory slots
		count := countVCPU
		if countMem < count {
			count = countMem
		}

		if count < 0 {
			count = 0
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

	// Start cluster manager HTTP server FIRST
	// This must happen before JetStream init so other nodes can join via /join endpoint
	// and help form the NATS cluster (avoids chicken-and-egg in multi-node setup)
	if err := d.ClusterManager(); err != nil {
		return fmt.Errorf("failed to start cluster manager: %w", err)
	}

	// Initialize JetStream for KV state storage (required - no disk fallback)
	// Start with 1 replica to allow single-node startup, then upgrade if cluster has more nodes
	// Retry with backoff if JetStream is not ready yet
	maxRetries := 10
	retryDelay := 500 * time.Millisecond

	for attempt := 1; attempt <= maxRetries; attempt++ {
		d.jsManager, err = NewJetStreamManager(d.natsConn, 1)
		if err != nil {
			slog.Warn("Failed to init JetStream", "error", err, "attempt", attempt, "maxRetries", maxRetries)
			if attempt < maxRetries {
				time.Sleep(retryDelay)
				retryDelay *= 2 // Exponential backoff
				if retryDelay > 5*time.Second {
					retryDelay = 5 * time.Second
				}
				continue
			}
			return fmt.Errorf("failed to initialize JetStream after %d attempts: %w", maxRetries, err)
		}

		if err := d.jsManager.InitKVBucket(); err != nil {
			slog.Warn("Failed to init KV bucket", "error", err, "attempt", attempt, "maxRetries", maxRetries)
			if attempt < maxRetries {
				time.Sleep(retryDelay)
				retryDelay *= 2
				if retryDelay > 5*time.Second {
					retryDelay = 5 * time.Second
				}
				continue
			}
			return fmt.Errorf("failed to initialize JetStream KV bucket after %d attempts: %w", maxRetries, err)
		}

		// Success
		slog.Info("JetStream KV store initialized successfully", "replicas", 1, "attempts", attempt)
		break
	}

	// Try to upgrade replicas if cluster has more nodes
	// This handles the case where this daemon starts after other NATS nodes are already up
	clusterSize := len(d.clusterConfig.Nodes)
	if clusterSize > 1 {
		if err := d.jsManager.UpdateReplicas(clusterSize); err != nil {
			slog.Warn("Failed to upgrade JetStream replicas on startup (other NATS nodes may not be ready)", "targetReplicas", clusterSize, "error", err)
		}
	}

	// Create services before loading/launching instances, since LaunchInstance depends on them
	d.instanceService = handlers_ec2_instance.NewInstanceServiceImpl(d.config, d.resourceMgr.instanceTypes, d.natsConn, &d.Instances)
	d.keyService = handlers_ec2_key.NewKeyServiceImpl(d.config)
	d.imageService = handlers_ec2_image.NewImageServiceImpl(d.config)
	d.volumeService = handlers_ec2_volume.NewVolumeServiceImpl(d.config)

	// Load existing state for VMs from JetStream or disk
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
				instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
				if ok {
					slog.Info("Re-allocating resources for instance", "instanceId", instance.ID, "type", instance.InstanceType)
					if err := d.resourceMgr.allocate(instanceType); err != nil {
						slog.Error("Failed to re-allocate resources for instance on startup", "instanceId", instance.ID, "err", err)
					}
				}

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

	log.Printf("Subscribing to subject pattern: %s", "ec2.DescribeVolumes")

	// Subscribe to EC2 DescribeVolumes with queue group
	d.natsSubscriptions["ec2.DescribeVolumes"], err = d.natsConn.QueueSubscribe("ec2.DescribeVolumes", "hive-workers", d.handleEC2DescribeVolumes)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.DescribeVolumes: %w", err)
	}

	slog.Info("Subscribing to subject pattern", "subject", "ec2.ModifyVolume")

	// Subscribe to EC2 ModifyVolume with queue group
	d.natsSubscriptions["ec2.ModifyVolume"], err = d.natsConn.QueueSubscribe("ec2.ModifyVolume", "hive-workers", d.handleEC2ModifyVolume)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS ec2.ModifyVolume: %w", err)
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

	// Subscribe to node discovery - all daemons respond so gateway can discover active nodes
	log.Printf("Subscribing to node discovery: hive.nodes.discover")
	d.natsSubscriptions["hive.nodes.discover"], err = d.natsConn.Subscribe("hive.nodes.discover", d.handleNodeDiscover)
	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS hive.nodes.discover: %w", err)
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

		// Update JetStream KV replicas to match new cluster size
		// This may fail if the new node's NATS server isn't running yet - that's OK,
		// replicas can be updated later when the cluster is fully formed
		if d.jsManager != nil {
			newReplicaCount := len(d.clusterConfig.Nodes)
			if err := d.jsManager.UpdateReplicas(newReplicaCount); err != nil {
				slog.Warn("Failed to update JetStream replicas (new node NATS may not be ready yet)", "targetReplicas", newReplicaCount, "error", err)
			}
		}

		// Send only shared cluster data (exclude node-specific top-level fields)
		sharedData := &config.SharedClusterData{
			Epoch:   d.clusterConfig.Epoch,
			Version: d.clusterConfig.Version,
			Nodes:   d.clusterConfig.Nodes,
		}

		// Read CA certificate and key to share with joining node for per-node cert generation
		caCertPath := filepath.Join(d.config.BaseDir, "config", "ca.pem")
		caKeyPath := filepath.Join(d.config.BaseDir, "config", "ca.key")

		var caCert, caKey string
		if caCertPEM, err := os.ReadFile(caCertPath); err == nil {
			caCert = string(caCertPEM)
		} else {
			slog.Warn("Could not read CA cert for join response", "error", err)
		}

		if caKeyPEM, err := os.ReadFile(caKeyPath); err == nil {
			caKey = string(caKeyPEM)
		} else {
			slog.Warn("Could not read CA key for join response", "error", err)
		}

		return c.JSON(config.NodeJoinResponse{
			Success:     true,
			Message:     fmt.Sprintf("node %s successfully joined cluster", req.Node),
			SharedData:  sharedData,
			ConfigHash:  configHash,
			JoiningNode: req.Node,
			CACert:      caCert,
			CAKey:       caKey,
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

// WriteState writes the instance state to JetStream KV store (required)
func (d *Daemon) WriteState() error {
	if d.jsManager == nil {
		return fmt.Errorf("JetStream manager not initialized - cannot write state")
	}
	if err := d.jsManager.WriteState(d.node, &d.Instances); err != nil {
		slog.Error("JetStream write failed", "error", err)
		return fmt.Errorf("failed to write state to JetStream: %w", err)
	}
	return nil
}

// LoadState loads the instance state from JetStream KV store (required)
func (d *Daemon) LoadState() error {
	if d.jsManager == nil {
		return fmt.Errorf("JetStream manager not initialized - cannot load state")
	}

	instances, err := d.jsManager.LoadState(d.node)
	if err != nil {
		slog.Error("JetStream load failed", "error", err)
		return fmt.Errorf("failed to load state from JetStream: %w", err)
	}

	// Copy only the VMS map, not the mutex
	d.Instances.VMS = instances.VMS
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

	// Check if we have enough resources and allocate them
	instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
	if ok {
		if err := d.resourceMgr.allocate(instanceType); err != nil {
			slog.Error("EC2 Start Request - Insufficient capacity", "instanceId", instance.ID, "err", err)
			ec2StartInstanceResponse.InstanceID = ec2StartInstance.InstanceID
			ec2StartInstanceResponse.Error = awserrors.ErrorInsufficientInstanceCapacity
			ec2StartInstanceResponse.Respond(msg)
			return
		}
	}

	// Launch the instance
	err := d.LaunchInstance(instance)

	if err != nil {
		// Deallocate on failure
		if ok {
			d.resourceMgr.deallocate(instanceType)
		}
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

	// Helper to ensure we always respond to NATS
	respondWithError := func(errCode string) {
		msg.Respond(utils.GenerateErrorPayload(errCode))
	}

	if err := json.Unmarshal(msg.Data, &command); err != nil {
		log.Printf("Error unmarshaling QMP command: %v", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	d.Instances.Mu.Lock()
	instance, ok := d.Instances.VMS[command.ID]
	d.Instances.Mu.Unlock()

	if !ok {
		slog.Warn("Instance is not running on this node", "id", command.ID)
		respondWithError(awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	// Start an instance
	if command.Attributes.StartInstance {
		slog.Info("Starting instance", "id", command.ID)

		// Allocate resources
		instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
		if ok {
			if err := d.resourceMgr.allocate(instanceType); err != nil {
				slog.Error("Failed to allocate resources for start command", "id", command.ID, "err", err)
				respondWithError(awserrors.ErrorInsufficientInstanceCapacity)
				return
			}
		}

		// Launch the instance infrastructure (QEMU, QMP, NATS subscriptions)
		err := d.LaunchInstance(instance)

		if err != nil {
			slog.Error("handleEC2RunInstances LaunchInstance failed", "err", err)
			// Free the resource on failure
			if ok {
				d.resourceMgr.deallocate(instanceType)
			}
			respondWithError(awserrors.ErrorServerInternal)
			return
		}

		// Update instance state
		d.Instances.Mu.Lock()
		instance.Status = "running"
		instance.Attributes = command.Attributes
		if instance.Instance != nil {
			instance.Instance.State.SetCode(16) // 16 = running
			instance.Instance.State.SetName("running")
		}
		d.Instances.Mu.Unlock()

		slog.Info("Instance started", "instanceId", instance.ID)

		// Write state to disk
		if writeErr := d.WriteState(); writeErr != nil {
			slog.Error("Failed to write state to disk", "err", writeErr)
		}

		msg.Respond(fmt.Appendf(nil, `{"status":"running","instanceId":"%s"}`, instance.ID))
		return
	}

	// Stop or terminate an instance
	if command.Attributes.StopInstance || command.Attributes.TerminateInstance {
		isTerminate := command.Attributes.TerminateInstance
		action := "Stopping"
		initialStatus := "stopping"
		finalStatus := "stopped"
		finalCode := int64(80)
		if isTerminate {
			action = "Terminating"
			initialStatus = "shutting-down"
			finalStatus = "terminated"
			finalCode = 48
		}

		slog.Info(action+" instance", "id", command.ID)

		// Update status to transitional state
		d.Instances.Mu.Lock()
		instance.Status = initialStatus
		if instance.Instance != nil {
			if isTerminate {
				instance.Instance.State.SetCode(32) // 32 = shutting-down
				instance.Instance.State.SetName("shutting-down")
			} else {
				instance.Instance.State.SetCode(64) // 64 = stopping
				instance.Instance.State.SetName("stopping")
			}
		}
		d.Instances.Mu.Unlock()

		// Respond immediately - operation will complete in background
		// stopInstance() handles the QMP shutdown command, so we don't send it here
		msg.Respond([]byte(`{}`))

		// Run cleanup in goroutine to not block NATS
		go func(inst *vm.VM, attrs qmp.Attributes) {
			stopErr := d.stopInstance(map[string]*vm.VM{inst.ID: inst}, isTerminate)

			d.Instances.Mu.Lock()
			if stopErr != nil {
				slog.Error("Failed to "+strings.ToLower(action)+" instance", "err", stopErr, "id", inst.ID)
				// On error, revert to previous state or mark as error
				inst.Status = "error"
				if inst.Instance != nil {
					inst.Instance.State.SetCode(0)
					inst.Instance.State.SetName("error")
				}
			} else {
				inst.Status = finalStatus
				inst.Attributes = attrs
				if inst.Instance != nil {
					inst.Instance.State.SetCode(finalCode)
					inst.Instance.State.SetName(finalStatus)
				}
				slog.Info("Instance "+finalStatus, "id", inst.ID)
			}
			d.Instances.Mu.Unlock()

			if writeErr := d.WriteState(); writeErr != nil {
				slog.Error("Failed to write state to disk", "err", writeErr)
			}
		}(instance, command.Attributes)

		return
	}

	// Regular QMP command - must succeed
	resp, err = d.SendQMPCommand(instance.QMPClient, command.QMPCommand, instance.ID)
	if err != nil {
		slog.Error("Failed to send QMP command", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	slog.Debug("RAW QMP Response", "resp", string(resp.Return))

	// Unmarshal the response
	target, ok := qmp.CommandResponseTypes[command.QMPCommand.Execute]
	if !ok {
		slog.Warn("Unhandled QMP command", "cmd", command.QMPCommand.Execute)
		msg.Respond(resp.Return)
		return
	}

	if err := json.Unmarshal(resp.Return, target); err != nil {
		slog.Error("Failed to unmarshal QMP response", "cmd", command.QMPCommand.Execute, "err", err)
		msg.Respond(resp.Return)
		return
	}

	// Update attributes and respond
	d.Instances.Mu.Lock()
	instance.Attributes = command.Attributes
	d.Instances.Mu.Unlock()

	if err := d.WriteState(); err != nil {
		slog.Error("Failed to write state to disk", "err", err)
	}

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

	// Determine how many instances to launch based on MinCount/MaxCount
	minCount := int(*runInstancesInput.MinCount)
	maxCount := int(*runInstancesInput.MaxCount)

	// Check how many we can actually launch
	allocatableCount := d.resourceMgr.canAllocate(instanceType, maxCount)

	if allocatableCount < minCount {
		// Cannot satisfy MinCount requirement - fail entirely
		slog.Error("handleEC2RunInstances insufficient capacity", "requested", minCount, "available", allocatableCount, "InstanceType", *runInstancesInput.InstanceType)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInsufficientInstanceCapacity)
		msg.Respond(errResp)
		return
	}

	// Launch up to MaxCount, capped by available capacity
	// Note: canAllocate() already caps at maxCount, so allocatableCount <= maxCount
	launchCount := allocatableCount

	slog.Info("Instance count determined", "min", minCount, "max", maxCount, "launching", launchCount)

	// Allocate resources for all instances upfront
	var allocatedCount int
	for i := 0; i < launchCount; i++ {
		if err := d.resourceMgr.allocate(instanceType); err != nil {
			slog.Error("handleEC2RunInstances allocate failed mid-allocation", "allocated", allocatedCount, "err", err)
			break
		}
		allocatedCount++
	}

	// Check if we still meet MinCount after allocation
	if allocatedCount < minCount {
		// Rollback allocations
		for i := 0; i < allocatedCount; i++ {
			d.resourceMgr.deallocate(instanceType)
		}
		slog.Error("handleEC2RunInstances insufficient capacity after allocation", "allocated", allocatedCount, "minCount", minCount)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInsufficientInstanceCapacity)
		msg.Respond(errResp)
		return
	}

	// Update launchCount to what we actually allocated
	launchCount = allocatedCount

	// Delegate to service for business logic (volume creation, cloud-init, etc.)
	instanceTypeName := ""
	if instanceType.InstanceType != nil {
		instanceTypeName = *instanceType.InstanceType
	}
	slog.Info("Launching EC2 instances", "instanceType", instanceTypeName, "count", launchCount)

	// Create all instances
	var instances []*vm.VM
	var allEC2Instances []*ec2.Instance

	for i := 0; i < launchCount; i++ {
		instance, ec2Instance, err := d.instanceService.RunInstance(runInstancesInput)
		if err != nil {
			slog.Error("handleEC2RunInstances service.RunInstance failed", "index", i, "err", err)
			// Deallocate this instance's resources
			d.resourceMgr.deallocate(instanceType)
			continue
		}
		instances = append(instances, instance)
		allEC2Instances = append(allEC2Instances, ec2Instance)
	}

	// Check if we still have enough instances after creation errors
	if len(instances) < minCount {
		// Rollback: deallocate resources for successfully created instances
		// (failed instances already deallocated their resources above)
		for range instances {
			d.resourceMgr.deallocate(instanceType)
		}
		slog.Error("handleEC2RunInstances failed to create minimum instances", "created", len(instances), "minCount", minCount)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}

	// Build reservation with all instances
	reservation := ec2.Reservation{}
	reservation.SetReservationId(vm.GenerateEC2ReservationID())
	reservation.SetOwnerId("123456789012") // TODO: Use actual owner ID from config
	reservation.Instances = allEC2Instances

	// Store reservation reference in all VMs
	for _, instance := range instances {
		instance.Reservation = &reservation
	}

	// Respond to NATS immediately with reservation (instances are provisioning)
	jsonResponse, err := json.Marshal(reservation)
	if err != nil {
		slog.Error("handleEC2RunInstances failed to marshal reservation", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		// Deallocate all resources
		for range instances {
			d.resourceMgr.deallocate(instanceType)
		}
		return
	}
	msg.Respond(jsonResponse)

	// Add all instances to state immediately so DescribeInstances can find them
	// while volumes are being prepared and VMs are launching
	d.Instances.Mu.Lock()
	for _, instance := range instances {
		d.Instances.VMS[instance.ID] = instance
	}
	d.Instances.Mu.Unlock()

	if err := d.WriteState(); err != nil {
		slog.Error("handleEC2RunInstances failed to write initial state", "err", err)
	}

	slog.Info("Instances added to state with pending status", "count", len(instances))

	// Launch all instances (volumes and VMs)
	var successCount int
	for _, instance := range instances {
		// Prepare the root volume, cloud-init, EFI drives via NBD (AMI clone to new volume)
		volumeInfos, err := d.instanceService.GenerateVolumes(runInstancesInput, instance)
		if err != nil {
			slog.Error("handleEC2RunInstances GenerateVolumes failed", "instanceId", instance.ID, "err", err)
			d.resourceMgr.deallocate(instanceType)
			d.markInstanceFailed(instance, "volume_preparation_failed")
			continue
		}

		// Populate BlockDeviceMappings on the ec2.Instance
		instance.Instance.BlockDeviceMappings = make([]*ec2.InstanceBlockDeviceMapping, 0, len(volumeInfos))
		for _, vi := range volumeInfos {
			mapping := &ec2.InstanceBlockDeviceMapping{}
			mapping.SetDeviceName(vi.DeviceName)
			mapping.Ebs = &ec2.EbsInstanceBlockDevice{}
			mapping.Ebs.SetVolumeId(vi.VolumeId)
			mapping.Ebs.SetAttachTime(vi.AttachTime)
			mapping.Ebs.SetDeleteOnTermination(vi.DeleteOnTermination)
			mapping.Ebs.SetStatus("attached")
			instance.Instance.BlockDeviceMappings = append(instance.Instance.BlockDeviceMappings, mapping)
		}

		// Launch the instance infrastructure (QEMU, QMP, NATS subscriptions)
		err = d.LaunchInstance(instance)
		if err != nil {
			slog.Error("handleEC2RunInstances LaunchInstance failed", "instanceId", instance.ID, "err", err)
			d.resourceMgr.deallocate(instanceType)
			d.markInstanceFailed(instance, "launch_failed")
			continue
		}

		successCount++
		slog.Info("handleEC2RunInstances launched instance", "instanceId", instance.ID)
	}

	slog.Info("handleEC2RunInstances completed", "requested", launchCount, "created", len(instances), "launched", successCount)
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

			// Send shutdown command - if it fails, VM may already be dead, continue with cleanup
			_, err := d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{Execute: "system_powerdown"}, instance.ID)
			if err != nil {
				slog.Warn("QMP system_powerdown failed (VM may already be stopped)", "id", instance.ID, "err", err)
				// Don't return - continue with cleanup
			}

			// Wait for PID file removal (or check if already gone)
			err = utils.WaitForPidFileRemoval(instance.ID, 60*time.Second)
			if err != nil {
				slog.Warn("Timeout waiting for PID file removal", "id", instance.ID, "err", err)

				// Try force killing the process if it's still running
				pid, readErr := utils.ReadPidFile(instance.ID)
				if readErr != nil {
					slog.Debug("No PID file found (VM likely already stopped)", "id", instance.ID)
				} else {
					slog.Info("Force killing process", "pid", pid, "id", instance.ID)
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

				// Update volume state to "available" for boot volumes
				if ebsRequest.Boot {
					if err := d.volumeService.UpdateVolumeState(ebsRequest.Name, "available", ""); err != nil {
						slog.Error("Failed to update volume state to available", "volumeId", ebsRequest.Name, "err", err)
					}
				}
			}

			// If flagged for termination (delete Volume)
			// if deleteVolume {
			// 	for _, ebsRequest := range instance.EBSRequests.Requests {

			// 		// Send the volume payload as JSON
			// 		ebsDeleteRequest, err := json.Marshal(ebsRequest)

			// 		if err != nil {
			// 			slog.Error("Failed to marshal volume payload", "err", err)
			// 			continue
			// 		}

			// 		msg, err := d.natsConn.Request("ebs.delete", ebsDeleteRequest, 30*time.Second)
			// 		if err != nil {
			// 			slog.Error("Failed to delete volume", "name", ebsRequest.Name, "id", instance.ID, "err", err)
			// 		} else {
			// 			slog.Info("Deleted Viperblock volume", "id", instance.ID, "data", string(msg.Data))
			// 		}
			// 	}
			// }

			// Deallocate resources
			instanceType := d.resourceMgr.instanceTypes[instance.InstanceType]
			if instanceType != nil {
				slog.Info("Deallocating resources for stopped instance", "instanceId", instance.ID, "type", instance.InstanceType)
				d.resourceMgr.deallocate(instanceType)
			}
		}()
	}

	// Wait for all shutdowns to finish
	wg.Wait()

	// Only unsubscribe from NATS subjects when terminating (deleteVolume=true)
	// For stop operations, keep the subscription so we can receive start commands
	if deleteVolume {
		for _, instance := range instances {
			slog.Info("Unsubscribing from NATS subject", "instance", instance.ID)
			d.natsSubscriptions[fmt.Sprintf("ec2.cmd.%s", instance.ID)].Unsubscribe()
			// TODO: Remove redundant subscription if not used
			//d.natsSubscriptions[fmt.Sprintf("ec2.describe.%s", instance.ID)].Unsubscribe()
		}
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

		// Write state to JetStream before closing NATS connection
		err := d.WriteState()
		if err != nil {
			slog.Error("Failed to write state", "err", err)
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

			// Check if instance is in a terminal or transitional state - exit heartbeat
			d.Instances.Mu.Lock()
			status := instance.Status
			d.Instances.Mu.Unlock()

			if status == "stopping" || status == "stopped" || status == "shutting-down" || status == "terminated" || status == "error" {
				slog.Info("QMP heartbeat exiting - instance not running", "instance", instance.ID, "status", status)

				// Close the QMP client connection if it exists
				if instance.QMPClient != nil && instance.QMPClient.Conn != nil {
					instance.QMPClient.Conn.Close()
				}
				return
			}

			slog.Debug("QMP heartbeat", "instance", instance.ID)
			qmpStatus, err := d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{Execute: "query-status"}, instance.ID)

			if err != nil {
				slog.Warn("QMP heartbeat failed", "instance", instance.ID, "err", err)
				// Don't exit on transient errors - let the status check above handle terminal states
				continue
			}

			slog.Debug("QMP status", "instance", instance.ID, "status", string(qmpStatus.Return))
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
		if err == nil {
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

	// Step 10: Mark boot volumes as "in-use" now that instance is confirmed running
	instance.EBSRequests.Mu.Lock()
	for _, ebsReq := range instance.EBSRequests.Requests {
		if ebsReq.Boot {
			if err := d.volumeService.UpdateVolumeState(ebsReq.Name, "in-use", instance.ID); err != nil {
				slog.Error("Failed to update volume state to in-use", "volumeId", ebsReq.Name, "err", err)
			}
		}
	}
	instance.EBSRequests.Mu.Unlock()

	err = d.WriteState()

	if err != nil {
		slog.Error("Failed to marshal launchVm", "err", err)
		return err
	}

	return nil
}

// markInstanceFailed updates an instance status to indicate a failure during launch
func (d *Daemon) markInstanceFailed(instance *vm.VM, reason string) {
	d.Instances.Mu.Lock()
	defer d.Instances.Mu.Unlock()

	instance.Status = "shutting-down"

	// Update EC2 Instance state for API compatibility
	if instance.Instance != nil {
		instance.Instance.State.SetCode(32) // 32 = shutting-down
		instance.Instance.State.SetName("shutting-down")
		// Add state reason
		instance.Instance.StateReason = &ec2.StateReason{}
		instance.Instance.StateReason.SetCode("Server.InternalError")
		instance.Instance.StateReason.SetMessage(reason)
	}

	d.Instances.VMS[instance.ID] = instance

	if err := d.WriteState(); err != nil {
		slog.Error("markInstanceFailed failed to write state", "err", err)
	}

	slog.Info("Instance marked as failed", "instanceId", instance.ID, "reason", reason)
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

		// Use the NBDURI from mount response - contains socket path or TCP address
		// NBDURI format: "nbd:unix:/path/to/socket.sock" or "nbd://host:port"
		if v.NBDURI == "" {
			slog.Error("NBDURI not set for volume", "volume", v.Name)
			return fmt.Errorf("NBDURI not set for volume %s - was volume mounted?", v.Name)
		}
		drive.File = v.NBDURI
		slog.Info("Using NBD URI for drive", "volume", v.Name, "uri", v.NBDURI)

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
	// TODO: Improve, confirm nbdkit started for each volume
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
						ptsChan <- -1
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

	if pts < 0 {
		// pts == -1 indicates failure to extract pts from QEMU output
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

		reply, err := d.natsConn.Request("ebs.mount", ebsMountRequest, 30*time.Second)

		slog.Info("Mounting volume", "Vol", v.Name, "NBDURI", v.NBDURI)

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

// canAllocate checks how many instances of the given type can be allocated
// Returns the count that can actually be allocated (0 to count)
func (rm *ResourceManager) canAllocate(instanceType *ec2.InstanceTypeInfo, count int) int {
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

	availableVCPU := rm.availableVCPU - rm.allocatedVCPU
	availableMem := rm.availableMem - rm.allocatedMem

	// Calculate how many instances we can fit based on CPU and memory
	countByCPU := count
	if vCPUs > 0 {
		countByCPU = availableVCPU / int(vCPUs)
	}

	countByMem := count
	if memoryGB > 0 {
		countByMem = int(availableMem / memoryGB)
	}

	// Take the minimum of CPU-limited and memory-limited counts
	allocatableCount := countByCPU
	if countByMem < allocatableCount {
		allocatableCount = countByMem
	}

	// Cap at requested count
	if allocatableCount > count {
		allocatableCount = count
	}

	// Ensure non-negative
	if allocatableCount < 0 {
		allocatableCount = 0
	}

	return allocatableCount
}

// allocate reserves resources for an instance
func (rm *ResourceManager) allocate(instanceType *ec2.InstanceTypeInfo) error {

	if rm.canAllocate(instanceType, 1) < 1 {
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

// handleEC2DescribeVolumes processes incoming EC2 DescribeVolumes requests
func (d *Daemon) handleEC2DescribeVolumes(msg *nats.Msg) {
	log.Printf("Received message on subject: %s", msg.Subject)
	log.Printf("Message data: %s", string(msg.Data))

	// Initialize describeVolumesInput before unmarshaling into it
	describeVolumesInput := &ec2.DescribeVolumesInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(describeVolumesInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DescribeVolumesInput")
		return
	}

	slog.Info("Processing DescribeVolumes request")

	// Delegate to volume service for business logic (S3 listing)
	output, err := d.volumeService.DescribeVolumes(describeVolumesInput)

	if err != nil {
		slog.Error("handleEC2DescribeVolumes service.DescribeVolumes failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with DescribeVolumesOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeVolumes failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DescribeVolumes completed", "count", len(output.Volumes))
}

// handleEC2ModifyVolume processes incoming EC2 ModifyVolume requests
func (d *Daemon) handleEC2ModifyVolume(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject)
	slog.Debug("Message data", "data", string(msg.Data))

	modifyVolumeInput := &ec2.ModifyVolumeInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(modifyVolumeInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match ModifyVolumeInput")
		return
	}

	slog.Info("Processing ModifyVolume request", "volumeId", modifyVolumeInput.VolumeId)

	output, err := d.volumeService.ModifyVolume(modifyVolumeInput)

	if err != nil {
		slog.Error("handleEC2ModifyVolume service.ModifyVolume failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2ModifyVolume failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	// Notify viperblockd to reload state after volume modification (e.g. resize)
	if modifyVolumeInput.VolumeId != nil {
		syncData, err := json.Marshal(config.EBSSyncRequest{Volume: *modifyVolumeInput.VolumeId})
		if err != nil {
			slog.Error("failed to marshal ebs.sync request", "volumeId", *modifyVolumeInput.VolumeId, "err", err)
		} else {
			_, syncErr := d.natsConn.Request("ebs.sync", syncData, 5*time.Second)
			if syncErr != nil {
				slog.Warn("ebs.sync notification failed (volume may not be mounted)",
					"volumeId", *modifyVolumeInput.VolumeId, "err", syncErr)
			}
		}
	}

	slog.Info("handleEC2ModifyVolume completed", "volumeId", modifyVolumeInput.VolumeId)
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
	filteredTypes := d.resourceMgr.GetAvailableInstanceTypeInfos(showCapacity)

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

	// Group instances by reservation ID (AWS returns instances grouped by reservation)
	reservationMap := make(map[string]*ec2.Reservation)

	// Iterate through all instances on this node
	for _, instance := range d.Instances.VMS {
		// Skip if filtering by instance IDs and this instance is not in the filter
		if len(instanceIDFilter) > 0 && !instanceIDFilter[instance.ID] {
			continue
		}

		// Use stored reservation metadata if available
		if instance.Reservation != nil && instance.Instance != nil {
			resID := ""
			if instance.Reservation.ReservationId != nil {
				resID = *instance.Reservation.ReservationId
			}

			// Create reservation entry if it doesn't exist
			if _, exists := reservationMap[resID]; !exists {
				reservation := &ec2.Reservation{}
				reservation.SetReservationId(resID)
				if instance.Reservation.OwnerId != nil {
					reservation.SetOwnerId(*instance.Reservation.OwnerId)
				}
				reservation.Instances = []*ec2.Instance{}
				reservationMap[resID] = reservation
			}

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
			case "stopping":
				instanceCopy.State.SetCode(64)
				instanceCopy.State.SetName("stopping")
			case "stopped":
				instanceCopy.State.SetCode(80)
				instanceCopy.State.SetName("stopped")
			case "shutting-down":
				instanceCopy.State.SetCode(32)
				instanceCopy.State.SetName("shutting-down")
			case "terminated":
				instanceCopy.State.SetCode(48)
				instanceCopy.State.SetName("terminated")
			default:
				instanceCopy.State.SetCode(0)
				instanceCopy.State.SetName("pending")
			}

			// Add instance to its reservation
			reservationMap[resID].Instances = append(reservationMap[resID].Instances, &instanceCopy)
		}
	}

	// Convert map to slice
	reservations := make([]*ec2.Reservation, 0, len(reservationMap))
	for _, reservation := range reservationMap {
		reservations = append(reservations, reservation)
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

// NodeDiscoverResponse is the response for node discovery requests
type NodeDiscoverResponse struct {
	Node string `json:"node"`
}

// handleNodeDiscover responds to node discovery requests with this node's ID
// Used by the gateway to dynamically discover active hive nodes in the cluster
func (d *Daemon) handleNodeDiscover(msg *nats.Msg) {
	response := NodeDiscoverResponse{
		Node: d.node,
	}

	jsonResponse, err := json.Marshal(response)
	if err != nil {
		slog.Error("handleNodeDiscover failed to marshal response", "err", err)
		return
	}

	msg.Respond(jsonResponse)
	slog.Debug("Node discovery responded", "node", d.node)
}
