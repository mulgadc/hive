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

// createValidRunInstancesInput creates a valid RunInstancesInput for testing
func createValidRunInstancesInput() *ec2.RunInstancesInput {
	return &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-0abcdef1234567890"),
		InstanceType: aws.String("t3.micro"),
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

	// Test allocation
	instanceType := rm.instanceTypes["t3.micro"]
	require.NotNil(t, instanceType)

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
