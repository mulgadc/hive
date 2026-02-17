package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/gofiber/fiber/v2"
	cpuid "github.com/klauspost/cpuid/v2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	handlers_ec2_eigw "github.com/mulgadc/hive/hive/handlers/ec2/eigw"
	handlers_ec2_image "github.com/mulgadc/hive/hive/handlers/ec2/image"
	handlers_ec2_instance "github.com/mulgadc/hive/hive/handlers/ec2/instance"
	handlers_ec2_key "github.com/mulgadc/hive/hive/handlers/ec2/key"
	handlers_ec2_snapshot "github.com/mulgadc/hive/hive/handlers/ec2/snapshot"
	handlers_ec2_tags "github.com/mulgadc/hive/hive/handlers/ec2/tags"
	handlers_ec2_volume "github.com/mulgadc/hive/hive/handlers/ec2/volume"
	"github.com/mulgadc/hive/hive/objectstore"
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

// ResourceManager handles the allocation and tracking of system resources.
// It dynamically manages per-instance-type NATS subscriptions: when capacity
// is available for a type, the node subscribes to ec2.RunInstances.{type};
// when full, it unsubscribes so NATS routes requests to other nodes.
type ResourceManager struct {
	mu            sync.RWMutex
	availableVCPU int
	availableMem  float64
	allocatedVCPU int
	allocatedMem  float64
	instanceTypes map[string]*ec2.InstanceTypeInfo

	// Dynamic instance-type subscription management
	subsMu       sync.Mutex
	natsConn     *nats.Conn
	instanceSubs map[string]*nats.Subscription
	handler      nats.MsgHandler
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
	accountService  *handlers_ec2_account.AccountSettingsServiceImpl
	snapshotService *handlers_ec2_snapshot.SnapshotServiceImpl
	tagsService     *handlers_ec2_tags.TagsServiceImpl
	eigwService     *handlers_ec2_eigw.EgressOnlyIGWServiceImpl
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

	// Delay after QMP device_del before blockdev-del (default 1s, 0 in tests)
	detachDelay time.Duration

	// shuttingDown is set to true during coordinated cluster shutdown (GATE phase).
	// When true, the daemon rejects new work and setupShutdown skips VM stops.
	shuttingDown atomic.Bool

	mu sync.Mutex
}

// cpuGeneration represents a specific CPU microarchitecture generation
// and the AWS instance families it maps to.
type cpuGeneration struct {
	name     string   // e.g. "Intel Ice Lake", "AMD Genoa"
	families []string // e.g. ["t3", "c6i", "m6i", "r6i"]
}

// Intel generations
var (
	genIntelBroadwell      = cpuGeneration{"Intel Broadwell", []string{"t2", "c4", "m4", "r4"}}
	genIntelSkylake        = cpuGeneration{"Intel Skylake/Cascade Lake", []string{"t3", "c5", "m5", "r5"}}
	genIntelIceLake        = cpuGeneration{"Intel Ice Lake", []string{"t3", "c6i", "m6i", "r6i"}}
	genIntelSapphireRapids = cpuGeneration{"Intel Sapphire Rapids", []string{"t3", "c7i", "m7i", "r7i"}}
	genIntelGraniteRapids  = cpuGeneration{"Intel Granite Rapids", []string{"t3", "c8i", "m8i", "r8i"}}
)

// AMD generations
var (
	genAMDZen  = cpuGeneration{"AMD Zen/Zen2 (Naples/Rome)", []string{"t3a", "c5a", "m5a", "r5a"}}
	genAMDZen3 = cpuGeneration{"AMD Zen 3 (Milan)", []string{"t3a", "c6a", "m6a", "r6a"}}
	genAMDZen4 = cpuGeneration{"AMD Zen 4 (Genoa)", []string{"t3a", "c7a", "m7a", "r7a"}}
	genAMDZen5 = cpuGeneration{"AMD Zen 5 (Turin)", []string{"t3a", "c8a", "m8a", "r8a"}}
)

// ARM generations
var (
	genARMNeoverseN1 = cpuGeneration{"ARM Neoverse N1 (Graviton2)", []string{"t4g", "c6g", "m6g", "r6g"}}
	genARMNeoverseV1 = cpuGeneration{"ARM Neoverse V1 (Graviton3)", []string{"t4g", "c7g", "m7g", "r7g"}}
	genARMNeoverseV2 = cpuGeneration{"ARM Neoverse V2 (Graviton4)", []string{"t4g", "c8g", "m8g", "r8g"}}
)

// Unknown/fallback generations — expose only burstable family
var (
	genUnknownIntel = cpuGeneration{"Unknown Intel", []string{"t3"}}
	genUnknownAMD   = cpuGeneration{"Unknown AMD", []string{"t3a"}}
	genUnknownARM   = cpuGeneration{"Unknown ARM", []string{"t4g"}}
	genUnknown      = cpuGeneration{"Unknown", []string{"t3"}}
)

// detectCPUGeneration detects the CPU microarchitecture generation using CPUID.
func detectCPUGeneration() cpuGeneration {
	switch cpuid.CPU.VendorID {
	case cpuid.Intel:
		return detectIntelGeneration(cpuid.CPU.Family, cpuid.CPU.Model)
	case cpuid.AMD:
		return detectAMDGeneration(cpuid.CPU.Family, cpuid.CPU.Model)
	default:
		if runtime.GOARCH == "arm64" {
			return detectARMGeneration()
		}
		slog.Warn("CPUID vendor not recognized, falling back to brand string detection",
			"vendorID", cpuid.CPU.VendorID, "brand", cpuid.CPU.BrandName)
	}
	return detectGenerationFromBrand(cpuid.CPU.BrandName, runtime.GOARCH)
}

// detectIntelGeneration maps Intel CPUID Family 6 model numbers to generations.
func detectIntelGeneration(family, model int) cpuGeneration {
	if family != 6 {
		slog.Warn("Unrecognized Intel CPU family, exposing t3 only", "family", family, "model", model)
		return genUnknownIntel
	}

	switch model {
	case 79, 86: // Broadwell server (BDX, BDX-DE)
		return genIntelBroadwell
	case 85: // Skylake-SP / Cascade Lake-SP
		return genIntelSkylake
	case 106, 108: // Ice Lake server (ICX, ICX-D)
		return genIntelIceLake
	case 143, 207: // Sapphire Rapids (SPR, EMR)
		return genIntelSapphireRapids
	case 173, 174: // Granite Rapids (GNR, GNR-D)
		return genIntelGraniteRapids

	// Consumer/desktop mapped to nearest server generation
	case 151, 154: // Alder Lake
		return genIntelIceLake
	case 183, 191: // Raptor Lake
		return genIntelSapphireRapids
	case 197, 198: // Arrow Lake
		return genIntelGraniteRapids
	}

	slog.Warn("Unrecognized Intel CPU model, exposing t3 only", "family", family, "model", model, "brand", cpuid.CPU.BrandName)
	return genUnknownIntel
}

// detectAMDGeneration maps AMD CPUID family/model to generations.
func detectAMDGeneration(family, model int) cpuGeneration {
	switch family {
	case 23: // Zen, Zen+, Zen 2 (Naples, Rome, Matisse, etc.)
		return genAMDZen
	case 25: // Zen 3 and Zen 4 share family 25; model ranges distinguish them
		// Zen 3 models: 0x00-0x0F (Milan/Vermeer), 0x20-0x5F (Rembrandt/Barcelo)
		// Zen 4 models: 0x10-0x1F (Genoa), 0x60+ (Raphael/Phoenix)
		isZen3 := model < 0x10 || (model >= 0x20 && model < 0x60)
		if isZen3 {
			return genAMDZen3
		}
		return genAMDZen4
	case 26: // Zen 5 (Turin, Granite Ridge)
		return genAMDZen5
	}

	slog.Warn("Unrecognized AMD CPU family, exposing t3a only", "family", family, "model", model, "brand", cpuid.CPU.BrandName)
	return genUnknownAMD
}

