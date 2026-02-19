package handlers_ec2_volume

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/objectstore"
	"github.com/mulgadc/viperblock/viperblock"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestVolumeService(az string) *VolumeServiceImpl {
	cfg := &config.Config{
		AZ: az,
		Predastore: config.PredastoreConfig{
			Bucket:    "test-bucket",
			Region:    "ap-southeast-2",
			Host:      "localhost:9000",
			AccessKey: "testkey",
			SecretKey: "testsecret",
		},
		WalDir: "/tmp/test-wal",
	}
	return NewVolumeServiceImplWithStore(cfg, objectstore.NewMemoryObjectStore(), nil)
}

func TestCreateVolume_Validation(t *testing.T) {
	tests := []struct {
		name    string
		az      string
		input   *ec2.CreateVolumeInput
		wantErr string
	}{
		{
			name:    "NilInput",
			az:      "ap-southeast-2a",
			input:   nil,
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "InvalidSize_Zero",
			az:   "ap-southeast-2a",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(0),
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "InvalidSize_Negative",
			az:   "ap-southeast-2a",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(-5),
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "InvalidSize_TooLarge",
			az:   "ap-southeast-2a",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(16385),
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "InvalidSize_NoSize",
			az:   "ap-southeast-2a",
			input: &ec2.CreateVolumeInput{
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "UnsupportedVolumeType_IO1",
			az:   "ap-southeast-2a",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String("ap-southeast-2a"),
				VolumeType:       aws.String("io1"),
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "UnsupportedVolumeType_GP2",
			az:   "ap-southeast-2a",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String("ap-southeast-2a"),
				VolumeType:       aws.String("gp2"),
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "UnsupportedVolumeType_ST1",
			az:   "ap-southeast-2a",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String("ap-southeast-2a"),
				VolumeType:       aws.String("st1"),
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "MismatchedAZ",
			az:   "ap-southeast-2a",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String("us-east-1a"),
			},
			wantErr: awserrors.ErrorInvalidAvailabilityZone,
		},
		{
			name: "EmptyAZ",
			az:   "ap-southeast-2a",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String(""),
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "NilAZ",
			az:   "ap-southeast-2a",
			input: &ec2.CreateVolumeInput{
				Size: aws.Int64(80),
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestVolumeService(tt.az)
			_, err := svc.CreateVolume(tt.input)
			assert.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}

// TestCreateVolume_PassesValidation verifies that valid inputs pass validation
// and only fail at the viperblock/S3 layer (no S3 backend in unit tests).
func TestCreateVolume_PassesValidation(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.CreateVolumeInput
	}{
		{
			name: "MinSize",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(1),
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
		},
		{
			name: "MaxSize",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(16384),
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
		},
		{
			name: "DefaultsToGP3",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestVolumeService("ap-southeast-2a")
			_, err := svc.CreateVolume(tt.input)
			if err != nil {
				assert.NotEqual(t, awserrors.ErrorInvalidParameterValue, err.Error())
				assert.NotEqual(t, awserrors.ErrorInvalidAvailabilityZone, err.Error())
			}
		})
	}
}

func TestDeleteVolume_Validation(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.DeleteVolumeInput
		wantErr string
	}{
		{
			name:    "NilInput",
			input:   nil,
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name:    "EmptyInput",
			input:   &ec2.DeleteVolumeInput{},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "NilVolumeId",
			input: &ec2.DeleteVolumeInput{
				VolumeId: nil,
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "EmptyVolumeId",
			input: &ec2.DeleteVolumeInput{
				VolumeId: aws.String(""),
			},
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestVolumeService("ap-southeast-2a")
			_, err := svc.DeleteVolume(tt.input)
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}

func TestDescribeVolumeStatus_NilInputDefaults(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")

	// nil input is defaulted to empty, then hits the slow path which
	// calls listAllVolumeIDs. With an empty MemoryObjectStore, no
	// volumes are found and an empty result is returned.
	output, err := svc.DescribeVolumeStatus(nil)
	require.NoError(t, err)
	assert.Empty(t, output.VolumeStatuses)
}

func TestDescribeVolumeStatus_WithVolumeIDs(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")

	// When volume IDs are provided, the fast path is taken. With an
	// empty MemoryObjectStore, the volume config is not found and an
	// InvalidVolume.NotFound error is returned.
	_, err := svc.DescribeVolumeStatus(&ec2.DescribeVolumeStatusInput{
		VolumeIds: []*string{aws.String("vol-abc123")},
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidVolumeNotFound, err.Error())
}

// newTestVolumeServiceWithStore creates a volume service with a specific memory store
func newTestVolumeServiceWithStore(az string, store *objectstore.MemoryObjectStore) *VolumeServiceImpl {
	cfg := &config.Config{
		AZ: az,
		Predastore: config.PredastoreConfig{
			Bucket:    "test-bucket",
			Region:    "ap-southeast-2",
			Host:      "localhost:9000",
			AccessKey: "testkey",
			SecretKey: "testsecret",
		},
		WalDir: "/tmp/test-wal",
	}
	return NewVolumeServiceImplWithStore(cfg, store, nil)
}

func TestCreateVolume_FromSnapshot_PassesValidation(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	snapshotID := "snap-test123"

	// Create snapshot metadata in store (matches hive snapshot service format)
	snapMeta := snapshotMetadata{
		VolumeID:   "vol-source",
		VolumeSize: 50,
	}
	snapData, err := json.Marshal(snapMeta)
	require.NoError(t, err)

	_, err = store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String(snapshotID + "/metadata.json"),
		Body:   strings.NewReader(string(snapData)),
	})
	require.NoError(t, err)

	// CreateVolume from snapshot without explicit size passes validation
	// (fails later at viperblock backend init because no S3 server in tests)
	_, err = svc.CreateVolume(&ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("ap-southeast-2a"),
		SnapshotId:       aws.String(snapshotID),
	})
	if err != nil {
		// Should not be a snapshot or validation error - those are the paths we're testing
		assert.NotContains(t, err.Error(), awserrors.ErrorInvalidSnapshotNotFound)
		assert.NotContains(t, err.Error(), awserrors.ErrorInvalidParameterValue)
	}
}

func TestCreateVolume_FromSnapshot_WithExplicitSize(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	snapshotID := "snap-test456"

	snapMeta := snapshotMetadata{
		VolumeID:   "vol-source",
		VolumeSize: 50,
	}
	snapData, err := json.Marshal(snapMeta)
	require.NoError(t, err)

	_, err = store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String(snapshotID + "/metadata.json"),
		Body:   strings.NewReader(string(snapData)),
	})
	require.NoError(t, err)

	// CreateVolume from snapshot with explicit larger size passes validation
	_, err = svc.CreateVolume(&ec2.CreateVolumeInput{
		Size:             aws.Int64(100),
		AvailabilityZone: aws.String("ap-southeast-2a"),
		SnapshotId:       aws.String(snapshotID),
	})
	if err != nil {
		assert.NotContains(t, err.Error(), awserrors.ErrorInvalidSnapshotNotFound)
		assert.NotContains(t, err.Error(), awserrors.ErrorInvalidParameterValue)
	}
}

