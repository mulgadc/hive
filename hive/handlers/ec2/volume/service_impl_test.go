package handlers_ec2_volume

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/objectstore"
	"github.com/mulgadc/viperblock/viperblock"
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

func TestDeleteVolume_BlockedBySnapshot(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	volumeID := "vol-test123"

	// Create volume config in store
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

	// Create a snapshot referencing this volume
	snapCfg := snapshotVolumeRef{VolumeID: volumeID}
	snapData, err := json.Marshal(snapCfg)
	require.NoError(t, err)

	_, err = store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String("snap-abc123/config.json"),
		Body:   strings.NewReader(string(snapData)),
	})
	require.NoError(t, err)

	// DeleteVolume should be blocked
	_, err = svc.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String(volumeID),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorVolumeInUse)
	assert.Contains(t, err.Error(), "snap-abc123")
}

func TestDeleteVolume_AllowedWithoutSnapshots(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()
	svc := newTestVolumeServiceWithStore("ap-southeast-2a", store)

	volumeID := "vol-test456"

	// Create volume config in store
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

	// Create a snapshot referencing a DIFFERENT volume
	snapCfg := snapshotVolumeRef{VolumeID: "vol-other"}
	snapData, err := json.Marshal(snapCfg)
	require.NoError(t, err)

	_, err = store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String("snap-xyz789/config.json"),
		Body:   strings.NewReader(string(snapData)),
	})
	require.NoError(t, err)

	// DeleteVolume should succeed (no snapshots reference this volume)
	_, err = svc.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String(volumeID),
	})
	require.NoError(t, err)
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
