package handlers_ec2_snapshot

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

// Ensure interface compliance
var _ SnapshotService = (*SnapshotServiceImpl)(nil)

// setupTestSnapshotService creates a snapshot service with in-memory storage for testing
func setupTestSnapshotService(t *testing.T) (*SnapshotServiceImpl, *objectstore.MemoryObjectStore) {
	store := objectstore.NewMemoryObjectStore()
	cfg := &config.Config{
		Predastore: config.PredastoreConfig{
			Bucket:    "test-bucket",
			AccessKey: "test-owner-123",
		},
	}

	svc := NewSnapshotServiceImplWithStore(cfg, store)
	return svc, store
}

// createTestVolume creates a test volume in the mock store
func createTestVolume(t *testing.T, store *objectstore.MemoryObjectStore, volumeID string, sizeGiB int) {
	volumeConfig := viperblock.VolumeConfig{
		VolumeMetadata: viperblock.VolumeMetadata{
			SizeGiB:          uint64(sizeGiB),
			IsEncrypted:      false,
			AvailabilityZone: "us-east-1a",
		},
	}
	data, err := json.Marshal(volumeConfig)
	require.NoError(t, err)

	_, err = store.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String("test-bucket"),
		Key:         aws.String(volumeID + "/config.json"),
		Body:        strings.NewReader(string(data)),
		ContentType: aws.String("application/json"),
	})
	require.NoError(t, err)
}