func TestCreateVolume_FromSnapshot_NotFound(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")

	_, err := svc.CreateVolume(&ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("ap-southeast-2a"),
		SnapshotId:       aws.String("snap-nonexistent"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorInvalidSnapshotNotFound)
}

func TestCreateVolume_FromSnapshot_SizeSmallerThanSnapshot(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	snapshotID := "snap-sizecheck"

	snapMeta := snapshotMetadata{
		VolumeID:   "vol-source",
		VolumeSize: 50,
	}
	snapData, err := json.Marshal(snapMeta)
	require.NoError(t, err)

	_, err = store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String(snapshotID + "/metadata.json"),
		Body:   strings.NewReader(string(snapData)),
	})
	require.NoError(t, err)

	// Size 10 < snapshot size 50 -- must be rejected
	_, err = svc.CreateVolume(&ec2.CreateVolumeInput{
		Size:             aws.Int64(10),
		AvailabilityZone: aws.String("ap-southeast-2a"),
		SnapshotId:       aws.String(snapshotID),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorInvalidParameterValue)
}

func TestCreateVolume_FromSnapshot_SizeEqualToSnapshot(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	snapshotID := "snap-sizeequal"

	snapMeta := snapshotMetadata{
		VolumeID:   "vol-source",
		VolumeSize: 50,
	}
	snapData, err := json.Marshal(snapMeta)
	require.NoError(t, err)

	_, err = store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String(snapshotID + "/metadata.json"),
		Body:   strings.NewReader(string(snapData)),
	})
	require.NoError(t, err)

	// Size == snapshot size should pass validation (may fail at backend init)
	_, err = svc.CreateVolume(&ec2.CreateVolumeInput{
		Size:             aws.Int64(50),
		AvailabilityZone: aws.String("ap-southeast-2a"),
		SnapshotId:       aws.String(snapshotID),
	})
	if err != nil {
		assert.NotContains(t, err.Error(), awserrors.ErrorInvalidParameterValue)
		assert.NotContains(t, err.Error(), awserrors.ErrorInvalidSnapshotNotFound)
	}
}

