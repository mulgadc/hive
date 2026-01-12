package daemon

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/config"
	handlers_ec2_instance "github.com/mulgadc/hive/hive/handlers/ec2/instance"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTestNATSServer starts an embedded NATS server for testing
// Using port -1 allows NATS to automatically allocate an available port
func startTestNATSServer(t *testing.T) (*server.Server, string) {
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1, // Let NATS auto-allocate an available port
		JetStream: false,
		NoLog:     true,
		NoSigs:    true,
	}

	ns, err := server.NewServer(opts)
	require.NoError(t, err, "Failed to create NATS server")

	// Start server in a goroutine
	go ns.Start()

	// Wait for server to be ready
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server failed to start")
	}

	// Get the actual URL that was assigned
	url := ns.ClientURL()
	t.Logf("Test NATS server started at: %s", url)

	return ns, url
}

// createTestDaemon creates a test daemon instance with minimal configuration
func createTestDaemon(t *testing.T, natsURL string) *Daemon {
	// Create a temporary directory for test data
	tmpDir, err := os.MkdirTemp("", "hive-daemon-test-*")
	require.NoError(t, err, "Failed to create temp directory")

	t.Cleanup(func() {
		os.RemoveAll(tmpDir)
	})

	// New cluster config
	clusterCfg := &config.ClusterConfig{
		Node:  "node-1",
		Nodes: map[string]config.Config{},
	}

	cfg := &config.Config{
		BaseDir: tmpDir,
		WalDir:  tmpDir,
		NATS: config.NATSConfig{
			Host: natsURL,
			ACL: config.NATSACL{
				Token: "",
			},
		},
		Predastore: config.PredastoreConfig{
			Host:      "http://localhost:9000",
			Bucket:    "test-bucket",
			Region:    "us-east-1",
			AccessKey: "test-access-key",
			SecretKey: "test-secret-key",
			BaseDir:   tmpDir,
		},
		AccessKey: "test-access-key",
		SecretKey: "test-secret-key",
	}

	clusterCfg.Nodes["node-1"] = *cfg

	daemon := NewDaemon(clusterCfg)

	// Connect to NATS
	nc, err := nats.Connect(natsURL)
	require.NoError(t, err, "Failed to connect to NATS")

	daemon.natsConn = nc

	// Initialize instance service (needed for handleEC2RunInstances)
	instanceTypes := make(map[string]handlers_ec2_instance.InstanceType)
	for k, v := range daemon.resourceMgr.instanceTypes {
		instanceTypes[k] = handlers_ec2_instance.InstanceType{
			Name:         v.Name,
			VCPUs:        v.VCPUs,
			MemoryGB:     v.MemoryGB,
			Architecture: v.Architecture,
		}
	}
	daemon.instanceService = handlers_ec2_instance.NewInstanceServiceImpl(cfg, instanceTypes, nc, &daemon.Instances)

	t.Cleanup(func() {
		if daemon.natsConn != nil {
			daemon.natsConn.Close()
		}
	})

	return daemon
}

// getTestInstanceType returns a valid instance type for testing based on the system's CPU
func getTestInstanceType() string {
	rm := NewResourceManager()
	return rm.instanceFamily + ".micro"
}

// createValidRunInstancesInput creates a valid RunInstancesInput for testing
func createValidRunInstancesInput() *ec2.RunInstancesInput {
	return &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-0abcdef1234567890"),
		InstanceType: aws.String(getTestInstanceType()),
		MinCount:     aws.Int64(1),
		MaxCount:     aws.Int64(1),
		KeyName:      aws.String("test-key-pair"),
		SecurityGroupIds: []*string{
			aws.String("sg-0123456789abcdef0"),
		},
		SubnetId: aws.String("subnet-6e7f829e"),
		UserData: aws.String("#!/bin/bash\necho 'test'"),
	}
}