// detectARMGeneration detects ARM CPU generation using brand string and feature flags.
func detectARMGeneration() cpuGeneration {
	brand := strings.ToLower(cpuid.CPU.BrandName)

	// Check for specific Graviton versions
	if strings.Contains(brand, "graviton4") || strings.Contains(brand, "neoverse-v2") {
		return genARMNeoverseV2
	}
	if strings.Contains(brand, "graviton3") || strings.Contains(brand, "neoverse-v1") {
		return genARMNeoverseV1
	}
	if strings.Contains(brand, "graviton2") || strings.Contains(brand, "neoverse-n1") {
		return genARMNeoverseN1
	}

	// SVE indicates Neoverse V1+ but cannot distinguish V1 from V2
	if cpuid.CPU.Has(cpuid.SVE) {
		slog.Warn("ARM generation detected via SVE heuristic, defaulting to Neoverse V1", "brand", cpuid.CPU.BrandName)
		return genARMNeoverseV1
	}

	// Default ARM to Neoverse N1 (most common)
	slog.Warn("Could not identify ARM generation, defaulting to Neoverse N1", "brand", cpuid.CPU.BrandName)
	return genARMNeoverseN1
}

// detectGenerationFromBrand is a fallback for VMs/hypervisors where CPUID may be virtualized.
func detectGenerationFromBrand(brand, arch string) cpuGeneration {
	if arch == "arm64" {
		return detectARMGeneration()
	}

	brandLower := strings.ToLower(brand)

	// Intel patterns
	if strings.Contains(brandLower, "xeon") || strings.Contains(brandLower, "intel") {
		switch {
		case strings.Contains(brandLower, "granite"):
			return genIntelGraniteRapids
		case strings.Contains(brandLower, "sapphire"):
			return genIntelSapphireRapids
		case strings.Contains(brandLower, "ice lake") || strings.Contains(brandLower, "icelake"):
			return genIntelIceLake
		case strings.Contains(brandLower, "cascade") || strings.Contains(brandLower, "skylake"):
			return genIntelSkylake
		case strings.Contains(brandLower, "broadwell"):
			return genIntelBroadwell
		default:
			// Generic Intel — default to Skylake (most common in VMs)
			slog.Warn("Intel CPU detected via brand string but generation unknown, defaulting to Skylake", "brand", brand)
			return genIntelSkylake
		}
	}

	// AMD patterns
	if strings.Contains(brandLower, "epyc") || strings.Contains(brandLower, "amd") || strings.Contains(brandLower, "ryzen") {
		switch {
		case strings.Contains(brandLower, "turin"):
			return genAMDZen5
		case strings.Contains(brandLower, "genoa") || strings.Contains(brandLower, "9004") || strings.Contains(brandLower, "raphael"):
			return genAMDZen4
		case strings.Contains(brandLower, "milan") || strings.Contains(brandLower, "7003") || strings.Contains(brandLower, "vermeer"):
			return genAMDZen3
		default:
			// Generic AMD — default to Zen/Zen2
			slog.Warn("AMD CPU detected via brand string but generation unknown, defaulting to Zen/Zen2", "brand", brand)
			return genAMDZen
		}
	}

	slog.Warn("Unrecognized CPU, exposing t3 only", "brand", brand, "arch", arch)
	return genUnknown
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

type instanceSize struct {
	suffix   string
	vcpus    int
	memoryGB float64
}

type instanceFamilyDef struct {
	name       string
	sizes      []instanceSize
	currentGen bool
}

// Size tables for each instance category

var burstableSizes = []instanceSize{
	{"nano", 2, 0.5},
	{"micro", 2, 1},
	{"small", 2, 2},
	{"medium", 2, 4},
	{"large", 2, 8},
	{"xlarge", 4, 16},
	{"2xlarge", 8, 32},
}

var gpSizes = []instanceSize{
	{"large", 2, 8},
	{"xlarge", 4, 16},
	{"2xlarge", 8, 32},
	{"4xlarge", 16, 64},
	{"8xlarge", 32, 128},
	{"12xlarge", 48, 192},
	{"16xlarge", 64, 256},
	{"24xlarge", 96, 384},
}

// gpSizesSmall is gpSizes without 12xlarge and 24xlarge (older/ARM families).
var gpSizesSmall = slices.Clone(gpSizes[:6])

var computeSizes = []instanceSize{
	{"large", 2, 4},
	{"xlarge", 4, 8},
	{"2xlarge", 8, 16},
	{"4xlarge", 16, 32},
	{"8xlarge", 32, 64},
	{"12xlarge", 48, 96},
	{"16xlarge", 64, 128},
	{"24xlarge", 96, 192},
}

// computeSizesSmall is computeSizes without 12xlarge and 24xlarge (older/ARM families).
var computeSizesSmall = slices.Clone(computeSizes[:6])

var memorySizes = []instanceSize{
	{"large", 2, 16},
	{"xlarge", 4, 32},
	{"2xlarge", 8, 64},
	{"4xlarge", 16, 128},
	{"8xlarge", 32, 256},
	{"12xlarge", 48, 384},
	{"16xlarge", 64, 512},
	{"24xlarge", 96, 768},
}

// memorySizesSmall is memorySizes without 12xlarge and 24xlarge (older/ARM families).
var memorySizesSmall = slices.Clone(memorySizes[:6])

// instanceFamilyDefs defines all supported instance families with their vendor and sizes.
//
// We support the core families across burstable, general purpose, compute optimized,
// and memory optimized categories. The following AWS family categories are intentionally
// excluded because they require specialized hardware not available on standard bare-metal hosts:
//
//   - Local disk variants (d/n suffixes): c5d, c5ad, c5n, m5d, m5ad, m5n, m5dn, m5zn, r5d, r5ad,
//     r5n, r5dn, r5b, c6gd, c6gn, c6id, c6in, m6gd, m6id, m6idn, m6in, r6gd, r6id, r6idn, r6in,
//     c7gd, c7gn, c7i-flex, m7gd, m7i-flex, r7gd, r7iz, c8gd, c8gn, c8i-flex, m8gd, m8i-flex,
//     r8gd, r8gn, r8gb, r8i-flex — require NVMe instance storage or enhanced networking
//   - GPU/accelerator: g2-g6, g6e, g6f, gr6, gr6f, p2-p6, inf1-inf2, trn1-trn2, dl1, dl2q — require
//     GPU, Inferentia, Trainium, or other accelerator hardware. note inf1 and trn1 are not supported since its aws only hardware.
//   - Storage optimized: d2, d3, d3en, h1, i2-i8g, i7ie, i8ge, im4gn, is4gen — require dense HDD/NVMe
//   - FPGA: f1, f2 — require FPGA hardware
//   - High memory: u-*, u7i-*, x1, x1e, x2gd, x2idn, x2iedn, x2iezn, x8g — require TB-scale memory
//   - High frequency: z1d — specialized high clock-speed instances
//   - (unsupported) Dedicated host: mac*, hpc* — require macOS/Apple hardware or HPC interconnects
//   - (unsupported) Video: vt1 — requires video transcoding hardware
//   - Legacy (pre-gen4): a1, c1, c3, cc1, cc2, cg1, cr1, hi1, hs1, m1, m2, m3, r3, t1
var instanceFamilyDefs = []instanceFamilyDef{
	// Burstable
	{name: "t2", sizes: burstableSizes, currentGen: false},
	{name: "t3", sizes: burstableSizes, currentGen: true},
	{name: "t3a", sizes: burstableSizes, currentGen: true},
	{name: "t4g", sizes: burstableSizes, currentGen: true},

	// General Purpose (1:4 vCPU:memory)
	{name: "m4", sizes: gpSizesSmall, currentGen: false},
	{name: "m5", sizes: gpSizes, currentGen: true},
	{name: "m5a", sizes: gpSizes, currentGen: true},
	{name: "m6i", sizes: gpSizes, currentGen: true},
	{name: "m6a", sizes: gpSizes, currentGen: true},
	{name: "m6g", sizes: gpSizesSmall, currentGen: true},
	{name: "m7i", sizes: gpSizes, currentGen: true},
	{name: "m7a", sizes: gpSizes, currentGen: true},
	{name: "m7g", sizes: gpSizesSmall, currentGen: true},
	{name: "m8i", sizes: gpSizes, currentGen: true},
	{name: "m8a", sizes: gpSizes, currentGen: true},
	{name: "m8g", sizes: gpSizesSmall, currentGen: true},

	// Compute Optimized (1:2 vCPU:memory)
	{name: "c4", sizes: computeSizesSmall, currentGen: false},
	{name: "c5", sizes: computeSizes, currentGen: true},
	{name: "c5a", sizes: computeSizes, currentGen: true},
	{name: "c6i", sizes: computeSizes, currentGen: true},
	{name: "c6a", sizes: computeSizes, currentGen: true},
	{name: "c6g", sizes: computeSizesSmall, currentGen: true},
	{name: "c7i", sizes: computeSizes, currentGen: true},
	{name: "c7a", sizes: computeSizes, currentGen: true},
	{name: "c7g", sizes: computeSizesSmall, currentGen: true},
	{name: "c8i", sizes: computeSizes, currentGen: true},
	{name: "c8a", sizes: computeSizes, currentGen: true},
	{name: "c8g", sizes: computeSizesSmall, currentGen: true},

	// Memory Optimized (1:8 vCPU:memory)
	{name: "r4", sizes: memorySizesSmall, currentGen: false},
	{name: "r5", sizes: memorySizes, currentGen: true},
	{name: "r5a", sizes: memorySizes, currentGen: true},
	{name: "r6i", sizes: memorySizes, currentGen: true},
	{name: "r6a", sizes: memorySizes, currentGen: true},
	{name: "r6g", sizes: memorySizesSmall, currentGen: true},
	{name: "r7i", sizes: memorySizes, currentGen: true},
	{name: "r7a", sizes: memorySizes, currentGen: true},
	{name: "r7g", sizes: memorySizesSmall, currentGen: true},
	{name: "r8i", sizes: memorySizes, currentGen: true},
	{name: "r8a", sizes: memorySizes, currentGen: true},
	{name: "r8g", sizes: memorySizesSmall, currentGen: true},
}

// generateInstanceTypes creates the instance type map for the given CPU generation.
// It generates all instance families matching the generation's family list across
// burstable, general purpose, compute optimized, and memory optimized categories.
func generateInstanceTypes(gen cpuGeneration, arch string) map[string]*ec2.InstanceTypeInfo {
	// Build a set of allowed families for fast lookup
	allowed := make(map[string]bool, len(gen.families))
	for _, f := range gen.families {
		allowed[f] = true
	}

	instanceTypes := make(map[string]*ec2.InstanceTypeInfo)
	for _, def := range instanceFamilyDefs {
		if !allowed[def.name] {
			continue
		}
		burstable := strings.HasPrefix(def.name, "t")
		for _, size := range def.sizes {
			name := fmt.Sprintf("%s.%s", def.name, size.suffix)
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
				CurrentGeneration:             aws.Bool(def.currentGen),
				BurstablePerformanceSupported: aws.Bool(burstable),
				Hypervisor:                    aws.String("kvm"),
				SupportedVirtualizationTypes:  []*string{aws.String("hvm")},
				SupportedRootDeviceTypes:      []*string{aws.String("ebs")},
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
		slog.Warn("Failed to get system memory, using default of 8GB", "err", err)
		totalMemGB = 8.0 // Default to 8GB if we can't get the actual memory
	}

	// Determine architecture
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}

	// Detect CPU generation and generate matching instance types
	gen := detectCPUGeneration()
	instanceTypes := generateInstanceTypes(gen, arch)

	if len(instanceTypes) == 0 {
		slog.Error("No instance types generated, daemon will be unable to run VMs",
			"generation", gen.name, "arch", arch)
	}

	slog.Info("System resources detected",
		"vCPUs", numCPU, "memGB", totalMemGB,
		"generation", gen.name, "families", gen.families,
		"instanceTypes", len(instanceTypes), "os", runtime.GOOS)

	return &ResourceManager{
		availableVCPU: numCPU,
		availableMem:  totalMemGB,
		instanceTypes: instanceTypes,
	}
}

// instanceTypeVCPUs returns the default vCPU count for an instance type, or 0 if unavailable.
func instanceTypeVCPUs(it *ec2.InstanceTypeInfo) int64 {
	if it.VCpuInfo != nil && it.VCpuInfo.DefaultVCpus != nil {
		return *it.VCpuInfo.DefaultVCpus
	}
	return 0
}

// instanceTypeMemoryMiB returns the memory in MiB for an instance type, or 0 if unavailable.
func instanceTypeMemoryMiB(it *ec2.InstanceTypeInfo) int64 {
	if it.MemoryInfo != nil && it.MemoryInfo.SizeInMiB != nil {
		return *it.MemoryInfo.SizeInMiB
	}
	return 0
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
		vCPUs := instanceTypeVCPUs(it)
		memoryGB := float64(instanceTypeMemoryMiB(it)) / 1024.0

		if vCPUs == 0 || memoryGB == 0 {
			continue
		}

		remainingVCPU := rm.availableVCPU - rm.allocatedVCPU
		remainingMem := rm.availableMem - rm.allocatedMem

		// Calculate how many instances of this type can fit based on REMAINING host capacity
		countVCPU := remainingVCPU / int(vCPUs)
		countMem := int(remainingMem / memoryGB)

		// Use the minimum of CPU slots and Memory slots
		count := min(countMem, countVCPU)

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

// GetResourceStats returns current resource allocation stats for the node status response.
func (rm *ResourceManager) GetResourceStats() (totalVCPU int, totalMemGB float64, allocVCPU int, allocMemGB float64, caps []config.InstanceTypeCap) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	totalVCPU = rm.availableVCPU
	totalMemGB = rm.availableMem
	allocVCPU = rm.allocatedVCPU
	allocMemGB = rm.allocatedMem

	remainingVCPU := rm.availableVCPU - rm.allocatedVCPU
	remainingMem := rm.availableMem - rm.allocatedMem

	for _, it := range rm.instanceTypes {
		vCPUs := instanceTypeVCPUs(it)
		memGB := float64(instanceTypeMemoryMiB(it)) / 1024.0
		if vCPUs == 0 || memGB == 0 {
			continue
		}
		countVCPU := remainingVCPU / int(vCPUs)
		countMem := int(remainingMem / memGB)
		count := min(countMem, countVCPU)
		if count < 0 {
			count = 0
		}
		name := ""
		if it.InstanceType != nil {
			name = *it.InstanceType
		}
		caps = append(caps, config.InstanceTypeCap{
			Name:      name,
			VCPU:      int(vCPUs),
			MemoryGB:  memGB,
			Available: count,
		})
	}
	return
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
		detachDelay:       1 * time.Second,
	}
}