func TestCreateVolume_FromSnapshot_CorruptMetadata(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	snapshotID := "snap-corrupt"

	// Put invalid JSON as snapshot metadata
	_, err := store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String(snapshotID + "/metadata.json"),
		Body:   strings.NewReader("not valid json{{{"),
	})
	require.NoError(t, err)

	_, err = svc.CreateVolume(&ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("ap-southeast-2a"),
		SnapshotId:       aws.String(snapshotID),
	})
	require.Error(t, err)
}

// setupTestVolumeKV creates a NATS JetStream test server and returns a KV bucket.
func setupTestVolumeKV(t *testing.T) nats.KeyValue {
	t.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	js, err := nc.JetStream()
	require.NoError(t, err)

	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket: "hive-volume-snapshots",
	})
	require.NoError(t, err)
	return kv
}

func createVolumeInStore(t *testing.T, store *objectstore.MemoryObjectStore, volumeID string) {
	t.Helper()
	volumeState := viperblock.VBState{
		VolumeConfig: viperblock.VolumeConfig{
			VolumeMetadata: viperblock.VolumeMetadata{
				VolumeID: volumeID,
				SizeGiB:  10,
				State:    "available",
			},
		},
	}
	data, err := json.Marshal(volumeState)
	require.NoError(t, err)

	_, err = store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String(volumeID + "/config.json"),
		Body:   strings.NewReader(string(data)),
	})
	require.NoError(t, err)
}

func TestDeleteVolume_BlockedByKV(t *testing.T) {
	kv := setupTestVolumeKV(t)
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)
	svc.snapshotKV = kv

	volumeID := "vol-kvblocked"
	createVolumeInStore(t, store, volumeID)

	// Put a snapshot ref in KV
	data, err := json.Marshal([]string{"snap-001"})
	require.NoError(t, err)
	_, err = kv.Put(volumeID, data)
	require.NoError(t, err)

	// DeleteVolume should be blocked
	_, err = svc.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String(volumeID),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorVolumeInUse)
}

func TestDeleteVolume_AllowedByKV(t *testing.T) {
	kv := setupTestVolumeKV(t)
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)
	svc.snapshotKV = kv

	volumeID := "vol-kvallowed"
	createVolumeInStore(t, store, volumeID)

	// No KV entry → delete allowed
	_, err := svc.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String(volumeID),
	})
	require.NoError(t, err)
}

func TestDeleteVolume_ErrorWhenKVNil(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)
	// snapshotKV is nil by default

	volumeID := "vol-nokvtest"
	createVolumeInStore(t, store, volumeID)

	// Should fail because snapshotKV is nil
	_, err := svc.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String(volumeID),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorServerInternal)
}

// createVolumeInStoreWithMeta seeds a volume config.json with custom metadata.
func createVolumeInStoreWithMeta(t *testing.T, store *objectstore.MemoryObjectStore, volumeID string, meta viperblock.VolumeMetadata) {
	t.Helper()
	wrapper := volumeConfigWrapper{
		VolumeConfig: viperblock.VolumeConfig{
			VolumeMetadata: meta,
		},
	}
	data, err := json.Marshal(wrapper)
	require.NoError(t, err)

	_, err = store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String(volumeID + "/config.json"),
		Body:   strings.NewReader(string(data)),
	})
	require.NoError(t, err)
}

// createVolumeInStoreWithVBState seeds a volume config.json as a full VBState
// (with BlockSize > 0) so that mergeVolumeConfig preserves VBState fields.
func createVolumeInStoreWithVBState(t *testing.T, store *objectstore.MemoryObjectStore, volumeID string, meta viperblock.VolumeMetadata, blockSize uint32, seqNum uint64) {
	t.Helper()
	state := viperblock.VBState{
		VolumeName: volumeID,
		VolumeSize: uint64(meta.SizeGiB) * 1024 * 1024 * 1024,
		BlockSize:  blockSize,
		SeqNum:     seqNum,
		VolumeConfig: viperblock.VolumeConfig{
			VolumeMetadata: meta,
		},
	}
	data, err := json.Marshal(state)
	require.NoError(t, err)

	_, err = store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String(volumeID + "/config.json"),
		Body:   strings.NewReader(string(data)),
	})
	require.NoError(t, err)
}

// --- Group 1: getVolumeByID tests ---