// TestHandleEC2RunInstances_MessageParsing tests that the handler correctly parses NATS messages
func TestHandleEC2RunInstances_MessageParsing(t *testing.T) {
	// Skip integration tests on macOS since viperblock/nbdkit are not available
	if os.Getenv("SKIP_INTEGRATION") != "" {
		t.Skip("Skipping integration test - SKIP_INTEGRATION is set")
	}

	tests := []struct {
		name           string
		input          any
		expectError    bool
		errorInPayload bool
		validate       func(t *testing.T, reply *nats.Msg)
	}{
		{
			name:           "Valid RunInstancesInput",
			input:          createValidRunInstancesInput(),
			expectError:    false,
			errorInPayload: false,
			validate: func(t *testing.T, reply *nats.Msg) {
				var reservation ec2.Reservation
				err := json.Unmarshal(reply.Data, &reservation)
				require.NoError(t, err, "Failed to unmarshal reservation response")

				assert.NotNil(t, reservation.ReservationId)
				assert.Len(t, reservation.Instances, 1)

				if len(reservation.Instances) > 0 {
					instance := reservation.Instances[0]
					assert.NotNil(t, instance.InstanceId)
					assert.True(t, len(*instance.InstanceId) > 0)
					// Instance should be in pending state initially
					assert.Equal(t, int64(0), *instance.State.Code)
					assert.Equal(t, "pending", *instance.State.Name)
				}
			},
		},
		{
			name: "Invalid Instance Type",
			input: &ec2.RunInstancesInput{
				ImageId:      aws.String("ami-0abcdef1234567890"),
				InstanceType: aws.String("invalid.type"),
				MinCount:     aws.Int64(1),
				MaxCount:     aws.Int64(1),
			},
			expectError:    false, // No transport error
			errorInPayload: true,  // But payload contains error
			validate: func(t *testing.T, reply *nats.Msg) {
				// Should receive an error response
				assert.NotNil(t, reply.Data)
				// The response should contain error information
				t.Logf("Error response: %s", string(reply.Data))
			},
		},
		{
			name: "Missing Required ImageId",
			input: &ec2.RunInstancesInput{
				InstanceType: aws.String("t3.micro"),
				MinCount:     aws.Int64(1),
				MaxCount:     aws.Int64(1),
			},
			expectError:    false,
			errorInPayload: true,
			validate: func(t *testing.T, reply *nats.Msg) {
				assert.NotNil(t, reply.Data)
				t.Logf("Error response: %s", string(reply.Data))
			},
		},
		{
			name: "Invalid MinCount (zero)",
			input: &ec2.RunInstancesInput{
				ImageId:      aws.String("ami-0abcdef1234567890"),
				InstanceType: aws.String("t3.micro"),
				MinCount:     aws.Int64(0),
				MaxCount:     aws.Int64(1),
			},
			expectError:    false,
			errorInPayload: true,
			validate: func(t *testing.T, reply *nats.Msg) {
				assert.NotNil(t, reply.Data)
				t.Logf("Error response: %s", string(reply.Data))
			},
		},
		{
			name:           "Malformed JSON",
			input:          []byte(`{"invalid": json}`),
			expectError:    false,
			errorInPayload: true,
			validate: func(t *testing.T, reply *nats.Msg) {
				assert.NotNil(t, reply.Data)
				t.Logf("Error response: %s", string(reply.Data))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip "Valid RunInstancesInput" test as it requires full infrastructure
			// (Predastore S3 backend, Viperblock, NBDkit) to actually create volumes
			if tt.name == "Valid RunInstancesInput" {
				t.Skip("Skipping valid input test - requires full hive infrastructure (viperblock, nbdkit, predastore)")
			}

			// Start test NATS server
			ns, natsURL := startTestNATSServer(t)
			defer ns.Shutdown()

			// Create test daemon
			daemon := createTestDaemon(t, natsURL)

			// Subscribe to the ec2.launch topic with the handler
			sub, err := daemon.natsConn.QueueSubscribe("ec2.launch", "hive-workers", daemon.handleEC2RunInstances)
			require.NoError(t, err, "Failed to subscribe to ec2.launch")
			defer sub.Unsubscribe()

			// Prepare message data
			var msgData []byte
			switch v := tt.input.(type) {
			case []byte:
				msgData = v
			default:
				msgData, err = json.Marshal(tt.input)
				require.NoError(t, err, "Failed to marshal input")
			}

			// Send request to NATS and wait for response
			reply, err := daemon.natsConn.Request("ec2.launch", msgData, 5*time.Second)

			if tt.expectError {
				assert.Error(t, err, "Expected error but got none")
				return
			}

			require.NoError(t, err, "Request failed")
			require.NotNil(t, reply, "No reply received")

			// Validate response
			if tt.validate != nil {
				tt.validate(t, reply)
			}
		})
	}
}