// natsSub defines a single NATS subscription entry for the table-driven setup.
type natsSub struct {
	topic      string
	handler    nats.MsgHandler
	queueGroup string // empty = plain Subscribe (fan-out)
}

// subscribeAll registers all NATS subscriptions using a table-driven approach.
func (d *Daemon) subscribeAll() error {
	subs := []natsSub{
		// ec2.RunInstances is handled by dynamic per-instance-type subscriptions
		// managed by ResourceManager.initSubscriptions()
		{"ec2.CreateKeyPair", d.handleEC2CreateKeyPair, "hive-workers"},
		{"ec2.DeleteKeyPair", d.handleEC2DeleteKeyPair, "hive-workers"},
		{"ec2.DescribeKeyPairs", d.handleEC2DescribeKeyPairs, "hive-workers"},
		{"ec2.ImportKeyPair", d.handleEC2ImportKeyPair, "hive-workers"},
		{"ec2.DescribeImages", d.handleEC2DescribeImages, "hive-workers"},
		{"ec2.CreateImage", d.handleEC2CreateImage, ""},
		{"ec2.CreateVolume", d.handleEC2CreateVolume, "hive-workers"},
		{"ec2.DescribeVolumes", d.handleEC2DescribeVolumes, "hive-workers"},
		{"ec2.ModifyVolume", d.handleEC2ModifyVolume, "hive-workers"},
		{"ec2.DeleteVolume", d.handleEC2DeleteVolume, "hive-workers"},
		{"ec2.DescribeVolumeStatus", d.handleEC2DescribeVolumeStatus, "hive-workers"},
		{"ec2.CreateSnapshot", d.handleEC2CreateSnapshot, "hive-workers"},
		{"ec2.DescribeSnapshots", d.handleEC2DescribeSnapshots, "hive-workers"},
		{"ec2.DeleteSnapshot", d.handleEC2DeleteSnapshot, "hive-workers"},
		{"ec2.CopySnapshot", d.handleEC2CopySnapshot, "hive-workers"},
		{"ec2.CreateTags", d.handleEC2CreateTags, "hive-workers"},
		{"ec2.DeleteTags", d.handleEC2DeleteTags, "hive-workers"},
		{"ec2.DescribeTags", d.handleEC2DescribeTags, "hive-workers"},
		{"ec2.CreateEgressOnlyInternetGateway", d.handleEC2CreateEgressOnlyInternetGateway, "hive-workers"},
		{"ec2.DeleteEgressOnlyInternetGateway", d.handleEC2DeleteEgressOnlyInternetGateway, "hive-workers"},
		{"ec2.DescribeEgressOnlyInternetGateways", d.handleEC2DescribeEgressOnlyInternetGateways, "hive-workers"},
		{"ec2.start", d.handleEC2StartStoppedInstance, "hive-workers"},
		{"ec2.terminate", d.handleEC2TerminateStoppedInstance, "hive-workers"},
		{"ec2.DescribeStoppedInstances", d.handleEC2DescribeStoppedInstances, "hive-workers"},
		// these 2 fan out to all nodes and gateway aggregates the results
		{"ec2.DescribeInstances", d.handleEC2DescribeInstances, ""},
		{"ec2.DescribeInstanceTypes", d.handleEC2DescribeInstanceTypes, ""},
		{"ec2.EnableEbsEncryptionByDefault", d.handleEC2EnableEbsEncryptionByDefault, "hive-workers"},
		{"ec2.DisableEbsEncryptionByDefault", d.handleEC2DisableEbsEncryptionByDefault, "hive-workers"},
		{"ec2.GetEbsEncryptionByDefault", d.handleEC2GetEbsEncryptionByDefault, "hive-workers"},
		{"ec2.GetSerialConsoleAccessStatus", d.handleEC2GetSerialConsoleAccessStatus, "hive-workers"},
		{"ec2.EnableSerialConsoleAccess", d.handleEC2EnableSerialConsoleAccess, "hive-workers"},
		{"ec2.DisableSerialConsoleAccess", d.handleEC2DisableSerialConsoleAccess, "hive-workers"},
		{fmt.Sprintf("hive.admin.%s.health", d.node), d.handleHealthCheck, ""},
		{"hive.nodes.discover", d.handleNodeDiscover, ""},
		{"hive.node.status", d.handleNodeStatus, ""},
		{"hive.node.vms", d.handleNodeVMs, ""},
		// Coordinated cluster shutdown phases (fan-out, no queue group)
		{"hive.cluster.shutdown.gate", d.handleShutdownGate, ""},
		{"hive.cluster.shutdown.drain", d.handleShutdownDrain, ""},
		{"hive.cluster.shutdown.storage", d.handleShutdownStorage, ""},
		{"hive.cluster.shutdown.persist", d.handleShutdownPersist, ""},
		{"hive.cluster.shutdown.infra", d.handleShutdownInfra, ""},
	}

	for _, s := range subs {
		var sub *nats.Subscription
		var err error
		if s.queueGroup != "" {
			sub, err = d.natsConn.QueueSubscribe(s.topic, s.queueGroup, s.handler)
		} else {
			sub, err = d.natsConn.Subscribe(s.topic, s.handler)
		}
		if err != nil {
			return fmt.Errorf("failed to subscribe to %s: %w", s.topic, err)
		}
		d.natsSubscriptions[s.topic] = sub
		slog.Info("Subscribed to NATS topic", "topic", s.topic, "queue", s.queueGroup)
	}
	return nil
}

