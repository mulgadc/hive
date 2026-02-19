package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	awss3 "github.com/aws/aws-sdk-go/service/s3"

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
	"github.com/mulgadc/hive/hive/vm"
	"github.com/mulgadc/viperblock/viperblock"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createFullTestDaemonWithStore creates a test daemon with ALL services initialized
// and returns the shared memory store for seeding test data.
func createFullTestDaemonWithStore(t *testing.T, natsURL string) (*Daemon, *objectstore.MemoryObjectStore) {
	daemon := createTestDaemon(t, natsURL)

	memStore := objectstore.NewMemoryObjectStore()
	cfg := daemon.config

	daemon.keyService = handlers_ec2_key.NewKeyServiceImplWithStore(memStore, cfg.Predastore.Bucket, "123456789")
	daemon.imageService = handlers_ec2_image.NewImageServiceImplWithStore(memStore, cfg.Predastore.Bucket, "123456789")
	daemon.volumeService = handlers_ec2_volume.NewVolumeServiceImplWithStore(cfg, memStore, daemon.natsConn)
	daemon.snapshotService = handlers_ec2_snapshot.NewSnapshotServiceImplWithStore(cfg, memStore, daemon.natsConn)
	daemon.tagsService = handlers_ec2_tags.NewTagsServiceImplWithStore(cfg, memStore)
	daemon.eigwService = handlers_ec2_eigw.NewEgressOnlyIGWServiceImpl(cfg)
	initAccountServiceForTest(t, daemon)

	return daemon, memStore
}

// createFullTestDaemon creates a test daemon with ALL services initialized (including
// key, image, snapshot, tags, eigw, account) using in-memory object stores.
func createFullTestDaemon(t *testing.T, natsURL string) *Daemon {
	daemon, _ := createFullTestDaemonWithStore(t, natsURL)
	return daemon
}

// createFullTestDaemonWithJetStream creates a test daemon with JetStream KV enabled,
// needed for tests involving state transitions (TransitionState calls WriteState).
func createFullTestDaemonWithJetStream(t *testing.T, natsURL string) *Daemon {
	daemon := createFullTestDaemon(t, natsURL)

	var err error
	daemon.jsManager, err = NewJetStreamManager(daemon.natsConn, 1)
	require.NoError(t, err)
	err = daemon.jsManager.InitKVBucket()
	require.NoError(t, err)

	return daemon
}

// initAccountServiceForTest initializes a JetStream-backed account service on the daemon
// using an isolated embedded NATS JetStream server per test to avoid shared KV state.
func initAccountServiceForTest(t *testing.T, daemon *Daemon) {
	t.Helper()
	ns, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	})
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	svc, err := handlers_ec2_account.NewAccountSettingsServiceImplWithNATS(nil, nc)
	require.NoError(t, err)
	daemon.accountService = svc
}

// --- handleNATSRequest generic tests ---

type testInput struct {
	Name string `json:"name"`
}

type testOutput struct {
	Greeting string `json:"greeting"`
}