// TestHandleEC2RunInstances_ResourceManagement tests resource allocation and validation
// NOTE: This test validates message handling and resource allocation logic without
// actually launching VMs. Full VM launch requires viperblock, nbdkit, and QEMU/KVM
// which are not available on macOS.
func TestHandleEC2RunInstances_ResourceManagement(t *testing.T) {
	// Skip this test as it requires full infrastructure
	// The test validates NATS message handling, but daemon.handleEC2RunInstances
	// attempts to create viperblock volumes which requires:
	// - S3 backend (predastore) running
	// - viperblock library with S3 backend
	// - nbdkit for NBD mounting
	// - QEMU for VM launch
	t.Skip("Skipping resource management test - requires full hive infrastructure (viperblock, nbdkit, predastore)")

	tests := []struct {
		name             string
		instanceType     string
		expectAllocation bool
		description      string
	}{
		{
			name:             "Valid t3.micro allocation",
			instanceType:     "t3.micro",
			expectAllocation: true,
			description:      "Should successfully allocate resources for t3.micro",
		},
		{
			name:             "Valid t3.nano allocation",
			instanceType:     "t3.nano",
			expectAllocation: true,
			description:      "Should successfully allocate resources for t3.nano",
		},
		{
			name:             "Invalid instance type",
			instanceType:     "t99.invalid",
			expectAllocation: false,
			description:      "Should fail for invalid instance type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns, natsURL := startTestNATSServer(t)
			defer ns.Shutdown()

			daemon := createTestDaemon(t, natsURL)

			sub, err := daemon.natsConn.QueueSubscribe("ec2.launch", "hive-workers", daemon.handleEC2RunInstances)
			require.NoError(t, err)
			defer sub.Unsubscribe()

			input := &ec2.RunInstancesInput{
				ImageId:      aws.String("ami-0abcdef1234567890"),
				InstanceType: aws.String(tt.instanceType),
				MinCount:     aws.Int64(1),
				MaxCount:     aws.Int64(1),
				KeyName:      aws.String("test-key"),
				SubnetId:     aws.String("subnet-test"),
				UserData:     aws.String(""), // Empty UserData to bypass cloud-init requirements
			}

			msgData, err := json.Marshal(input)
			require.NoError(t, err)

			reply, err := daemon.natsConn.Request("ec2.launch", msgData, 5*time.Second)
			require.NoError(t, err)
			require.NotNil(t, reply)

			if tt.expectAllocation {
				var reservation ec2.Reservation
				err := json.Unmarshal(reply.Data, &reservation)
				require.NoError(t, err)
				assert.Len(t, reservation.Instances, 1)
			} else {
				// Should receive error response
				t.Logf("Expected error response: %s", string(reply.Data))
			}
		})
	}
}

// TestDaemon_Initialization tests daemon initialization
func TestDaemon_Initialization(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hive-daemon-init-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// New cluster config
	clusterCfg := &config.ClusterConfig{
		Node:  "node-1",
		Nodes: map[string]config.Config{},
	}

	cfg := &config.Config{
		BaseDir: tmpDir,
		WalDir:  tmpDir,
		NATS: config.NATSConfig{
			Host: "nats://localhost:4222",
		},
		AccessKey: "test-key",
		SecretKey: "test-secret",
	}

	clusterCfg.Nodes["node-1"] = *cfg

	daemon := NewDaemon(clusterCfg)

	assert.NotNil(t, daemon)
	assert.NotNil(t, daemon.resourceMgr)
	assert.NotNil(t, daemon.Instances.VMS)
	assert.Equal(t, cfg, daemon.config)
}