func TestGetVolumeByID_FullMetadata(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	now := time.Now()
	meta := viperblock.VolumeMetadata{
		VolumeID:            "vol-full",
		SizeGiB:             20,
		State:               "in-use",
		CreatedAt:           now,
		AvailabilityZone:    "ap-southeast-2a",
		VolumeType:          "gp3",
		IOPS:                5000,
		SnapshotID:          "snap-abc",
		IsEncrypted:         true,
		AttachedInstance:    "i-12345",
		DeviceName:          "/dev/nbd0",
		DeleteOnTermination: true,
		AttachedAt:          now,
		Tags:                map[string]string{"Name": "test-vol", "env": "dev"},
	}
	createVolumeInStoreWithMeta(t, store, "vol-full", meta)

	vol, err := svc.getVolumeByID("vol-full")
	require.NoError(t, err)

	assert.Equal(t, "vol-full", *vol.VolumeId)
	assert.Equal(t, int64(20), *vol.Size)
	assert.Equal(t, "in-use", *vol.State)
	assert.Equal(t, "gp3", *vol.VolumeType)
	assert.Equal(t, int64(5000), *vol.Iops)
	assert.Equal(t, "snap-abc", *vol.SnapshotId)
	assert.True(t, *vol.Encrypted)
	assert.Equal(t, "ap-southeast-2a", *vol.AvailabilityZone)

	// Attachment
	require.Len(t, vol.Attachments, 1)
	att := vol.Attachments[0]
	assert.Equal(t, "i-12345", *att.InstanceId)
	assert.Equal(t, "/dev/nbd0", *att.Device)
	assert.Equal(t, "attached", *att.State)
	assert.True(t, *att.DeleteOnTermination)

	// Tags
	assert.Len(t, vol.Tags, 2)
}

func TestGetVolumeByID_AttachmentDetached(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	meta := viperblock.VolumeMetadata{
		VolumeID:         "vol-detach",
		SizeGiB:          10,
		State:            "available",
		AttachedInstance: "i-99999",
		DeviceName:       "/dev/nbd1",
	}
	createVolumeInStoreWithMeta(t, store, "vol-detach", meta)

	vol, err := svc.getVolumeByID("vol-detach")
	require.NoError(t, err)

	require.Len(t, vol.Attachments, 1)
	assert.Equal(t, "detached", *vol.Attachments[0].State)
}

func TestGetVolumeByID_DefaultStateAndType(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	meta := viperblock.VolumeMetadata{
		VolumeID: "vol-defaults",
		SizeGiB:  5,
		State:    "",
	}
	createVolumeInStoreWithMeta(t, store, "vol-defaults", meta)

	vol, err := svc.getVolumeByID("vol-defaults")
	require.NoError(t, err)

	assert.Equal(t, "available", *vol.State)
	assert.Equal(t, "gp3", *vol.VolumeType)
}

func TestGetVolumeByID_EmptyVolumeID(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	meta := viperblock.VolumeMetadata{
		VolumeID: "",
		SizeGiB:  10,
	}
	createVolumeInStoreWithMeta(t, store, "vol-emptyid", meta)

	_, err := svc.getVolumeByID("vol-emptyid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "volume ID is empty")
}

func TestGetVolumeByID_ZeroSize(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	meta := viperblock.VolumeMetadata{
		VolumeID: "vol-zerosize",
		SizeGiB:  0,
	}
	createVolumeInStoreWithMeta(t, store, "vol-zerosize", meta)

	_, err := svc.getVolumeByID("vol-zerosize")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero size")
}

func TestGetVolumeByID_NotFound(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	_, err := svc.getVolumeByID("vol-nonexistent")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidVolumeNotFound, err.Error())
}

// --- Group 2: DescribeVolumes tests ---

func TestDescribeVolumes_NilInput(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	// Seed one volume so slow path has something to find
	createVolumeInStoreWithMeta(t, store, "vol-nil1", viperblock.VolumeMetadata{
		VolumeID: "vol-nil1", SizeGiB: 10, State: "available",
	})

	output, err := svc.DescribeVolumes(nil)
	require.NoError(t, err)
	assert.Len(t, output.Volumes, 1)
}

func TestDescribeVolumes_EmptyStore(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	output, err := svc.DescribeVolumes(&ec2.DescribeVolumesInput{})
	require.NoError(t, err)
	assert.Empty(t, output.Volumes)
}