// TestCreateSnapshot tests creating a snapshot from a volume
func TestCreateSnapshot(t *testing.T) {
	svc, store := setupTestSnapshotService(t)

	// Create a test volume first
	volumeID := "vol-test123"
	createTestVolume(t, store, volumeID, 100)

	// Create snapshot
	result, err := svc.CreateSnapshot(&ec2.CreateSnapshotInput{
		VolumeId:    aws.String(volumeID),
		Description: aws.String("Test snapshot"),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("snapshot"),
				Tags: []*ec2.Tag{
					{Key: aws.String("Name"), Value: aws.String("test-snap")},
				},
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, strings.HasPrefix(*result.SnapshotId, "snap-"))
	assert.Equal(t, volumeID, *result.VolumeId)
	assert.Equal(t, int64(100), *result.VolumeSize)
	assert.Equal(t, "completed", *result.State)
	assert.Equal(t, "100%", *result.Progress)
	assert.Equal(t, "Test snapshot", *result.Description)

	// Verify tags
	assert.Len(t, result.Tags, 1)
	assert.Equal(t, "Name", *result.Tags[0].Key)
	assert.Equal(t, "test-snap", *result.Tags[0].Value)
}

// TestCreateSnapshot_MissingVolumeId tests creating a snapshot without volume ID
func TestCreateSnapshot_MissingVolumeId(t *testing.T) {
	svc, _ := setupTestSnapshotService(t)

	_, err := svc.CreateSnapshot(&ec2.CreateSnapshotInput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorInvalidParameterValue)
}

// TestCreateSnapshot_VolumeNotFound tests creating a snapshot from non-existent volume
func TestCreateSnapshot_VolumeNotFound(t *testing.T) {
	svc, _ := setupTestSnapshotService(t)

	_, err := svc.CreateSnapshot(&ec2.CreateSnapshotInput{
		VolumeId: aws.String("vol-nonexistent"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorInvalidVolumeNotFound)
}

// TestDescribeSnapshots tests listing all snapshots
func TestDescribeSnapshots(t *testing.T) {
	svc, store := setupTestSnapshotService(t)

	// Create test volumes
	createTestVolume(t, store, "vol-1", 50)
	createTestVolume(t, store, "vol-2", 100)

	// Create multiple snapshots
	snap1, err := svc.CreateSnapshot(&ec2.CreateSnapshotInput{
		VolumeId:    aws.String("vol-1"),
		Description: aws.String("Snapshot 1"),
	})
	require.NoError(t, err)

	snap2, err := svc.CreateSnapshot(&ec2.CreateSnapshotInput{
		VolumeId:    aws.String("vol-2"),
		Description: aws.String("Snapshot 2"),
	})
	require.NoError(t, err)

	// Describe all snapshots
	result, err := svc.DescribeSnapshots(&ec2.DescribeSnapshotsInput{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Snapshots, 2)

	// Verify snapshot IDs are present
	snapshotIDs := make(map[string]bool)
	for _, snap := range result.Snapshots {
		snapshotIDs[*snap.SnapshotId] = true
	}
	assert.True(t, snapshotIDs[*snap1.SnapshotId])
	assert.True(t, snapshotIDs[*snap2.SnapshotId])
}

// TestDescribeSnapshots_ByID tests listing specific snapshots by ID
func TestDescribeSnapshots_ByID(t *testing.T) {
	svc, store := setupTestSnapshotService(t)

	// Create test volume
	createTestVolume(t, store, "vol-1", 50)

	// Create multiple snapshots
	snap1, err := svc.CreateSnapshot(&ec2.CreateSnapshotInput{
		VolumeId: aws.String("vol-1"),
	})
	require.NoError(t, err)

	_, err = svc.CreateSnapshot(&ec2.CreateSnapshotInput{
		VolumeId: aws.String("vol-1"),
	})
	require.NoError(t, err)

	// Describe only the first snapshot
	result, err := svc.DescribeSnapshots(&ec2.DescribeSnapshotsInput{
		SnapshotIds: []*string{snap1.SnapshotId},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Snapshots, 1)
	assert.Equal(t, *snap1.SnapshotId, *result.Snapshots[0].SnapshotId)
}

// TestDescribeSnapshots_Empty tests listing snapshots when none exist
func TestDescribeSnapshots_Empty(t *testing.T) {
	svc, _ := setupTestSnapshotService(t)

	result, err := svc.DescribeSnapshots(&ec2.DescribeSnapshotsInput{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Snapshots)
}

// TestDeleteSnapshot tests deleting a snapshot
func TestDeleteSnapshot(t *testing.T) {
	svc, store := setupTestSnapshotService(t)

	// Create test volume and snapshot
	createTestVolume(t, store, "vol-1", 50)
	snap, err := svc.CreateSnapshot(&ec2.CreateSnapshotInput{
		VolumeId: aws.String("vol-1"),
	})
	require.NoError(t, err)

	// Verify snapshot exists
	result, err := svc.DescribeSnapshots(&ec2.DescribeSnapshotsInput{
		SnapshotIds: []*string{snap.SnapshotId},
	})
	require.NoError(t, err)
	assert.Len(t, result.Snapshots, 1)

	// Delete snapshot
	_, err = svc.DeleteSnapshot(&ec2.DeleteSnapshotInput{
		SnapshotId: snap.SnapshotId,
	})
	require.NoError(t, err)

	// Verify snapshot is gone
	result, err = svc.DescribeSnapshots(&ec2.DescribeSnapshotsInput{
		SnapshotIds: []*string{snap.SnapshotId},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Snapshots)
}

// TestDeleteSnapshot_NotFound tests deleting a non-existent snapshot
func TestDeleteSnapshot_NotFound(t *testing.T) {
	svc, _ := setupTestSnapshotService(t)

	_, err := svc.DeleteSnapshot(&ec2.DeleteSnapshotInput{
		SnapshotId: aws.String("snap-nonexistent"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorInvalidSnapshotNotFound)
}

// TestDeleteSnapshot_MissingID tests deleting without snapshot ID
func TestDeleteSnapshot_MissingID(t *testing.T) {
	svc, _ := setupTestSnapshotService(t)

	_, err := svc.DeleteSnapshot(&ec2.DeleteSnapshotInput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorInvalidParameterValue)
}

// TestCopySnapshot tests copying a snapshot
func TestCopySnapshot(t *testing.T) {
	svc, store := setupTestSnapshotService(t)

	// Create test volume and snapshot
	createTestVolume(t, store, "vol-1", 50)
	snap, err := svc.CreateSnapshot(&ec2.CreateSnapshotInput{
		VolumeId:    aws.String("vol-1"),
		Description: aws.String("Original snapshot"),
	})
	require.NoError(t, err)

	// Copy snapshot
	copyResult, err := svc.CopySnapshot(&ec2.CopySnapshotInput{
		SourceSnapshotId: snap.SnapshotId,
		Description:      aws.String("Copied snapshot"),
	})
	require.NoError(t, err)
	require.NotNil(t, copyResult)
	assert.True(t, strings.HasPrefix(*copyResult.SnapshotId, "snap-"))
	assert.NotEqual(t, *snap.SnapshotId, *copyResult.SnapshotId)

	// Verify both snapshots exist
	result, err := svc.DescribeSnapshots(&ec2.DescribeSnapshotsInput{})
	require.NoError(t, err)
	assert.Len(t, result.Snapshots, 2)
}

// TestCopySnapshot_NotFound tests copying a non-existent snapshot
func TestCopySnapshot_NotFound(t *testing.T) {
	svc, _ := setupTestSnapshotService(t)

	_, err := svc.CopySnapshot(&ec2.CopySnapshotInput{
		SourceSnapshotId: aws.String("snap-nonexistent"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorInvalidSnapshotNotFound)
}

// TestCopySnapshot_MissingSourceID tests copying without source snapshot ID
func TestCopySnapshot_MissingSourceID(t *testing.T) {
	svc, _ := setupTestSnapshotService(t)

	_, err := svc.CopySnapshot(&ec2.CopySnapshotInput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorInvalidParameterValue)
}

// TestCopySnapshot_PreservesTags tests that tags are copied
func TestCopySnapshot_PreservesTags(t *testing.T) {
	svc, store := setupTestSnapshotService(t)

	// Create test volume and snapshot with tags
	createTestVolume(t, store, "vol-1", 50)
	snap, err := svc.CreateSnapshot(&ec2.CreateSnapshotInput{
		VolumeId: aws.String("vol-1"),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("snapshot"),
				Tags: []*ec2.Tag{
					{Key: aws.String("Environment"), Value: aws.String("test")},
				},
			},
		},
	})
	require.NoError(t, err)

	// Copy snapshot
	copyResult, err := svc.CopySnapshot(&ec2.CopySnapshotInput{
		SourceSnapshotId: snap.SnapshotId,
	})
	require.NoError(t, err)

	// Verify copied snapshot has tags
	result, err := svc.DescribeSnapshots(&ec2.DescribeSnapshotsInput{
		SnapshotIds: []*string{copyResult.SnapshotId},
	})
	require.NoError(t, err)
	require.Len(t, result.Snapshots, 1)
	assert.Len(t, result.Snapshots[0].Tags, 1)
	assert.Equal(t, "Environment", *result.Snapshots[0].Tags[0].Key)
	assert.Equal(t, "test", *result.Snapshots[0].Tags[0].Value)
}

// TestIsValidSnapshotID tests the snapshot ID validation helper
func TestIsValidSnapshotID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"snap-abc123", true},
		{"snap-1", true},
		{"snap-", false},
		{"vol-abc123", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			assert.Equal(t, tc.valid, isValidSnapshotID(tc.id))
		})
	}
}
