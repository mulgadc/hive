package daemon

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/config"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	handlers_ec2_eigw "github.com/mulgadc/hive/hive/handlers/ec2/eigw"
	handlers_ec2_image "github.com/mulgadc/hive/hive/handlers/ec2/image"
	handlers_ec2_key "github.com/mulgadc/hive/hive/handlers/ec2/key"
	handlers_ec2_snapshot "github.com/mulgadc/hive/hive/handlers/ec2/snapshot"
	handlers_ec2_tags "github.com/mulgadc/hive/hive/handlers/ec2/tags"
	handlers_ec2_volume "github.com/mulgadc/hive/hive/handlers/ec2/volume"
	"github.com/mulgadc/hive/hive/objectstore"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createFullTestDaemon creates a test daemon with ALL services initialized (including
// key, image, snapshot, tags, eigw, account) using in-memory object stores.
func createFullTestDaemon(t *testing.T, natsURL string) *Daemon {
	daemon := createTestDaemon(t, natsURL)

	memStore := objectstore.NewMemoryObjectStore()
	cfg := daemon.config

	daemon.keyService = handlers_ec2_key.NewKeyServiceImplWithStore(memStore, cfg.Predastore.Bucket, "123456789")
	daemon.imageService = handlers_ec2_image.NewImageServiceImplWithStore(memStore, cfg.Predastore.Bucket, "123456789")
	daemon.volumeService = handlers_ec2_volume.NewVolumeServiceImplWithStore(cfg, memStore, daemon.natsConn)
	daemon.snapshotService = handlers_ec2_snapshot.NewSnapshotServiceImplWithStore(cfg, memStore, daemon.natsConn)
	daemon.tagsService = handlers_ec2_tags.NewTagsServiceImplWithStore(cfg, memStore)
	daemon.eigwService = handlers_ec2_eigw.NewEgressOnlyIGWServiceImpl(cfg)
	daemon.accountService = handlers_ec2_account.NewAccountSettingsServiceImpl(cfg)

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

	sub, err := daemon.natsConn.QueueSubscribe(
		fmt.Sprintf("ec2.cmd.%s", instanceID),
		"hive-events",
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

	sub, err := daemon.natsConn.QueueSubscribe(
		fmt.Sprintf("ec2.cmd.%s", instanceID),
		"hive-events",
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

	sub, err := daemon.natsConn.QueueSubscribe("ec2.cmd.i-nonexistent", "hive-events", daemon.handleEC2Events)
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

	sub, err := daemon.natsConn.QueueSubscribe("ec2.cmd.test", "hive-events", daemon.handleEC2Events)
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