// Start initializes and starts the daemon
func (d *Daemon) Start() error {
	if err := d.connectNATS(); err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// ClusterManager must start before JetStream init so other nodes can join
	// via /join endpoint and help form the NATS cluster.
	if err := d.ClusterManager(); err != nil {
		return fmt.Errorf("failed to start cluster manager: %w", err)
	}

	if err := d.initJetStream(); err != nil {
		return fmt.Errorf("failed to initialize JetStream: %w", err)
	}

	// Write service manifest so other nodes know what this node runs
	if d.jsManager != nil {
		if err := d.jsManager.WriteServiceManifest(
			d.node,
			d.config.GetServices(),
			d.config.NATS.Host,
			d.config.Predastore.Host,
		); err != nil {
			slog.Warn("Failed to write service manifest", "error", err)
		}
	}

	// Create services before loading/launching instances, since LaunchInstance depends on them
	store := objectstore.NewS3ObjectStoreFromConfig(d.config.Predastore.Host, d.config.Predastore.Region, d.config.AccessKey, d.config.SecretKey)
	d.instanceService = handlers_ec2_instance.NewInstanceServiceImpl(d.config, d.resourceMgr.instanceTypes, d.natsConn, &d.Instances, store)
	d.keyService = handlers_ec2_key.NewKeyServiceImpl(d.config)
	d.imageService = handlers_ec2_image.NewImageServiceImpl(d.config, d.natsConn)
	snapSvc, snapshotKV, err := handlers_ec2_snapshot.NewSnapshotServiceImplWithNATS(d.config, d.natsConn)
	if err != nil {
		return fmt.Errorf("failed to initialize snapshot service with NATS KV: %w", err)
	}
	d.snapshotService = snapSvc
	d.volumeService = handlers_ec2_volume.NewVolumeServiceImpl(d.config, d.natsConn, snapshotKV)
	d.tagsService = handlers_ec2_tags.NewTagsServiceImpl(d.config)

	if eigwSvc, err := handlers_ec2_eigw.NewEgressOnlyIGWServiceImplWithNATS(d.config, d.natsConn); err != nil {
		slog.Warn("Failed to initialize EIGW service, falling back to in-memory", "error", err)
		d.eigwService = handlers_ec2_eigw.NewEgressOnlyIGWServiceImpl(d.config)
	} else {
		d.eigwService = eigwSvc
	}
	accountSvc, err := handlers_ec2_account.NewAccountSettingsServiceImplWithNATS(d.config, d.natsConn)
	if err != nil {
		slog.Warn("Failed to create account settings service with NATS, using in-memory fallback", "error", err)
		accountSvc = handlers_ec2_account.NewAccountSettingsServiceImpl(d.config)
	}
	d.accountService = accountSvc

	// Protect daemon from OOM killer (prefer killing QEMU VMs instead)
	if err := utils.SetOOMScore(os.Getpid(), -500); err != nil {
		slog.Warn("Failed to set daemon OOM score", "err", err)
	}

	d.waitForClusterReady()
	d.restoreInstances()

	if err := d.subscribeAll(); err != nil {
		return fmt.Errorf("failed to subscribe to NATS topics: %w", err)
	}

	// Initialize dynamic per-instance-type subscriptions for capacity-aware routing.
	// Each instance type gets its own NATS topic (ec2.RunInstances.{type}) so requests
	// are only routed to nodes with available capacity.
	d.resourceMgr.initSubscriptions(d.natsConn, d.handleEC2RunInstances)

	d.startHeartbeat()
	d.setupShutdown()
	d.awaitShutdown()

	return nil
}