func TestDescribeVolumes_SlowPath_MultipleVolumes(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	for _, id := range []string{"vol-a", "vol-b", "vol-c"} {
		createVolumeInStoreWithMeta(t, store, id, viperblock.VolumeMetadata{
			VolumeID: id, SizeGiB: 10, State: "available",
		})
	}

	output, err := svc.DescribeVolumes(&ec2.DescribeVolumesInput{})
	require.NoError(t, err)
	assert.Len(t, output.Volumes, 3)
}

func TestDescribeVolumes_FastPath_SpecificIDs(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	for _, id := range []string{"vol-x", "vol-y", "vol-z"} {
		createVolumeInStoreWithMeta(t, store, id, viperblock.VolumeMetadata{
			VolumeID: id, SizeGiB: 10, State: "available",
		})
	}

	output, err := svc.DescribeVolumes(&ec2.DescribeVolumesInput{
		VolumeIds: []*string{aws.String("vol-x"), aws.String("vol-z")},
	})
	require.NoError(t, err)
	assert.Len(t, output.Volumes, 2)

	ids := map[string]bool{}
	for _, v := range output.Volumes {
		ids[*v.VolumeId] = true
	}
	assert.True(t, ids["vol-x"])
	assert.True(t, ids["vol-z"])
}

func TestDescribeVolumes_FastPath_MixedExistingAndMissing(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-exists", viperblock.VolumeMetadata{
		VolumeID: "vol-exists", SizeGiB: 10, State: "available",
	})

	// AWS returns InvalidVolume.NotFound when any requested ID is missing
	_, err := svc.DescribeVolumes(&ec2.DescribeVolumesInput{
		VolumeIds: []*string{aws.String("vol-exists"), aws.String("vol-ghost")},
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidVolumeNotFound, err.Error())
}

func TestDescribeVolumes_FastPath_NilVolumeID(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-ok", viperblock.VolumeMetadata{
		VolumeID: "vol-ok", SizeGiB: 10, State: "available",
	})

	output, err := svc.DescribeVolumes(&ec2.DescribeVolumesInput{
		VolumeIds: []*string{nil, aws.String("vol-ok")},
	})
	require.NoError(t, err)
	assert.Len(t, output.Volumes, 1)
}

// --- Group 3: ModifyVolume tests ---

func TestModifyVolume_NilVolumeID(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")

	_, err := svc.ModifyVolume(&ec2.ModifyVolumeInput{
		VolumeId: nil,
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidVolumeIDMalformed, err.Error())
}

func TestModifyVolume_EmptyVolumeID(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")

	_, err := svc.ModifyVolume(&ec2.ModifyVolumeInput{
		VolumeId: aws.String(""),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidVolumeIDMalformed, err.Error())
}

func TestModifyVolume_VolumeNotFound(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")

	_, err := svc.ModifyVolume(&ec2.ModifyVolumeInput{
		VolumeId: aws.String("vol-nonexistent"),
		Size:     aws.Int64(20),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidVolumeNotFound, err.Error())
}

func TestModifyVolume_ShrinkRejected(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-shrink", viperblock.VolumeMetadata{
		VolumeID: "vol-shrink", SizeGiB: 10, State: "available",
	})

	_, err := svc.ModifyVolume(&ec2.ModifyVolumeInput{
		VolumeId: aws.String("vol-shrink"),
		Size:     aws.Int64(5),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestModifyVolume_SameSizeRejected(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-same", viperblock.VolumeMetadata{
		VolumeID: "vol-same", SizeGiB: 10, State: "available",
	})

	_, err := svc.ModifyVolume(&ec2.ModifyVolumeInput{
		VolumeId: aws.String("vol-same"),
		Size:     aws.Int64(10),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestModifyVolume_AttachedInUse(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-inuse", viperblock.VolumeMetadata{
		VolumeID:         "vol-inuse",
		SizeGiB:          10,
		State:            "in-use",
		AttachedInstance: "i-12345",
	})

	_, err := svc.ModifyVolume(&ec2.ModifyVolumeInput{
		VolumeId: aws.String("vol-inuse"),
		Size:     aws.Int64(20),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorIncorrectState, err.Error())
}

func TestModifyVolume_SuccessfulGrow(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-grow", viperblock.VolumeMetadata{
		VolumeID:   "vol-grow",
		SizeGiB:    10,
		State:      "available",
		VolumeType: "gp3",
		IOPS:       3000,
	})

	output, err := svc.ModifyVolume(&ec2.ModifyVolumeInput{
		VolumeId: aws.String("vol-grow"),
		Size:     aws.Int64(20),
	})
	require.NoError(t, err)

	mod := output.VolumeModification
	assert.Equal(t, "vol-grow", *mod.VolumeId)
	assert.Equal(t, int64(10), *mod.OriginalSize)
	assert.Equal(t, int64(20), *mod.TargetSize)
	assert.Equal(t, "completed", *mod.ModificationState)
	assert.Equal(t, int64(100), *mod.Progress)

	// Verify persisted config
	cfg, err := svc.GetVolumeConfig("vol-grow")
	require.NoError(t, err)
	assert.Equal(t, uint64(20), cfg.VolumeMetadata.SizeGiB)
}

func TestModifyVolume_ModifyTypeAndIOPS(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-typemod", viperblock.VolumeMetadata{
		VolumeID:   "vol-typemod",
		SizeGiB:    10,
		State:      "available",
		VolumeType: "gp3",
		IOPS:       3000,
	})

	output, err := svc.ModifyVolume(&ec2.ModifyVolumeInput{
		VolumeId:   aws.String("vol-typemod"),
		Size:       aws.Int64(20),
		VolumeType: aws.String("io1"),
		Iops:       aws.Int64(10000),
	})
	require.NoError(t, err)

	mod := output.VolumeModification
	assert.Equal(t, "gp3", *mod.OriginalVolumeType)
	assert.Equal(t, "io1", *mod.TargetVolumeType)
	assert.Equal(t, int64(3000), *mod.OriginalIops)
	assert.Equal(t, int64(10000), *mod.TargetIops)
}