func TestHandleNATSRequest_ValidRequest(t *testing.T) {
	natsURL := sharedNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	serviceFn := func(in *testInput) (*testOutput, error) {
		return &testOutput{Greeting: "hello " + in.Name}, nil
	}

	sub, err := nc.Subscribe("test.greet", func(msg *nats.Msg) {
		handleNATSRequest(msg, serviceFn)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reqData, _ := json.Marshal(testInput{Name: "world"})
	reply, err := nc.Request("test.greet", reqData, 5*time.Second)
	require.NoError(t, err)

	var out testOutput
	err = json.Unmarshal(reply.Data, &out)
	require.NoError(t, err)
	assert.Equal(t, "hello world", out.Greeting)
}

func TestHandleNATSRequest_MalformedJSON(t *testing.T) {
	natsURL := sharedNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	serviceFn := func(in *testInput) (*testOutput, error) {
		return &testOutput{Greeting: "hello"}, nil
	}

	sub, err := nc.Subscribe("test.malformed", func(msg *nats.Msg) {
		handleNATSRequest(msg, serviceFn)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reply, err := nc.Request("test.malformed", []byte(`{not valid json}`), 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code", "Should contain error code")
}

func TestHandleNATSRequest_ServiceError(t *testing.T) {
	natsURL := sharedNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	serviceFn := func(in *testInput) (*testOutput, error) {
		return nil, fmt.Errorf("something went wrong")
	}

	sub, err := nc.Subscribe("test.err", func(msg *nats.Msg) {
		handleNATSRequest(msg, serviceFn)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reqData, _ := json.Marshal(testInput{Name: "world"})
	reply, err := nc.Request("test.err", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code", "Should contain error code")
}

// --- Handler wrapper tests (representative set via NATS round-trip) ---

func TestHandleEC2CreateKeyPair_RoundTrip(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.CreateKeyPair", "hive-workers", daemon.handleEC2CreateKeyPair)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.CreateKeyPairInput{
		KeyName: aws.String("test-key-001"),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.CreateKeyPair", reqData, 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, reply)

	var output ec2.CreateKeyPairOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
	assert.Equal(t, "test-key-001", *output.KeyName)
	assert.NotEmpty(t, *output.KeyFingerprint)
	assert.NotEmpty(t, *output.KeyMaterial)
}

func TestHandleEC2CreateTags_RoundTrip(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.CreateTags", "hive-workers", daemon.handleEC2CreateTags)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-12345678")},
		Tags: []*ec2.Tag{
			{Key: aws.String("Name"), Value: aws.String("test-instance")},
		},
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.CreateTags", reqData, 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, reply)

	var output ec2.CreateTagsOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
}

func TestHandleEC2DescribeImages_RoundTrip(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.DescribeImages", "hive-workers", daemon.handleEC2DescribeImages)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.DescribeImagesInput{}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.DescribeImages", reqData, 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, reply)

	var output ec2.DescribeImagesOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
}

func TestHandleEC2DescribeVolumes_RoundTrip(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.DescribeVolumes", "hive-workers", daemon.handleEC2DescribeVolumes)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.DescribeVolumesInput{}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.DescribeVolumes", reqData, 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, reply)

	var output ec2.DescribeVolumesOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
}

func TestHandleEC2DescribeKeyPairs_RoundTrip(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.DescribeKeyPairs", "hive-workers", daemon.handleEC2DescribeKeyPairs)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.DescribeKeyPairsInput{}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.DescribeKeyPairs", reqData, 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, reply)

	var output ec2.DescribeKeyPairsOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
}

// --- handleHealthCheck tests ---

func TestHandleHealthCheck(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createTestDaemon(t, natsURL)

	topic := fmt.Sprintf("hive.admin.%s.health", daemon.node)
	sub, err := daemon.natsConn.Subscribe(topic, daemon.handleHealthCheck)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reply, err := daemon.natsConn.Request(topic, nil, 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, reply)

	var resp config.NodeHealthResponse
	err = json.Unmarshal(reply.Data, &resp)
	require.NoError(t, err)

	assert.Equal(t, daemon.node, resp.Node)
	assert.Equal(t, "running", resp.Status)
	assert.NotEmpty(t, resp.ConfigHash)
	assert.GreaterOrEqual(t, resp.Uptime, int64(0))
}

// --- handleNodeDiscover tests ---

func TestHandleNodeDiscover(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.Subscribe("hive.nodes.discover", daemon.handleNodeDiscover)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reply, err := daemon.natsConn.Request("hive.nodes.discover", nil, 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, reply)

	var resp NodeDiscoverResponse
	err = json.Unmarshal(reply.Data, &resp)
	require.NoError(t, err)

	assert.Equal(t, daemon.node, resp.Node)
}

// --- handleEC2RunInstances AMI validation tests ---

func TestHandleEC2RunInstances_InvalidAMI(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.RunInstances", "hive-workers", daemon.handleEC2RunInstances)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-nonexistent"),
		InstanceType: aws.String(getTestInstanceType()),
		MinCount:     aws.Int64(1),
		MaxCount:     aws.Int64(1),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.RunInstances", reqData, 5*time.Second)
	require.NoError(t, err)

	// Should return InvalidAMIID.NotFound, not ServerInternal
	assert.Contains(t, string(reply.Data), "InvalidAMIID.NotFound")
}

func TestHandleEC2RunInstances_InvalidKeyPair(t *testing.T) {
	natsURL := sharedNATSURL

	daemon, memStore := createFullTestDaemonWithStore(t, natsURL)

	// Seed a valid AMI so AMI validation passes
	seedTestAMI(t, memStore, daemon.config.Predastore.Bucket, "ami-test123")

	sub, err := daemon.natsConn.QueueSubscribe("ec2.RunInstances", "hive-workers", daemon.handleEC2RunInstances)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-test123"),
		InstanceType: aws.String(getTestInstanceType()),
		KeyName:      aws.String("nonexistent-keypair"),
		MinCount:     aws.Int64(1),
		MaxCount:     aws.Int64(1),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.RunInstances", reqData, 5*time.Second)
	require.NoError(t, err)

	// Should return InvalidKeyPair.NotFound, not proceed to launch
	assert.Contains(t, string(reply.Data), "InvalidKeyPair.NotFound")
}

func TestHandleEC2RunInstances_ValidKeyPairPassesValidation(t *testing.T) {
	natsURL := sharedNATSURL

	daemon, memStore := createFullTestDaemonWithStore(t, natsURL)

	// Seed a valid AMI
	seedTestAMI(t, memStore, daemon.config.Predastore.Bucket, "ami-test456")

	// Seed a valid key pair (public key + metadata)
	bucket := daemon.config.Predastore.Bucket
	_, err := memStore.PutObject(&awss3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("keys/123456789/my-key"),
		Body:   strings.NewReader("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest"),
	})
	require.NoError(t, err)

	metadataJSON := `{"KeyPairId":"key-abc123","KeyName":"my-key","KeyFingerprint":"SHA256:test"}`
	_, err = memStore.PutObject(&awss3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("keys/123456789/key-abc123.json"),
		Body:   strings.NewReader(metadataJSON),
	})
	require.NoError(t, err)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.RunInstances", "hive-workers", daemon.handleEC2RunInstances)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-test456"),
		InstanceType: aws.String(getTestInstanceType()),
		KeyName:      aws.String("my-key"),
		MinCount:     aws.Int64(1),
		MaxCount:     aws.Int64(1),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.RunInstances", reqData, 5*time.Second)
	require.NoError(t, err)

	// Should NOT contain InvalidKeyPair.NotFound — key pair validation should pass
	assert.NotContains(t, string(reply.Data), "InvalidKeyPair.NotFound")
}