// connectNATS establishes a connection to the NATS server with reconnect handling.
func (d *Daemon) connectNATS() error {
	nc, err := utils.ConnectNATS(d.config.NATS.Host, d.config.NATS.ACL.Token)
	if err != nil {
		return err
	}
	d.natsConn = nc
	return nil
}

// initJetStream initializes JetStream with retry/backoff and upgrades replicas for multi-node clusters.
func (d *Daemon) initJetStream() error {
	const maxRetries = 10
	retryDelay := 500 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		var err error
		d.jsManager, err = NewJetStreamManager(d.natsConn, 1)
		if err == nil {
			err = d.jsManager.InitKVBucket()
		}

		if err == nil {
			err = d.jsManager.InitClusterStateBucket()
		}

		if err == nil {
			slog.Info("JetStream KV stores initialized successfully", "replicas", 1, "attempts", attempt)
			lastErr = nil
			break
		}

		lastErr = err
		slog.Warn("Failed to init JetStream", "error", err, "attempt", attempt, "maxRetries", maxRetries)

		if attempt < maxRetries {
			time.Sleep(retryDelay)
			retryDelay = min(retryDelay*2, 5*time.Second)
		}
	}

	if lastErr != nil {
		return fmt.Errorf("failed to initialize JetStream after %d attempts: %w", maxRetries, lastErr)
	}

	// Upgrade replicas if cluster has more than one node
	clusterSize := len(d.clusterConfig.Nodes)
	if clusterSize > 1 {
		if err := d.jsManager.UpdateReplicas(clusterSize); err != nil {
			slog.Warn("Failed to upgrade JetStream replicas on startup (other NATS nodes may not be ready)", "targetReplicas", clusterSize, "error", err)
		}
	}

	return nil
}

// waitForClusterReady waits until dependent infrastructure services are reachable
// before starting VM recovery. This prevents races where VMs try to mount volumes
// before viperblock/predastore are ready.
func (d *Daemon) waitForClusterReady() {
	slog.Info("Waiting for cluster readiness...")
	maxWait := 2 * time.Minute
	start := time.Now()
	interval := 2 * time.Second

	for time.Since(start) < maxWait {
		ready := true
		var reason string

		// Viperblock must be reachable (local or remote)
		if ready && !d.checkViperblockReady() {
			ready = false
			reason = "viperblock not ready"
		}

		// Predastore must be reachable (local or remote)
		if ready && !d.checkPredastoreReady() {
			ready = false
			reason = "predastore not ready"
		}

		if ready {
			slog.Info("Cluster readiness check passed", "elapsed", time.Since(start))
			return
		}

		slog.Debug("Cluster not ready, waiting...", "reason", reason, "elapsed", time.Since(start))
		time.Sleep(interval)
	}

	slog.Warn("Cluster readiness timeout, proceeding with recovery anyway", "maxWait", maxWait)
}

// checkViperblockReady checks if viperblock is reachable by verifying
// the NATS connection is up (viperblock subscribes to ebs topics on NATS).
func (d *Daemon) checkViperblockReady() bool {
	if d.natsConn == nil {
		return false
	}
	return d.natsConn.IsConnected()
}