// TestResourceManager tests resource manager functionality
func TestResourceManager(t *testing.T) {
	rm := NewResourceManager()

	require.NotNil(t, rm)
	assert.Greater(t, rm.availableVCPU, 0)
	assert.Greater(t, rm.availableMem, float64(0))

	// Verify CPU model and instance family were detected
	assert.NotEmpty(t, rm.cpuModel)
	assert.NotEmpty(t, rm.instanceFamily)
	t.Logf("Detected CPU: %s, Instance Family: %s", rm.cpuModel, rm.instanceFamily)

	// Test allocation using the first available instance type (dynamic based on CPU)
	require.NotEmpty(t, rm.instanceTypes, "Should have at least one instance type")

	// Get the .micro size for the detected family
	microKey := rm.instanceFamily + ".micro"
	instanceType, exists := rm.instanceTypes[microKey]
	require.True(t, exists, "Should have %s instance type", microKey)

	// Check if can allocate
	canAlloc := rm.canAllocate(instanceType)
	assert.True(t, canAlloc)

	// Allocate
	err := rm.allocate(instanceType)
	assert.NoError(t, err)

	// Check resources were allocated
	assert.Equal(t, instanceType.VCPUs, rm.allocatedVCPU)
	assert.Equal(t, instanceType.MemoryGB, rm.allocatedMem)

	// Deallocate
	rm.deallocate(instanceType)
	assert.Equal(t, 0, rm.allocatedVCPU)
	assert.Equal(t, float64(0), rm.allocatedMem)
}

// TestGetInstanceTypeInfos tests the GetInstanceTypeInfos method
func TestGetInstanceTypeInfos(t *testing.T) {
	rm := NewResourceManager()

	infos := rm.GetInstanceTypeInfos()

	require.NotEmpty(t, infos, "Should return at least one instance type")
	assert.Len(t, infos, 7, "Should have 7 instance sizes (nano, micro, small, medium, large, xlarge, 2xlarge)")

	// Verify structure of returned instance type info
	for _, info := range infos {
		assert.NotNil(t, info.InstanceType, "InstanceType should not be nil")
		assert.NotNil(t, info.VCpuInfo, "VCpuInfo should not be nil")
		assert.NotNil(t, info.VCpuInfo.DefaultVCpus, "DefaultVCpus should not be nil")
		assert.NotNil(t, info.MemoryInfo, "MemoryInfo should not be nil")
		assert.NotNil(t, info.MemoryInfo.SizeInMiB, "SizeInMiB should not be nil")
		assert.NotNil(t, info.ProcessorInfo, "ProcessorInfo should not be nil")
		assert.NotEmpty(t, info.ProcessorInfo.SupportedArchitectures, "SupportedArchitectures should not be empty")
		assert.NotNil(t, info.CurrentGeneration, "CurrentGeneration should not be nil")
		assert.True(t, *info.CurrentGeneration, "CurrentGeneration should be true")

		t.Logf("Instance type: %s, vCPUs: %d, Memory: %d MiB",
			*info.InstanceType, *info.VCpuInfo.DefaultVCpus, *info.MemoryInfo.SizeInMiB)
	}
}

// TestCPUDetection tests CPU model detection
func TestCPUDetection(t *testing.T) {
	cpuModel, err := getCPUModel()

	// CPU detection should succeed on Linux and macOS
	require.NoError(t, err, "CPU detection should succeed")
	assert.NotEmpty(t, cpuModel, "CPU model should not be empty")

	t.Logf("Detected CPU model: %s", cpuModel)
}