func TestHandleEC2RunInstances_EmptyKeyNameSkipsValidation(t *testing.T) {
	natsURL := sharedNATSURL

	daemon, memStore := createFullTestDaemonWithStore(t, natsURL)
	seedTestAMI(t, memStore, daemon.config.Predastore.Bucket, "ami-test789")

	sub, err := daemon.natsConn.QueueSubscribe("ec2.RunInstances", "hive-workers", daemon.handleEC2RunInstances)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// No KeyName at all — should skip validation
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-test789"),
		InstanceType: aws.String(getTestInstanceType()),
		MinCount:     aws.Int64(1),
		MaxCount:     aws.Int64(1),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.RunInstances", reqData, 5*time.Second)
	require.NoError(t, err)

	// Should NOT contain InvalidKeyPair.NotFound
	assert.NotContains(t, string(reply.Data), "InvalidKeyPair.NotFound")
}

// --- handleEC2RunInstances service-layer error propagation ---

func TestHandleEC2RunInstances_ServiceErrorPropagated(t *testing.T) {
	natsURL := sharedNATSURL

	daemon, memStore := createFullTestDaemonWithStore(t, natsURL)
	seedTestAMI(t, memStore, daemon.config.Predastore.Bucket, "ami-propatest")

	// Override instanceService with one that has an empty instance types map.
	// The resourceMgr still has instance types, so the daemon-level check passes,
	// but RunInstance() will fail with ErrorInvalidInstanceType.
	emptyTypes := map[string]*ec2.InstanceTypeInfo{}
	daemon.instanceService = handlers_ec2_instance.NewInstanceServiceImpl(
		daemon.config, emptyTypes, daemon.natsConn, &daemon.Instances,
		objectstore.NewMemoryObjectStore(),
	)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.RunInstances", "hive-workers", daemon.handleEC2RunInstances)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-propatest"),
		InstanceType: aws.String(getTestInstanceType()),
		MinCount:     aws.Int64(1),
		MaxCount:     aws.Int64(1),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.RunInstances", reqData, 5*time.Second)
	require.NoError(t, err)

	// Should propagate the specific AWS error from the service layer,
	// not swallow it into ServerInternal
	assert.Contains(t, string(reply.Data), "InvalidInstanceType")
	assert.NotContains(t, string(reply.Data), "ServerInternal")
}

// --- handleStopOrTerminateInstance tests (JetStream required for TransitionState) ---

func TestHandleEC2Events_StopInstance(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	instanceID := "i-test-stop-001"
	daemon.Instances.VMS[instanceID] = &vm.VM{
		ID:           instanceID,
		InstanceType: getTestInstanceType(),
		Status:       vm.StateRunning,
		Instance:     &ec2.Instance{},
		QMPClient:    &qmp.QMPClient{},
	}

	sub, err := daemon.natsConn.Subscribe(
		fmt.Sprintf("ec2.cmd.%s", instanceID),
		daemon.handleEC2Events,
	)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	cmd := qmp.Command{
		ID:         instanceID,
		Attributes: qmp.Attributes{StopInstance: true},
	}
	cmdData, _ := json.Marshal(cmd)

	reply, err := daemon.natsConn.Request(
		fmt.Sprintf("ec2.cmd.%s", instanceID),
		cmdData,
		5*time.Second,
	)
	require.NoError(t, err)
	require.NotNil(t, reply)

	// Should get immediate {} response
	assert.Equal(t, `{}`, string(reply.Data))

	// State should transition to stopping
	daemon.Instances.Mu.Lock()
	status := daemon.Instances.VMS[instanceID].Status
	daemon.Instances.Mu.Unlock()
	assert.Equal(t, vm.StateStopping, status)
}

func TestHandleEC2Events_TerminateInstance(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	instanceID := "i-test-term-001"
	daemon.Instances.VMS[instanceID] = &vm.VM{
		ID:           instanceID,
		InstanceType: getTestInstanceType(),
		Status:       vm.StateRunning,
		Instance:     &ec2.Instance{},
		QMPClient:    &qmp.QMPClient{},
	}

	sub, err := daemon.natsConn.Subscribe(
		fmt.Sprintf("ec2.cmd.%s", instanceID),
		daemon.handleEC2Events,
	)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	cmd := qmp.Command{
		ID:         instanceID,
		Attributes: qmp.Attributes{TerminateInstance: true},
	}
	cmdData, _ := json.Marshal(cmd)

	reply, err := daemon.natsConn.Request(
		fmt.Sprintf("ec2.cmd.%s", instanceID),
		cmdData,
		5*time.Second,
	)
	require.NoError(t, err)
	require.NotNil(t, reply)

	assert.Equal(t, `{}`, string(reply.Data))

	daemon.Instances.Mu.Lock()
	status := daemon.Instances.VMS[instanceID].Status
	daemon.Instances.Mu.Unlock()
	assert.Equal(t, vm.StateShuttingDown, status)
}