// checkPredastoreReady checks if predastore is reachable via TCP.
func (d *Daemon) checkPredastoreReady() bool {
	host := d.config.Predastore.Host
	if host == "" {
		return true // no predastore configured, skip check
	}
	conn, err := net.DialTimeout("tcp", host, 3*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// migrateStoppedToSharedKV writes a stopped instance to shared KV and removes
// it from the local instance map. Returns true if migration succeeded.
func (d *Daemon) migrateStoppedToSharedKV(instance *vm.VM) bool {
	if d.jsManager == nil {
		return false
	}
	instance.LastNode = d.node
	if err := d.jsManager.WriteStoppedInstance(instance.ID, instance); err != nil {
		slog.Error("Failed to migrate stopped instance to shared KV",
			"instance", instance.ID, "err", err)
		return false
	}
	delete(d.Instances.VMS, instance.ID)
	slog.Info("Migrated stopped instance to shared KV", "instance", instance.ID)
	return true
}

// maxConcurrentRecovery limits how many VMs are relaunched in parallel during recovery.
const maxConcurrentRecovery = 2

// restoreInstances loads persisted VM state and re-launches instances that are
// neither terminated nor flagged as user-stopped.
func (d *Daemon) restoreInstances() {
	// Check for clean shutdown marker
	cleanShutdown := false
	if d.jsManager != nil {
		if marker, err := d.jsManager.ReadShutdownMarker(d.node); err == nil {
			cleanShutdown = marker
			if marker {
				slog.Info("Clean shutdown marker found, trusting KV state")
				_ = d.jsManager.DeleteShutdownMarker(d.node)
			}
		}
	}

	if !cleanShutdown {
		slog.Warn("No clean shutdown marker — possible crash recovery, validating QEMU PIDs carefully")
		time.Sleep(3 * time.Second)
	}

	err := d.LoadState()
	if err != nil {
		slog.Warn("Failed to load state, continuing with empty state", "error", err)
		return
	}

	slog.Info("Loaded state", "instance count", len(d.Instances.VMS))

	// Ensure mutexes and QMP clients are usable after deserialization
	d.Instances.Mu = sync.Mutex{}

	// Phase 1: Reconnect running QEMU, finalize transitional states, collect VMs to relaunch
	var toLaunch []*vm.VM

	for i := range d.Instances.VMS {
		d.Instances.VMS[i].EBSRequests.Mu = sync.Mutex{}
		d.Instances.VMS[i].QMPClient = &qmp.QMPClient{}

		instance := d.Instances.VMS[i]

		if instance.Status == vm.StateTerminated {
			slog.Info("Instance state is terminated, skipping", "instance", instance.ID)
			continue
		}

		if instance.Status == vm.StateStopped {
			d.migrateStoppedToSharedKV(instance)
			continue
		}

		instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
		if !ok {
			slog.Error("Instance type not found in current type map, cannot re-allocate resources",
				"instanceId", instance.ID, "instanceType", instance.InstanceType)
		} else {
			slog.Info("Re-allocating resources for instance", "instanceId", instance.ID, "type", instance.InstanceType)
			if err := d.resourceMgr.allocate(instanceType); err != nil {
				slog.Error("Failed to re-allocate resources for instance on startup", "instanceId", instance.ID, "err", err)
			}
		}

		// Check if QEMU process is still alive from before the restart
		if d.isInstanceProcessRunning(instance) {
			slog.Info("Instance QEMU process still alive, reconnecting", "instance", instance.ID)
			if err := d.reconnectInstance(instance); err != nil {
				slog.Error("Failed to reconnect to running instance", "instanceId", instance.ID, "err", err)
			}
			continue
		}

		// QEMU is not running -- resolve transitional states from interrupted operations
		switch instance.Status {
		case vm.StateStopping, vm.StateShuttingDown:
			prevStatus := instance.Status
			if instance.Status == vm.StateStopping {
				instance.Status = vm.StateStopped
			} else {
				instance.Status = vm.StateTerminated
			}
			slog.Info("QEMU exited during transition, finalizing state",
				"instance", instance.ID, "from", prevStatus, "to", instance.Status)

			if instance.Status == vm.StateStopped && d.migrateStoppedToSharedKV(instance) {
				continue
			}

			if err := d.WriteState(); err != nil {
				slog.Error("Failed to persist state, will retry on next restart",
					"instance", instance.ID, "error", err)
				instance.Status = prevStatus // revert so next restart retries
			}
			continue
		case vm.StateRunning:
			// Was running but QEMU died - reset to pending so LaunchInstance can transition to running
			instance.Status = vm.StatePending
			slog.Info("Instance was running but QEMU exited, relaunching", "instance", instance.ID)
		}

		toLaunch = append(toLaunch, instance)
	}

	// Phase 2: Relaunch crashed VMs with semaphore-based throttling
	if len(toLaunch) > 0 {
		slog.Info("Launching instances (recovery)", "count", len(toLaunch), "maxConcurrent", maxConcurrentRecovery)
		sem := make(chan struct{}, maxConcurrentRecovery)
		var wg sync.WaitGroup

		for _, instance := range toLaunch {
			sem <- struct{}{} // acquire
			wg.Add(1)
			go func(inst *vm.VM) {
				defer wg.Done()
				defer func() { <-sem }() // release
				slog.Info("Launching instance (recovery)", "instance", inst.ID)
				if err := d.LaunchInstance(inst); err != nil {
					slog.Error("Failed to launch instance", "instanceId", inst.ID, "err", err)
				}
			}(instance)
		}
		wg.Wait()
	}

	// Persist state after any migrations/removals during restore
	if err := d.WriteState(); err != nil {
		slog.Error("Failed to persist state after restore", "error", err)
	}
}

// isInstanceProcessRunning checks if the QEMU process for an instance is still alive.
func (d *Daemon) isInstanceProcessRunning(instance *vm.VM) bool {
	pid, err := utils.ReadPidFile(instance.ID)
	if err != nil || pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// reconnectInstance re-establishes QMP and NATS connections to a running QEMU instance
// after a daemon restart. This bypasses the state machine since recovery is not a
// normal state transition.
func (d *Daemon) reconnectInstance(instance *vm.VM) error {
	if err := d.CreateQMPClient(instance); err != nil {
		return fmt.Errorf("failed to reconnect QMP: %w", err)
	}

	d.mu.Lock()
	sub, err := d.natsConn.Subscribe(fmt.Sprintf("ec2.cmd.%s", instance.ID), d.handleEC2Events)
	if err != nil {
		d.mu.Unlock()
		// Clean up the QMP connection we just opened
		if instance.QMPClient != nil && instance.QMPClient.Conn != nil {
			_ = instance.QMPClient.Conn.Close()
			instance.QMPClient = nil
		}
		return fmt.Errorf("failed to subscribe to NATS: %w", err)
	}
	d.natsSubscriptions[instance.ID] = sub
	d.mu.Unlock()

	instance.Status = vm.StateRunning

	if err := d.WriteState(); err != nil {
		return fmt.Errorf("failed to persist reconnected instance state: %w", err)
	}

	slog.Info("Successfully reconnected to running instance", "instance", instance.ID)
	return nil
}

// awaitShutdown blocks until the daemon's shutdown wait group completes.
func (d *Daemon) awaitShutdown() {
	done := make(chan struct{})
	go func() {
		d.shutdownWg.Wait()
		close(done)
	}()
	<-done
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

		serviceHealth := make(map[string]string)
		for _, svc := range d.config.GetServices() {
			serviceHealth[svc] = "ok"
		}
		// For remote dependencies, check connectivity
		if !d.config.HasService("nats") {
			if d.natsConn != nil && d.natsConn.IsConnected() {
				serviceHealth["nats"] = "remote_ok"
			} else {
				serviceHealth["nats"] = "remote_unreachable"
			}
		}

		response := config.NodeHealthResponse{
			Node:          d.node,
			Status:        "running",
			ConfigHash:    configHash,
			Epoch:         d.clusterConfig.Epoch,
			Uptime:        int64(time.Since(d.startTime).Seconds()),
			Services:      d.config.GetServices(),
			ServiceHealth: serviceHealth,
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
		configDir := filepath.Dir(d.configPath)
		caCertPath := filepath.Join(configDir, "ca.pem")
		caKeyPath := filepath.Join(configDir, "ca.key")

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

		// Read predastore.toml to share with joining node (for multi-node predastore)
		var predastoreConfig string
		predastorePath := filepath.Join(configDir, "predastore", "predastore.toml")
		if content, err := os.ReadFile(predastorePath); err == nil {
			predastoreConfig = string(content)
		} else {
			slog.Warn("Could not read predastore.toml for join response", "path", predastorePath, "error", err)
		}

		return c.JSON(config.NodeJoinResponse{
			Success:          true,
			Message:          fmt.Sprintf("node %s successfully joined cluster", req.Node),
			SharedData:       sharedData,
			ConfigHash:       configHash,
			JoiningNode:      req.Node,
			CACert:           caCert,
			CAKey:            caKey,
			PredastoreConfig: predastoreConfig,
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

// WriteState writes the instance state to JetStream KV store (required).
// It acquires d.Instances.Mu internally.
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
			// QMP events are informational only — state transitions are driven
			// by the command handlers that initiate the action, avoiding races
			// between event-driven and command-driven transitions.
			slog.Info("QMP event", "event", msg["event"], "instanceId", instanceId)
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

func (d *Daemon) stopInstance(instances map[string]*vm.VM, deleteVolume bool) error {

	// Signal to shutdown each VM
	var wg sync.WaitGroup

	// Run asynchronously within a worker group
	for _, instance := range instances {

		wg.Go(func() {

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
					if err := utils.KillProcess(pid); err != nil {
						slog.Error("Failed to kill process", "pid", pid, "id", instance.ID, "err", err)
					}
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

				msg, err := d.natsConn.Request(d.ebsTopic("unmount"), ebsUnMountRequest, 30*time.Second)
				if err != nil {
					slog.Error("Failed to unmount volume", "name", ebsRequest.Name, "id", instance.ID, "err", err)
				} else {
					slog.Info("Unmounted Viperblock volume", "id", instance.ID, "data", string(msg.Data))
				}

				// Update volume state to "available" for all user-visible volumes (boot + hot-attached)
				if !ebsRequest.EFI && !ebsRequest.CloudInit {
					if err := d.volumeService.UpdateVolumeState(ebsRequest.Name, "available", "", ""); err != nil {
						slog.Error("Failed to update volume state to available", "volumeId", ebsRequest.Name, "err", err)
					}
				}
			}

			// If flagged for termination, clean up volumes
			if deleteVolume {
				for _, ebsRequest := range instance.EBSRequests.Requests {
					// Internal volumes (EFI, cloud-init) are always cleaned up via ebs.delete
					// to stop viperblockd processes. S3 data cleanup happens via DeleteVolume
					// on the parent root volume (which deletes -efi/ and -cloudinit/ prefixes).
					if ebsRequest.EFI || ebsRequest.CloudInit {
						ebsDeleteData, err := json.Marshal(config.EBSDeleteRequest{Volume: ebsRequest.Name})
						if err != nil {
							slog.Error("Failed to marshal ebs.delete request for internal volume", "name", ebsRequest.Name, "err", err)
							continue
						}
						deleteMsg, err := d.natsConn.Request("ebs.delete", ebsDeleteData, 30*time.Second)
						if err != nil {
							slog.Warn("Failed to send ebs.delete for internal volume", "name", ebsRequest.Name, "id", instance.ID, "err", err)
						} else {
							slog.Info("Sent ebs.delete for internal volume", "name", ebsRequest.Name, "id", instance.ID, "data", string(deleteMsg.Data))
						}
						continue
					}

					// User-visible volumes: respect DeleteOnTermination flag
					if !ebsRequest.DeleteOnTermination {
						slog.Info("Volume has DeleteOnTermination=false, skipping deletion", "name", ebsRequest.Name, "id", instance.ID)
						continue
					}

					// DeleteVolume handles: NATS ebs.delete notification + S3 cleanup
					// (including -efi/ and -cloudinit/ sub-prefixes)
					slog.Info("Deleting volume with DeleteOnTermination=true", "name", ebsRequest.Name, "id", instance.ID)
					_, err := d.volumeService.DeleteVolume(&ec2.DeleteVolumeInput{
						VolumeId: &ebsRequest.Name,
					})
					if err != nil {
						slog.Error("Failed to delete volume on termination", "name", ebsRequest.Name, "id", instance.ID, "err", err)
					} else {
						slog.Info("Deleted volume on termination", "name", ebsRequest.Name, "id", instance.ID)
					}
				}
			}

			// Deallocate resources
			instanceType := d.resourceMgr.instanceTypes[instance.InstanceType]
			if instanceType != nil {
				slog.Info("Deallocating resources for stopped instance", "instanceId", instance.ID, "type", instance.InstanceType)
				d.resourceMgr.deallocate(instanceType)
			}
		})
	}

	// Wait for all shutdowns to finish
	wg.Wait()

	// Only unsubscribe from NATS subjects when terminating (deleteVolume=true)
	// For stop operations, keep the subscription so we can receive start commands
	if deleteVolume {
		for _, instance := range instances {
			d.mu.Lock()
			if sub, ok := d.natsSubscriptions[instance.ID]; ok {
				slog.Info("Unsubscribing from NATS subject", "instance", instance.ID)
				if err := sub.Unsubscribe(); err != nil {
					slog.Error("Failed to unsubscribe from NATS subject", "instance", instance.ID, "err", err)
				}
				delete(d.natsSubscriptions, instance.ID)
			}
			d.mu.Unlock()
		}
	}
	return nil

}

func (d *Daemon) setupShutdown() {
	d.shutdownWg.Go(func() {

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

		<-sigChan
		slog.Info("Received shutdown signal, cleaning up...")

		// Cancel context to stop heartbeat and other goroutines
		d.cancel()

		// If coordinated shutdown already handled VMs (DRAIN phase), skip stopInstance
		if d.shuttingDown.Load() {
			slog.Info("Coordinated shutdown in progress, skipping VM stop (already handled by DRAIN phase)")
		} else {
			// Pass instances to terminate
			if err := d.stopInstance(d.Instances.VMS, false); err != nil {
				slog.Error("Failed to stop instances during shutdown", "err", err)
			}
		}

		// Final cleanup
		for _, sub := range d.natsSubscriptions {
			// Unsubscribe from each subscription
			slog.Info("Unsubscribing from NATS", "subject", sub.Subject)
			if err := sub.Unsubscribe(); err != nil {
				slog.Error("Error unsubscribing from NATS", "err", err)
			}

		}

		// Write shutdown marker to cluster state KV
		if d.jsManager != nil {
			if err := d.jsManager.WriteShutdownMarker(d.node); err != nil {
				slog.Error("Failed to write shutdown marker", "err", err)
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
			slog.Info("Shutting down cluster manager...")
			if err := d.clusterApp.Shutdown(); err != nil {
				slog.Error("Error shutting down cluster manager", "err", err)
			}
		}

		slog.Info("Shutdown complete")
	})
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

			if status == vm.StateStopping || status == vm.StateStopped || status == vm.StateShuttingDown || status == vm.StateTerminated || status == vm.StateError {
				slog.Info("QMP heartbeat exiting - instance not running", "instance", instance.ID, "status", status)

				// Close the QMP client connection if it exists
				if instance.QMPClient != nil && instance.QMPClient.Conn != nil {
					if err := instance.QMPClient.Conn.Close(); err != nil {
						slog.Error("Failed to close QMP connection", "instance", instance.ID, "err", err)
					}
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

	// Unsubscribe any existing subscription (e.g. from restoreInstances for stopped instances)
	if existing, ok := d.natsSubscriptions[instance.ID]; ok {
		_ = existing.Unsubscribe()
	}

	d.natsSubscriptions[instance.ID], err = d.natsConn.Subscribe(fmt.Sprintf("ec2.cmd.%s", instance.ID), d.handleEC2Events)

	if err != nil {
		slog.Error("failed to subscribe to NATS", "err", err)
		return err
	}

	// Step 9: Update the instance metadata for running state and volume attached
	d.Instances.Mu.Lock()
	d.Instances.VMS[instance.ID] = instance
	d.Instances.Mu.Unlock()

	if err := d.TransitionState(instance, vm.StateRunning); err != nil {
		slog.Error("Failed to transition instance to running", "instanceId", instance.ID, "err", err)
		return err
	}

	// Step 10: Mark boot volumes as "in-use" now that instance is confirmed running
	instance.EBSRequests.Mu.Lock()
	for _, ebsReq := range instance.EBSRequests.Requests {
		if ebsReq.Boot {
			if err := d.volumeService.UpdateVolumeState(ebsReq.Name, "in-use", instance.ID, ""); err != nil {
				slog.Error("Failed to update volume state to in-use", "volumeId", ebsReq.Name, "err", err)
			}
		}
	}
	instance.EBSRequests.Mu.Unlock()

	return nil
}

// markInstanceFailed updates an instance status to indicate a failure during launch
func (d *Daemon) markInstanceFailed(instance *vm.VM, reason string) {
	// Set state reason before transition (requires lock)
	d.Instances.Mu.Lock()
	if instance.Instance != nil {
		instance.Instance.StateReason = &ec2.StateReason{}
		instance.Instance.StateReason.SetCode("Server.InternalError")
		instance.Instance.StateReason.SetMessage(reason)
	}
	d.Instances.Mu.Unlock()

	if err := d.TransitionState(instance, vm.StateShuttingDown); err != nil {
		slog.Error("markInstanceFailed transition failed", "instanceId", instance.ID, "err", err)
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

	vCPUs := int(instanceTypeVCPUs(instanceType))
	memoryMiB := instanceTypeMemoryMiB(instanceType)
	architecture := "x86_64"
	if instanceType.ProcessorInfo != nil && len(instanceType.ProcessorInfo.SupportedArchitectures) > 0 && instanceType.ProcessorInfo.SupportedArchitectures[0] != nil {
		architecture = *instanceType.ProcessorInfo.SupportedArchitectures[0]
	}

	instance.Config = vm.Config{
		Name:         instance.ID,
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

	// Add PCIe root ports for volume hotplug (Q35 requires explicit root ports).
	// 11 ports for /dev/sd[f-p] hotplug slots, starting at chassis 1.
	for i := 1; i <= 11; i++ {
		instance.Config.Devices = append(instance.Config.Devices, vm.Device{
			Value: fmt.Sprintf("pcie-root-port,id=hotplug%d,chassis=%d,slot=0", i, i),
		})
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
			drive.Cache = "none"

			iothreadID := "ioth-os"
			instance.Config.IOThreads = append(instance.Config.IOThreads, vm.IOThread{ID: iothreadID})

			instance.Config.Devices = append(instance.Config.Devices, vm.Device{
				Value: fmt.Sprintf("virtio-blk-pci,drive=%s,iothread=%s,num-queues=%d,bootindex=1",
					drive.ID, iothreadID, instance.Config.CPUCount),
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
	startupConfirmed := make(chan bool, 1)

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

		// Set OOM score for QEMU process (prefer killing VMs over system services)
		if err := utils.SetOOMScore(cmd.Process.Pid, 500); err != nil {
			slog.Warn("Failed to set QEMU OOM score", "pid", cmd.Process.Pid, "err", err)
		}

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

		// Block until QEMU exits
		waitErr := cmd.Wait()

		if waitErr != nil {
			slog.Error("VM process exited", "instance", instance.ID, "err", waitErr)
		}

		// Signal startup check (non-blocking)
		select {
		case exitChan <- 1:
		default:
		}

		// Wait for startup phase to complete before deciding on crash handling
		confirmed := <-startupConfirmed
		if !confirmed {
			return // Startup failed, LaunchInstance handles the error
		}

		// Handle exit: crash vs clean shutdown
		if waitErr != nil {
			d.handleInstanceCrash(instance, waitErr)
		} else {
			slog.Info("VM process exited cleanly", "instance", instance.ID)
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

	// Check if QEMU exited immediately with an error
	select {
	case exitErr := <-exitChan:
		startupConfirmed <- false // tell goroutine not to handle crash
		if exitErr != 0 {
			errorMsg := fmt.Errorf("failed: %v", exitErr)
			slog.Error("Failed to launch qemu", "err", errorMsg)
			return errorMsg
		}
	default:
		startupConfirmed <- true // goroutine will handle future crashes
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

// ebsTopic returns a node-specific EBS NATS topic, e.g. "ebs.node1.mount".
// This ensures mount/unmount requests are routed to the viperblock instance
// running on the same node as the daemon (NBD sockets are local).
func (d *Daemon) ebsTopic(action string) string {
	return fmt.Sprintf("ebs.%s.%s", d.node, action)
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

		reply, err := d.natsConn.Request(d.ebsTopic("mount"), ebsMountRequest, 30*time.Second)

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

// rollbackEBSMount sends an ebs.unmount request to undo a previously successful ebs.mount.
// Rollback failures are logged but not propagated; callers treat this as best-effort cleanup.
func (d *Daemon) rollbackEBSMount(req config.EBSRequest) {
	data, err := json.Marshal(req)
	if err != nil {
		slog.Error("rollbackEBSMount: failed to marshal unmount request", "volume", req.Name, "err", err)
		return
	}
	msg, err := d.natsConn.Request(d.ebsTopic("unmount"), data, 10*time.Second)
	if err != nil {
		slog.Error("rollbackEBSMount: ebs.unmount NATS request failed", "volume", req.Name, "err", err)
		return
	}
	var resp config.EBSUnMountResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		slog.Error("rollbackEBSMount: failed to unmarshal response", "volume", req.Name, "err", err)
		return
	}
	if resp.Error != "" {
		slog.Error("rollbackEBSMount: ebs.unmount returned error", "volume", req.Name, "err", resp.Error)
		return
	}
	if resp.Mounted {
		slog.Error("rollbackEBSMount: volume still mounted after unmount", "volume", req.Name)
		return
	}
	slog.Info("rollbackEBSMount: volume unmounted successfully", "volume", req.Name)
}

// respondWithVolumeAttachment builds an ec2.VolumeAttachment, marshals it to JSON, and
// responds on the NATS message. Used by both AttachVolume and DetachVolume handlers.
func (d *Daemon) respondWithVolumeAttachment(msg *nats.Msg, respondWithError func(string), volumeID, instanceID, device, state string) {
	attachment := ec2.VolumeAttachment{
		VolumeId:            aws.String(volumeID),
		InstanceId:          aws.String(instanceID),
		Device:              aws.String(device),
		State:               aws.String(state),
		AttachTime:          aws.Time(time.Now()),
		DeleteOnTermination: aws.Bool(false),
	}

	jsonResp, err := json.Marshal(attachment)
	if err != nil {
		slog.Error("Failed to marshal VolumeAttachment response", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	if err := msg.Respond(jsonResp); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

// nextAvailableDevice finds the next available /dev/sd[f-p] device name for an instance.
// It checks both EBSRequests and BlockDeviceMappings to avoid conflicts.
func nextAvailableDevice(instance *vm.VM) string {
	usedDevices := make(map[string]bool)

	// Collect devices from existing BlockDeviceMappings
	if instance.Instance != nil {
		for _, bdm := range instance.Instance.BlockDeviceMappings {
			if bdm.DeviceName != nil {
				usedDevices[*bdm.DeviceName] = true
			}
		}
	}

	// Collect devices from EBSRequests (may not yet be in BlockDeviceMappings)
	instance.EBSRequests.Mu.Lock()
	for _, req := range instance.EBSRequests.Requests {
		if req.DeviceName != "" {
			usedDevices[req.DeviceName] = true
		}
	}
	instance.EBSRequests.Mu.Unlock()

	// AWS convention: /dev/sd[f-p] for attached volumes
	for c := 'f'; c <= 'p'; c++ {
		dev := fmt.Sprintf("/dev/sd%c", c)
		if !usedDevices[dev] {
			return dev
		}
	}

	return ""
}

// canAllocate checks how many instances of the given type can be allocated
// Returns the count that can actually be allocated (0 to count)
func (rm *ResourceManager) canAllocate(instanceType *ec2.InstanceTypeInfo, count int) int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	vCPUs := instanceTypeVCPUs(instanceType)
	memoryGB := float64(instanceTypeMemoryMiB(instanceType)) / 1024.0

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
	allocatableCount := min(countByMem, countByCPU)

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

// allocate reserves resources for an instance and updates NATS subscriptions
func (rm *ResourceManager) allocate(instanceType *ec2.InstanceTypeInfo) error {
	if rm.canAllocate(instanceType, 1) < 1 {
		instanceTypeName := ""
		if instanceType.InstanceType != nil {
			instanceTypeName = *instanceType.InstanceType
		}
		return fmt.Errorf("insufficient resources for instance type %s", instanceTypeName)
	}

	rm.mu.Lock()
	vCPUs := instanceTypeVCPUs(instanceType)
	memoryGB := float64(instanceTypeMemoryMiB(instanceType)) / 1024.0
	rm.allocatedVCPU += int(vCPUs)
	rm.allocatedMem += memoryGB
	rm.mu.Unlock()

	rm.updateInstanceSubscriptions()
	return nil
}

// deallocate releases resources for an instance and updates NATS subscriptions
func (rm *ResourceManager) deallocate(instanceType *ec2.InstanceTypeInfo) {
	rm.mu.Lock()
	vCPUs := instanceTypeVCPUs(instanceType)
	memoryGB := float64(instanceTypeMemoryMiB(instanceType)) / 1024.0
	rm.allocatedVCPU -= int(vCPUs)
	rm.allocatedMem -= memoryGB
	rm.mu.Unlock()

	rm.updateInstanceSubscriptions()
}

// initSubscriptions sets up dynamic per-instance-type NATS subscriptions.
// Called once during daemon startup after NATS is connected.
func (rm *ResourceManager) initSubscriptions(nc *nats.Conn, handler nats.MsgHandler) {
	rm.natsConn = nc
	rm.handler = handler
	rm.instanceSubs = make(map[string]*nats.Subscription)
	rm.updateInstanceSubscriptions()
}

// updateInstanceSubscriptions recalculates which instance types can fit on this
// node and subscribes/unsubscribes from the corresponding NATS topics. Each type
// gets its own topic (ec2.RunInstances.{type}) with the hive-workers queue group,
// so NATS only routes requests to nodes that can actually serve them.
func (rm *ResourceManager) updateInstanceSubscriptions() {
	if rm.natsConn == nil {
		return
	}

	rm.subsMu.Lock()
	defer rm.subsMu.Unlock()

	for typeName, typeInfo := range rm.instanceTypes {
		topic := fmt.Sprintf("ec2.RunInstances.%s", typeName)
		canFit := rm.canAllocate(typeInfo, 1) >= 1

		_, subscribed := rm.instanceSubs[topic]
		if canFit && !subscribed {
			sub, err := rm.natsConn.QueueSubscribe(topic, "hive-workers", rm.handler)
			if err != nil {
				slog.Error("Failed to subscribe to instance type topic", "topic", topic, "err", err)
				continue
			}
			rm.instanceSubs[topic] = sub
			slog.Debug("Subscribed to instance type", "topic", topic)
		} else if !canFit && subscribed {
			if err := rm.instanceSubs[topic].Unsubscribe(); err != nil {
				slog.Error("Failed to unsubscribe from instance type topic", "topic", topic, "err", err)
			}
			delete(rm.instanceSubs, topic)
			slog.Info("Unsubscribed from instance type (capacity full)", "topic", topic)
		}
	}
}