// TestInstanceFamilyMapping tests CPU to instance family mapping
func TestInstanceFamilyMapping(t *testing.T) {
	tests := []struct {
		cpuModel       string
		expectedFamily string
	}{
		{"AMD EPYC 7763 64-Core Processor", "m8a"},
		{"Intel(R) Xeon(R) Platinum 8375C CPU @ 2.90GHz", "m7i"},
		{"Apple M1 Pro", "m8g"},
		{"AWS Graviton3", "m8g"},
		{"Unknown CPU", "t3"}, // fallback for x86_64
	}

	for _, tt := range tests {
		t.Run(tt.cpuModel, func(t *testing.T) {
			family := getInstanceFamilyFromCPU(tt.cpuModel)
			assert.Equal(t, tt.expectedFamily, family)
		})
	}
}

// TestGetAvailableInstanceTypeInfos_ResourceFiltering tests that instance types are filtered by available resources
func TestGetAvailableInstanceTypeInfos_ResourceFiltering(t *testing.T) {
	rm := NewResourceManager()

	// Get initial count of all available types
	allTypes := rm.GetInstanceTypeInfos()
	initialAvailable := rm.GetAvailableInstanceTypeInfos()

	t.Logf("System has %d vCPUs, %.2f GB RAM", rm.availableVCPU, rm.availableMem)
	t.Logf("All instance types: %d, Initially available: %d", len(allTypes), len(initialAvailable))

	// Initially available types should only include those that fit system resources
	// (on small machines, xlarge/2xlarge may already be filtered out)
	assert.LessOrEqual(t, len(initialAvailable), len(allTypes),
		"Available types should be <= total types")
	assert.Greater(t, len(initialAvailable), 0, "Should have at least one available type")

	// Verify all initially available types fit within system resources
	for _, info := range initialAvailable {
		vcpus := int(*info.VCpuInfo.DefaultVCpus)
		memGB := float64(*info.MemoryInfo.SizeInMiB) / 1024

		assert.LessOrEqual(t, vcpus, rm.availableVCPU,
			"Instance type %s vCPUs should fit system", *info.InstanceType)
		assert.LessOrEqual(t, memGB, rm.availableMem,
			"Instance type %s memory should fit system", *info.InstanceType)
	}

	// Allocate the smallest instance type (nano) to consume some resources
	nanoKey := rm.instanceFamily + ".nano" // 2 vCPUs, 0.5GB
	nanoType, exists := rm.instanceTypes[nanoKey]
	require.True(t, exists, "Should have %s instance type", nanoKey)

	err := rm.allocate(nanoType)
	require.NoError(t, err, "Should be able to allocate %s", nanoKey)

	t.Logf("After allocating %s: allocated %d vCPUs, %.2f GB RAM",
		nanoKey, rm.allocatedVCPU, rm.allocatedMem)

	// Now get available types - should be fewer or equal (depending on system resources)
	afterAllocation := rm.GetAvailableInstanceTypeInfos()
	t.Logf("Available after allocation: %d", len(afterAllocation))

	// Verify all returned types fit within REMAINING resources
	remainingVCPU := rm.availableVCPU - rm.allocatedVCPU
	remainingMem := rm.availableMem - rm.allocatedMem

	for _, info := range afterAllocation {
		typeName := *info.InstanceType
		vcpus := int(*info.VCpuInfo.DefaultVCpus)
		memGB := float64(*info.MemoryInfo.SizeInMiB) / 1024

		assert.LessOrEqual(t, vcpus, remainingVCPU,
			"Instance type %s should not exceed remaining vCPUs", typeName)
		assert.LessOrEqual(t, memGB, remainingMem,
			"Instance type %s should not exceed remaining memory", typeName)

		t.Logf("Available: %s (vCPUs: %d, Memory: %.2f GB)", typeName, vcpus, memGB)
	}

	// Deallocate and verify we get the same available types as before
	rm.deallocate(nanoType)
	afterDeallocation := rm.GetAvailableInstanceTypeInfos()
	assert.Equal(t, len(initialAvailable), len(afterDeallocation),
		"Should have same available types after deallocation")
}