func TestModifyVolume_AvailableWithAttachment(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	// Volume attached but state is "available" (stopped instance) -- allowed
	createVolumeInStoreWithMeta(t, store, "vol-stopinst", viperblock.VolumeMetadata{
		VolumeID:         "vol-stopinst",
		SizeGiB:          10,
		State:            "available",
		AttachedInstance: "i-stopped",
	})

	output, err := svc.ModifyVolume(&ec2.ModifyVolumeInput{
		VolumeId: aws.String("vol-stopinst"),
		Size:     aws.Int64(20),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(20), *output.VolumeModification.TargetSize)
}

// --- Group 4: UpdateVolumeState tests ---

func TestUpdateVolumeState_AttachVolume(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-attach", viperblock.VolumeMetadata{
		VolumeID: "vol-attach", SizeGiB: 10, State: "available",
	})

	err := svc.UpdateVolumeState("vol-attach", "in-use", "i-abc123", "/dev/nbd0")
	require.NoError(t, err)

	cfg, err := svc.GetVolumeConfig("vol-attach")
	require.NoError(t, err)
	assert.Equal(t, "in-use", cfg.VolumeMetadata.State)
	assert.Equal(t, "i-abc123", cfg.VolumeMetadata.AttachedInstance)
	assert.Equal(t, "/dev/nbd0", cfg.VolumeMetadata.DeviceName)
	assert.False(t, cfg.VolumeMetadata.AttachedAt.IsZero())
}

func TestUpdateVolumeState_DetachVolume(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-detach2", viperblock.VolumeMetadata{
		VolumeID:         "vol-detach2",
		SizeGiB:          10,
		State:            "in-use",
		AttachedInstance: "i-xyz789",
		DeviceName:       "/dev/nbd1",
	})

	err := svc.UpdateVolumeState("vol-detach2", "available", "", "")
	require.NoError(t, err)

	cfg, err := svc.GetVolumeConfig("vol-detach2")
	require.NoError(t, err)
	assert.Equal(t, "available", cfg.VolumeMetadata.State)
	assert.Empty(t, cfg.VolumeMetadata.AttachedInstance)
	assert.Empty(t, cfg.VolumeMetadata.DeviceName)
}

func TestUpdateVolumeState_VolumeNotFound(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")

	err := svc.UpdateVolumeState("vol-missing", "available", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get volume config")
}

func TestUpdateVolumeState_PreservesVBState(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	meta := viperblock.VolumeMetadata{
		VolumeID: "vol-vbstate", SizeGiB: 10, State: "available",
	}
	createVolumeInStoreWithVBState(t, store, "vol-vbstate", meta, 4096, 5)

	err := svc.UpdateVolumeState("vol-vbstate", "in-use", "i-preserve", "/dev/nbd0")
	require.NoError(t, err)

	// Re-read the raw JSON to verify VBState fields survived
	getResult, err := store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String("vol-vbstate/config.json"),
	})
	require.NoError(t, err)

	body, err := io.ReadAll(getResult.Body)
	require.NoError(t, err)

	var state viperblock.VBState
	require.NoError(t, json.Unmarshal(body, &state))

	assert.Equal(t, uint32(4096), state.BlockSize)
	assert.Equal(t, uint64(5), state.SeqNum)
	assert.Equal(t, "in-use", state.VolumeConfig.VolumeMetadata.State)
	assert.Equal(t, "i-preserve", state.VolumeConfig.VolumeMetadata.AttachedInstance)
}