func TestHandleEC2Events_InstanceNotFound(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.Subscribe("ec2.cmd.i-nonexistent", daemon.handleEC2Events)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	cmd := qmp.Command{
		ID:         "i-nonexistent",
		Attributes: qmp.Attributes{StopInstance: true},
	}
	cmdData, _ := json.Marshal(cmd)

	reply, err := daemon.natsConn.Request("ec2.cmd.i-nonexistent", cmdData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

func TestHandleEC2Events_MalformedJSON(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.Subscribe("ec2.cmd.test", daemon.handleEC2Events)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reply, err := daemon.natsConn.Request("ec2.cmd.test", []byte(`{bad json}`), 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

// --- respondWithVolumeAttachment tests ---

func TestRespondWithVolumeAttachment(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.Subscribe("test.volume.attach", func(msg *nats.Msg) {
		respondWithError := func(errCode string) {
			msg.Respond([]byte(fmt.Sprintf(`{"Code":"%s"}`, errCode)))
		}
		daemon.respondWithVolumeAttachment(msg, respondWithError, "vol-123", "i-456", "/dev/sdf", "attached")
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reply, err := daemon.natsConn.Request("test.volume.attach", nil, 5*time.Second)
	require.NoError(t, err)

	var attachment ec2.VolumeAttachment
	err = json.Unmarshal(reply.Data, &attachment)
	require.NoError(t, err)

	assert.Equal(t, "vol-123", *attachment.VolumeId)
	assert.Equal(t, "i-456", *attachment.InstanceId)
	assert.Equal(t, "/dev/sdf", *attachment.Device)
	assert.Equal(t, "attached", *attachment.State)
	assert.NotNil(t, attachment.AttachTime)
	assert.Equal(t, false, *attachment.DeleteOnTermination)
}

// --- handleEC2ModifyVolume tests ---

func TestHandleEC2ModifyVolume_MalformedInput(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.ModifyVolume", "hive-workers", daemon.handleEC2ModifyVolume)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reply, err := daemon.natsConn.Request("ec2.ModifyVolume", []byte(`{bad}`), 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

func TestHandleEC2ModifyVolume_VolumeNotFound(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.ModifyVolume", "hive-workers", daemon.handleEC2ModifyVolume)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.ModifyVolumeInput{
		VolumeId: aws.String("vol-nonexistent"),
		Size:     aws.Int64(16),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.ModifyVolume", reqData, 5*time.Second)
	require.NoError(t, err)

	// Should return an error since the volume doesn't exist
	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

// --- Account settings handler tests ---

func TestHandleEC2GetEbsEncryptionByDefault(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.GetEbsEncryptionByDefault", "hive-workers", daemon.handleEC2GetEbsEncryptionByDefault)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.GetEbsEncryptionByDefaultInput{}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.GetEbsEncryptionByDefault", reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.GetEbsEncryptionByDefaultOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
	assert.NotNil(t, output.EbsEncryptionByDefault)
}

func TestHandleEC2GetSerialConsoleAccessStatus(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.GetSerialConsoleAccessStatus", "hive-workers", daemon.handleEC2GetSerialConsoleAccessStatus)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.GetSerialConsoleAccessStatusInput{}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.GetSerialConsoleAccessStatus", reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.GetSerialConsoleAccessStatusOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
	assert.NotNil(t, output.SerialConsoleAccessEnabled)
}

func TestHandleEC2EnableEbsEncryptionByDefault(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.EnableEbsEncryptionByDefault", "hive-workers", daemon.handleEC2EnableEbsEncryptionByDefault)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.EnableEbsEncryptionByDefaultInput{}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.EnableEbsEncryptionByDefault", reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.EnableEbsEncryptionByDefaultOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
	assert.NotNil(t, output.EbsEncryptionByDefault)
	assert.True(t, *output.EbsEncryptionByDefault)
}

func TestHandleEC2DisableEbsEncryptionByDefault(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.DisableEbsEncryptionByDefault", "hive-workers", daemon.handleEC2DisableEbsEncryptionByDefault)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.DisableEbsEncryptionByDefaultInput{}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.DisableEbsEncryptionByDefault", reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.DisableEbsEncryptionByDefaultOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
	assert.NotNil(t, output.EbsEncryptionByDefault)
	assert.False(t, *output.EbsEncryptionByDefault)
}

func TestHandleEC2EnableSerialConsoleAccess(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.EnableSerialConsoleAccess", "hive-workers", daemon.handleEC2EnableSerialConsoleAccess)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.EnableSerialConsoleAccessInput{}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.EnableSerialConsoleAccess", reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.EnableSerialConsoleAccessOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
	assert.NotNil(t, output.SerialConsoleAccessEnabled)
	assert.True(t, *output.SerialConsoleAccessEnabled)
}

func TestHandleEC2DisableSerialConsoleAccess(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.DisableSerialConsoleAccess", "hive-workers", daemon.handleEC2DisableSerialConsoleAccess)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.DisableSerialConsoleAccessInput{}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.DisableSerialConsoleAccess", reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.DisableSerialConsoleAccessOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
	assert.NotNil(t, output.SerialConsoleAccessEnabled)
	assert.False(t, *output.SerialConsoleAccessEnabled)
}

// --- handleEC2CreateImage tests ---

func TestHandleEC2CreateImage_InstanceNotFound(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.Subscribe("ec2.CreateImage", daemon.handleEC2CreateImage)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.CreateImageInput{
		InstanceId: aws.String("i-nonexistent"),
		Name:       aws.String("my-image"),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.CreateImage", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

func TestHandleEC2CreateImage_MissingInstanceId(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.Subscribe("ec2.CreateImage", daemon.handleEC2CreateImage)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.CreateImageInput{
		Name: aws.String("my-image"),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.CreateImage", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

func TestHandleEC2CreateImage_InvalidState(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	// Add an instance in "pending" state (not running or stopped)
	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS["i-pending123"] = &vm.VM{
		ID:     "i-pending123",
		Status: vm.StatePending,
		Instance: &ec2.Instance{
			InstanceId: aws.String("i-pending123"),
			ImageId:    aws.String("ami-source"),
			BlockDeviceMappings: []*ec2.InstanceBlockDeviceMapping{
				{
					DeviceName: aws.String("/dev/sda1"),
					Ebs:        &ec2.EbsInstanceBlockDevice{VolumeId: aws.String("vol-root123")},
				},
			},
		},
	}
	daemon.Instances.Mu.Unlock()

	sub, err := daemon.natsConn.Subscribe("ec2.CreateImage", daemon.handleEC2CreateImage)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.CreateImageInput{
		InstanceId: aws.String("i-pending123"),
		Name:       aws.String("my-image"),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.CreateImage", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

func TestHandleEC2CreateImage_NoRootVolume(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	// Add instance with no block device mappings
	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS["i-novol123"] = &vm.VM{
		ID:     "i-novol123",
		Status: vm.StateRunning,
		Instance: &ec2.Instance{
			InstanceId:          aws.String("i-novol123"),
			ImageId:             aws.String("ami-source"),
			BlockDeviceMappings: []*ec2.InstanceBlockDeviceMapping{},
		},
	}
	daemon.Instances.Mu.Unlock()

	sub, err := daemon.natsConn.Subscribe("ec2.CreateImage", daemon.handleEC2CreateImage)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.CreateImageInput{
		InstanceId: aws.String("i-novol123"),
		Name:       aws.String("my-image"),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.CreateImage", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

func TestHandleEC2CreateImage_MalformedJSON(t *testing.T) {
	natsURL := sharedNATSURL

	daemon := createFullTestDaemon(t, natsURL)

	sub, err := daemon.natsConn.Subscribe("ec2.CreateImage", daemon.handleEC2CreateImage)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reply, err := daemon.natsConn.Request("ec2.CreateImage", []byte(`{bad json}`), 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

// --- SetConfigPath test ---

func TestSetConfigPath(t *testing.T) {
	clusterCfg := &config.ClusterConfig{
		Node:  "node-1",
		Nodes: map[string]config.Config{"node-1": {BaseDir: "/tmp"}},
	}
	daemon := NewDaemon(clusterCfg)

	assert.Empty(t, daemon.configPath)
	daemon.SetConfigPath("/etc/hive/config.toml")
	assert.Equal(t, "/etc/hive/config.toml", daemon.configPath)
}

// --- handleEC2StartStoppedInstance tests ---

func TestHandleEC2StartStoppedInstance_MissingInstance(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.start", "hive-workers", daemon.handleEC2StartStoppedInstance)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Request to start a non-existent instance
	reqData, _ := json.Marshal(map[string]string{"instance_id": "i-nonexistent"})
	reply, err := daemon.natsConn.Request("ec2.start", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

func TestHandleEC2StartStoppedInstance_MissingInstanceID(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.start", "hive-workers", daemon.handleEC2StartStoppedInstance)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Request with empty instance_id
	reqData, _ := json.Marshal(map[string]string{"instance_id": ""})
	reply, err := daemon.natsConn.Request("ec2.start", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

func TestHandleEC2StartStoppedInstance_NotStoppedState(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	// Write an instance in running state to shared KV
	runningVM := &vm.VM{
		ID:           "i-running-shared",
		Status:       vm.StateRunning,
		InstanceType: getTestInstanceType(),
	}
	err := daemon.jsManager.WriteStoppedInstance(runningVM.ID, runningVM)
	require.NoError(t, err)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.start", "hive-workers", daemon.handleEC2StartStoppedInstance)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reqData, _ := json.Marshal(map[string]string{"instance_id": "i-running-shared"})
	reply, err := daemon.natsConn.Request("ec2.start", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")

	// Cleanup
	_ = daemon.jsManager.DeleteStoppedInstance(runningVM.ID)
}

// --- handleEC2DescribeStoppedInstances tests ---

func TestHandleEC2DescribeStoppedInstances_ReturnsStoppedInstances(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	// Write stopped instances to shared KV with full reservation/instance metadata
	stoppedVM := &vm.VM{
		ID:           "i-describe-stopped-001",
		Status:       vm.StateStopped,
		InstanceType: getTestInstanceType(),
		LastNode:     "node-1",
		Reservation: &ec2.Reservation{
			ReservationId: aws.String("r-test-001"),
			OwnerId:       aws.String("123456789012"),
		},
		Instance: &ec2.Instance{
			InstanceId:   aws.String("i-describe-stopped-001"),
			InstanceType: aws.String(getTestInstanceType()),
		},
	}
	err := daemon.jsManager.WriteStoppedInstance(stoppedVM.ID, stoppedVM)
	require.NoError(t, err)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.DescribeStoppedInstances", "hive-workers", daemon.handleEC2DescribeStoppedInstances)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.DescribeInstancesInput{}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.DescribeStoppedInstances", reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.DescribeInstancesOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)

	// Find our stopped instance in the output
	found := false
	for _, res := range output.Reservations {
		for _, inst := range res.Instances {
			if inst.InstanceId != nil && *inst.InstanceId == "i-describe-stopped-001" {
				found = true
				assert.Equal(t, "stopped", *inst.State.Name)
				assert.Equal(t, int64(80), *inst.State.Code)
			}
		}
	}
	assert.True(t, found, "Should find stopped instance in DescribeStoppedInstances output")

	// Cleanup
	_ = daemon.jsManager.DeleteStoppedInstance(stoppedVM.ID)
}

func TestHandleEC2DescribeStoppedInstances_WithFilter(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	// Write two stopped instances
	for _, id := range []string{"i-filter-001", "i-filter-002"} {
		v := &vm.VM{
			ID:       id,
			Status:   vm.StateStopped,
			LastNode: "node-1",
			Reservation: &ec2.Reservation{
				ReservationId: aws.String("r-filter"),
				OwnerId:       aws.String("123456789012"),
			},
			Instance: &ec2.Instance{
				InstanceId: aws.String(id),
			},
		}
		err := daemon.jsManager.WriteStoppedInstance(id, v)
		require.NoError(t, err)
	}

	sub, err := daemon.natsConn.QueueSubscribe("ec2.DescribeStoppedInstances", "hive-workers", daemon.handleEC2DescribeStoppedInstances)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Filter for only one instance
	input := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{aws.String("i-filter-001")},
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request("ec2.DescribeStoppedInstances", reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.DescribeInstancesOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)

	// Count matching instances
	var matchCount int
	for _, res := range output.Reservations {
		for _, inst := range res.Instances {
			if inst.InstanceId != nil && *inst.InstanceId == "i-filter-001" {
				matchCount++
			}
			// Should NOT contain i-filter-002
			if inst.InstanceId != nil && *inst.InstanceId == "i-filter-002" {
				t.Error("Should not contain i-filter-002 when filtering for i-filter-001")
			}
		}
	}
	assert.Equal(t, 1, matchCount, "Should find exactly one filtered instance")

	// Cleanup
	_ = daemon.jsManager.DeleteStoppedInstance("i-filter-001")
	_ = daemon.jsManager.DeleteStoppedInstance("i-filter-002")
}

// --- handleEC2TerminateStoppedInstance tests ---

func TestHandleEC2TerminateStoppedInstance_MissingInstanceID(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.terminate", "hive-workers", daemon.handleEC2TerminateStoppedInstance)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reqData, _ := json.Marshal(map[string]string{"instance_id": ""})
	reply, err := daemon.natsConn.Request("ec2.terminate", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

func TestHandleEC2TerminateStoppedInstance_MissingInstance(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.terminate", "hive-workers", daemon.handleEC2TerminateStoppedInstance)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reqData, _ := json.Marshal(map[string]string{"instance_id": "i-nonexistent"})
	reply, err := daemon.natsConn.Request("ec2.terminate", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")
}

func TestHandleEC2TerminateStoppedInstance_NotStoppedState(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	// Write an instance in running state to shared KV
	runningVM := &vm.VM{
		ID:           "i-term-running",
		Status:       vm.StateRunning,
		InstanceType: getTestInstanceType(),
	}
	err := daemon.jsManager.WriteStoppedInstance(runningVM.ID, runningVM)
	require.NoError(t, err)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.terminate", "hive-workers", daemon.handleEC2TerminateStoppedInstance)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reqData, _ := json.Marshal(map[string]string{"instance_id": "i-term-running"})
	reply, err := daemon.natsConn.Request("ec2.terminate", reqData, 5*time.Second)
	require.NoError(t, err)

	var errResp map[string]any
	err = json.Unmarshal(reply.Data, &errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp, "Code")

	// Cleanup
	_ = daemon.jsManager.DeleteStoppedInstance(runningVM.ID)
}

func TestHandleEC2TerminateStoppedInstance_Success(t *testing.T) {
	natsURL := sharedJSNATSURL

	daemon := createFullTestDaemonWithJetStream(t, natsURL)

	// Write a stopped instance to shared KV
	stoppedVM := &vm.VM{
		ID:           "i-term-stopped-001",
		Status:       vm.StateStopped,
		InstanceType: getTestInstanceType(),
		LastNode:     "node-1",
		Reservation: &ec2.Reservation{
			ReservationId: aws.String("r-term-001"),
			OwnerId:       aws.String("123456789012"),
		},
		Instance: &ec2.Instance{
			InstanceId:   aws.String("i-term-stopped-001"),
			InstanceType: aws.String(getTestInstanceType()),
		},
	}
	err := daemon.jsManager.WriteStoppedInstance(stoppedVM.ID, stoppedVM)
	require.NoError(t, err)

	sub, err := daemon.natsConn.QueueSubscribe("ec2.terminate", "hive-workers", daemon.handleEC2TerminateStoppedInstance)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	reqData, _ := json.Marshal(map[string]string{"instance_id": "i-term-stopped-001"})
	reply, err := daemon.natsConn.Request("ec2.terminate", reqData, 5*time.Second)
	require.NoError(t, err)

	var resp map[string]string
	err = json.Unmarshal(reply.Data, &resp)
	require.NoError(t, err)
	assert.Equal(t, "terminated", resp["status"])
	assert.Equal(t, "i-term-stopped-001", resp["instanceId"])

	// Verify instance was removed from shared KV
	loaded, err := daemon.jsManager.LoadStoppedInstance("i-term-stopped-001")
	require.NoError(t, err)
	assert.Nil(t, loaded, "Instance should be removed from shared KV after termination")
}

func TestHandleEC2GetConsoleOutput(t *testing.T) {
	natsURL := sharedNATSURL
	daemon := createFullTestDaemon(t, natsURL)

	// Enable serial console access
	_, err := daemon.accountService.EnableSerialConsoleAccess(&ec2.EnableSerialConsoleAccessInput{})
	require.NoError(t, err)

	instanceID := "i-console-test-001"

	// Create a temp console log file
	tmpDir := t.TempDir()
	logPath := tmpDir + "/console-" + instanceID + ".log"
	require.NoError(t, os.WriteFile(logPath, []byte("Hello from serial console\nBoot complete."), 0644))

	// Add an instance with console log path
	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS[instanceID] = &vm.VM{
		ID:     instanceID,
		Status: vm.StateRunning,
		Config: vm.Config{
			ConsoleLogPath: logPath,
		},
	}
	daemon.Instances.Mu.Unlock()

	topic := fmt.Sprintf("ec2.%s.GetConsoleOutput", instanceID)
	sub, err := daemon.natsConn.Subscribe(topic, daemon.handleEC2GetConsoleOutput)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.GetConsoleOutputInput{
		InstanceId: aws.String(instanceID),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request(topic, reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.GetConsoleOutputOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
	assert.Equal(t, instanceID, *output.InstanceId)
	assert.NotNil(t, output.Output)
	assert.NotEmpty(t, *output.Output)
	assert.NotNil(t, output.Timestamp)

	// Decode base64 output and verify content
	decoded, err := base64.StdEncoding.DecodeString(*output.Output)
	require.NoError(t, err)
	assert.Contains(t, string(decoded), "Boot complete.")
}

func TestHandleEC2GetConsoleOutput_EmptyLog(t *testing.T) {
	natsURL := sharedNATSURL
	daemon := createFullTestDaemon(t, natsURL)

	// Enable serial console access
	_, err := daemon.accountService.EnableSerialConsoleAccess(&ec2.EnableSerialConsoleAccessInput{})
	require.NoError(t, err)

	instanceID := "i-console-empty-001"

	// Instance exists but no log file yet
	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS[instanceID] = &vm.VM{
		ID:     instanceID,
		Status: vm.StateRunning,
		Config: vm.Config{
			ConsoleLogPath: "/nonexistent/console.log",
		},
	}
	daemon.Instances.Mu.Unlock()

	topic := fmt.Sprintf("ec2.%s.GetConsoleOutput", instanceID)
	sub, err := daemon.natsConn.Subscribe(topic, daemon.handleEC2GetConsoleOutput)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.GetConsoleOutputInput{
		InstanceId: aws.String(instanceID),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request(topic, reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.GetConsoleOutputOutput
	err = json.Unmarshal(reply.Data, &output)
	require.NoError(t, err)
	assert.Equal(t, instanceID, *output.InstanceId)
	assert.NotNil(t, output.Output)
	assert.Empty(t, *output.Output)
}

func TestHandleEC2GetConsoleOutput_NotFound(t *testing.T) {
	natsURL := sharedNATSURL
	daemon := createFullTestDaemon(t, natsURL)

	// Enable serial console access
	_, err := daemon.accountService.EnableSerialConsoleAccess(&ec2.EnableSerialConsoleAccessInput{})
	require.NoError(t, err)

	instanceID := "i-nonexistent-console"
	topic := fmt.Sprintf("ec2.%s.GetConsoleOutput", instanceID)
	sub, err := daemon.natsConn.Subscribe(topic, daemon.handleEC2GetConsoleOutput)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.GetConsoleOutputInput{
		InstanceId: aws.String(instanceID),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request(topic, reqData, 5*time.Second)
	require.NoError(t, err)

	// Should get an error response (instance not found)
	assert.Contains(t, string(reply.Data), "InvalidInstanceID.NotFound")
}

func TestHandleEC2GetConsoleOutput_SerialConsoleDisabled(t *testing.T) {
	natsURL := sharedNATSURL
	daemon := createFullTestDaemon(t, natsURL)

	// Serial console access defaults to disabled — do NOT enable it

	instanceID := "i-console-disabled-001"
	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS[instanceID] = &vm.VM{
		ID:     instanceID,
		Status: vm.StateRunning,
		Config: vm.Config{
			ConsoleLogPath: "/tmp/some-log.log",
		},
	}
	daemon.Instances.Mu.Unlock()

	topic := fmt.Sprintf("ec2.%s.GetConsoleOutput", instanceID)
	sub, err := daemon.natsConn.Subscribe(topic, daemon.handleEC2GetConsoleOutput)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	input := &ec2.GetConsoleOutputInput{
		InstanceId: aws.String(instanceID),
	}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request(topic, reqData, 5*time.Second)
	require.NoError(t, err)

	assert.Contains(t, string(reply.Data), "SerialConsoleSessionUnavailable")
}

func TestHandleEC2GetConsoleOutput_EnableThenDisable(t *testing.T) {
	natsURL := sharedNATSURL
	daemon := createFullTestDaemon(t, natsURL)

	instanceID := "i-console-toggle-001"
	tmpDir := t.TempDir()
	logPath := tmpDir + "/console.log"
	require.NoError(t, os.WriteFile(logPath, []byte("console output"), 0644))

	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS[instanceID] = &vm.VM{
		ID:     instanceID,
		Status: vm.StateRunning,
		Config: vm.Config{
			ConsoleLogPath: logPath,
		},
	}
	daemon.Instances.Mu.Unlock()

	topic := fmt.Sprintf("ec2.%s.GetConsoleOutput", instanceID)
	sub, err := daemon.natsConn.Subscribe(topic, daemon.handleEC2GetConsoleOutput)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Enable serial console access — GetConsoleOutput should succeed
	_, err = daemon.accountService.EnableSerialConsoleAccess(&ec2.EnableSerialConsoleAccessInput{})
	require.NoError(t, err)

	input := &ec2.GetConsoleOutputInput{InstanceId: aws.String(instanceID)}
	reqData, _ := json.Marshal(input)
	reply, err := daemon.natsConn.Request(topic, reqData, 5*time.Second)
	require.NoError(t, err)

	var output ec2.GetConsoleOutputOutput
	require.NoError(t, json.Unmarshal(reply.Data, &output))
	assert.NotEmpty(t, *output.Output)

	// Disable serial console access — GetConsoleOutput should fail
	_, err = daemon.accountService.DisableSerialConsoleAccess(&ec2.DisableSerialConsoleAccessInput{})
	require.NoError(t, err)

	reply, err = daemon.natsConn.Request(topic, reqData, 5*time.Second)
	require.NoError(t, err)
	assert.Contains(t, string(reply.Data), "SerialConsoleSessionUnavailable")
}

// TestAttachVolume_ZoneMismatch verifies that attaching a volume in a different AZ
// returns InvalidVolume.ZoneMismatch instead of proceeding.
func TestAttachVolume_ZoneMismatch(t *testing.T) {
	natsURL := sharedNATSURL
	daemon, store := createFullTestDaemonWithStore(t, natsURL)

	// Set the daemon's AZ
	daemon.config.AZ = "us-east-1a"

	instanceID := "i-test-az-mismatch"
	volumeID := "vol-az-mismatch"

	// Create a running instance
	instance := &vm.VM{
		ID:           instanceID,
		InstanceType: getTestInstanceType(),
		Status:       vm.StateRunning,
		Instance:     &ec2.Instance{},
		QMPClient:    &qmp.QMPClient{},
	}
	daemon.Instances.VMS[instanceID] = instance

	// Create a volume in a different AZ
	wrapper := struct {
		VolumeConfig viperblock.VolumeConfig `json:"VolumeConfig"`
	}{
		VolumeConfig: viperblock.VolumeConfig{
			VolumeMetadata: viperblock.VolumeMetadata{
				VolumeID:         volumeID,
				SizeGiB:          10,
				State:            "available",
				AvailabilityZone: "us-west-2a",
			},
		},
	}
	data, _ := json.Marshal(wrapper)
	store.PutObject(&awss3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String(volumeID + "/config.json"),
		Body:   strings.NewReader(string(data)),
	})

	// Subscribe handler
	sub, err := daemon.natsConn.Subscribe(
		fmt.Sprintf("ec2.cmd.%s", instanceID),
		daemon.handleEC2Events,
	)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	command := qmp.Command{
		ID: instanceID,
		Attributes: qmp.Attributes{
			AttachVolume: true,
		},
		AttachVolumeData: &qmp.AttachVolumeData{
			VolumeID: volumeID,
		},
	}
	cmdData, _ := json.Marshal(command)

	resp, err := daemon.natsConn.Request(
		fmt.Sprintf("ec2.cmd.%s", instanceID),
		cmdData,
		5*time.Second,
	)
	require.NoError(t, err)
	assert.Contains(t, string(resp.Data), "InvalidVolume.ZoneMismatch")
}