// --- Group 6: listAllVolumeIDs tests ---

func TestListAllVolumeIDs_FiltersCorrectly(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	// Seed objects with various prefixes
	for _, key := range []string{
		"vol-abc/config.json",
		"vol-def/config.json",
		"vol-abc-efi/config.json",
		"vol-abc-cloudinit/config.json",
		"ami-123/metadata.json",
		"snap-456/metadata.json",
	} {
		_, err := store.PutObject(&s3.PutObjectInput{
			Bucket: aws.String("test-bucket"),
			Key:    aws.String(key),
			Body:   strings.NewReader("{}"),
		})
		require.NoError(t, err)
	}

	ids, err := svc.listAllVolumeIDs()
	require.NoError(t, err)

	// Should only contain vol-abc and vol-def (not efi/cloudinit/ami/snap)
	assert.Len(t, ids, 2)
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	assert.True(t, idSet["vol-abc"])
	assert.True(t, idSet["vol-def"])
}

func TestListAllVolumeIDs_EmptyBucket(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	ids, err := svc.listAllVolumeIDs()
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestListAllVolumeIDs_NilPrefix(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	// Seed a single volume to ensure the loop runs
	_, err := store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String("vol-only/config.json"),
		Body:   strings.NewReader("{}"),
	})
	require.NoError(t, err)

	ids, err := svc.listAllVolumeIDs()
	require.NoError(t, err)
	assert.Len(t, ids, 1)
	assert.Equal(t, "vol-only", ids[0])
}

// --- Group 7: DeleteVolume remaining tests ---

func TestDeleteVolume_VolumeInUse(t *testing.T) {
	kv := setupTestVolumeKV(t)
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)
	svc.snapshotKV = kv

	createVolumeInStoreWithMeta(t, store, "vol-busy", viperblock.VolumeMetadata{
		VolumeID:         "vol-busy",
		SizeGiB:          10,
		State:            "in-use",
		AttachedInstance: "i-running",
	})

	_, err := svc.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String("vol-busy"),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorVolumeInUse, err.Error())
}

func TestDeleteVolume_VolumeAttachedButAvailable(t *testing.T) {
	kv := setupTestVolumeKV(t)
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)
	svc.snapshotKV = kv

	// State != "available" triggers the check even without "in-use"
	// Actually: the code checks `State != "available" || AttachedInstance != ""`
	// So having AttachedInstance set while state is "available" still triggers VolumeInUse
	createVolumeInStoreWithMeta(t, store, "vol-attached", viperblock.VolumeMetadata{
		VolumeID:         "vol-attached",
		SizeGiB:          10,
		State:            "available",
		AttachedInstance: "i-stopped",
	})

	_, err := svc.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String("vol-attached"),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorVolumeInUse, err.Error())
}

func TestDeleteVolume_WithNATSNotification(t *testing.T) {
	kv := setupTestVolumeKV(t)
	store := objectstore.NewMemoryObjectStore()

	// Set up NATS server and connection for this test
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)
	svc.snapshotKV = kv
	svc.natsConn = nc

	volumeID := "vol-natsok"
	createVolumeInStoreWithMeta(t, store, volumeID, viperblock.VolumeMetadata{
		VolumeID: volumeID, SizeGiB: 10, State: "available",
	})

	// Subscribe to ebs.delete and reply with success
	sub, err := nc.Subscribe("ebs.delete", func(msg *nats.Msg) {
		resp := config.EBSDeleteResponse{Volume: volumeID, Success: true}
		data, _ := json.Marshal(resp)
		msg.Respond(data)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	_, err = svc.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String(volumeID),
	})
	require.NoError(t, err)

	// Verify all objects deleted
	assert.Equal(t, 0, store.Count())
}

func TestDescribeVolumeStatus_SlowPath_WithVolumes(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	for _, id := range []string{"vol-s1", "vol-s2"} {
		createVolumeInStoreWithMeta(t, store, id, viperblock.VolumeMetadata{
			VolumeID:         id,
			SizeGiB:          10,
			State:            "available",
			AvailabilityZone: "ap-southeast-2a",
		})
	}

	output, err := svc.DescribeVolumeStatus(nil)
	require.NoError(t, err)
	assert.Len(t, output.VolumeStatuses, 2)

	for _, item := range output.VolumeStatuses {
		assert.Equal(t, "ok", *item.VolumeStatus.Status)
		assert.Equal(t, "ap-southeast-2a", *item.AvailabilityZone)
		assert.Len(t, item.VolumeStatus.Details, 2)
	}
}

func TestDescribeVolumeStatus_FastPath_WithVolumes(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-status1", viperblock.VolumeMetadata{
		VolumeID:         "vol-status1",
		SizeGiB:          10,
		State:            "in-use",
		AvailabilityZone: "ap-southeast-2a",
	})

	output, err := svc.DescribeVolumeStatus(&ec2.DescribeVolumeStatusInput{
		VolumeIds: []*string{aws.String("vol-status1")},
	})
	require.NoError(t, err)
	require.Len(t, output.VolumeStatuses, 1)
	assert.Equal(t, "vol-status1", *output.VolumeStatuses[0].VolumeId)
	assert.Equal(t, "ok", *output.VolumeStatuses[0].VolumeStatus.Status)
}

func TestDescribeVolumes_SlowPath_SkipsBrokenConfig(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	// Good volume
	createVolumeInStoreWithMeta(t, store, "vol-good", viperblock.VolumeMetadata{
		VolumeID: "vol-good", SizeGiB: 10, State: "available",
	})
	// Bad volume: zero size triggers error in getVolumeByID
	createVolumeInStoreWithMeta(t, store, "vol-bad", viperblock.VolumeMetadata{
		VolumeID: "vol-bad", SizeGiB: 0,
	})

	output, err := svc.DescribeVolumes(&ec2.DescribeVolumesInput{})
	require.NoError(t, err)
	// Only the good volume should be returned
	assert.Len(t, output.Volumes, 1)
	assert.Equal(t, "vol-good", *output.Volumes[0].VolumeId)
}

func TestDescribeVolumeStatus_SlowPath_SkipsBrokenConfig(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	createVolumeInStoreWithMeta(t, store, "vol-ok", viperblock.VolumeMetadata{
		VolumeID: "vol-ok", SizeGiB: 10, State: "available", AvailabilityZone: "ap-southeast-2a",
	})
	createVolumeInStoreWithMeta(t, store, "vol-broken", viperblock.VolumeMetadata{
		VolumeID: "vol-broken", SizeGiB: 0,
	})

	output, err := svc.DescribeVolumeStatus(nil)
	require.NoError(t, err)
	assert.Len(t, output.VolumeStatuses, 1)
}

func TestNewVolumeServiceImplWithStore_WithSnapshotKV(t *testing.T) {
	kv := setupTestVolumeKV(t)
	store := objectstore.NewMemoryObjectStore()
	cfg := &config.Config{
		Predastore: config.PredastoreConfig{Bucket: "test-bucket"},
	}
	svc := NewVolumeServiceImplWithStore(cfg, store, nil, kv)
	assert.NotNil(t, svc.snapshotKV)
}

func TestDeleteVolume_NATSErrorResponse(t *testing.T) {
	kv := setupTestVolumeKV(t)
	store := objectstore.NewMemoryObjectStore()

	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)
	svc.snapshotKV = kv
	svc.natsConn = nc

	volumeID := "vol-natserr"
	createVolumeInStoreWithMeta(t, store, volumeID, viperblock.VolumeMetadata{
		VolumeID: volumeID, SizeGiB: 10, State: "available",
	})

	// Subscribe and respond with an error
	sub, err := nc.Subscribe("ebs.delete", func(msg *nats.Msg) {
		resp := config.EBSDeleteResponse{Volume: volumeID, Error: "volume still mounted"}
		data, _ := json.Marshal(resp)
		msg.Respond(data)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	_, err = svc.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String(volumeID),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorServerInternal, err.Error())
}

func TestDeleteVolume_NATSTimeout(t *testing.T) {
	kv := setupTestVolumeKV(t)
	store := objectstore.NewMemoryObjectStore()

	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)
	svc.snapshotKV = kv
	svc.natsConn = nc

	volumeID := "vol-natstimeout"
	createVolumeInStoreWithMeta(t, store, volumeID, viperblock.VolumeMetadata{
		VolumeID: volumeID, SizeGiB: 10, State: "available",
	})

	// No subscriber → NATS request will timeout, but delete proceeds (best-effort)
	_, err = svc.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String(volumeID),
	})
	require.NoError(t, err)
}
