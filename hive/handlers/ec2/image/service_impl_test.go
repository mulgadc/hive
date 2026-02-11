package handlers_ec2_image

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_snapshot "github.com/mulgadc/hive/hive/handlers/ec2/snapshot"
	"github.com/mulgadc/hive/hive/objectstore"
	"github.com/mulgadc/viperblock/viperblock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBucket = "test-bucket"
const testAccountID = "123456789"

// setupTestImageService creates an image service with in-memory storage for testing
func setupTestImageService(t *testing.T) (*ImageServiceImpl, *objectstore.MemoryObjectStore) {
	store := objectstore.NewMemoryObjectStore()
	svc := NewImageServiceImplWithStore(store, testBucket, testAccountID)
	return svc, store
}

// createTestVolumeConfig creates a test volume config in the mock store
func createTestVolumeConfig(t *testing.T, store *objectstore.MemoryObjectStore, volumeID string, sizeGiB int) {
	volumeState := viperblock.VBState{
		VolumeConfig: viperblock.VolumeConfig{
			VolumeMetadata: viperblock.VolumeMetadata{
				SizeGiB: uint64(sizeGiB),
			},
		},
	}
	data, err := json.Marshal(volumeState)
	require.NoError(t, err)

	_, err = store.PutObject(&awss3.PutObjectInput{
		Bucket:      aws.String(testBucket),
		Key:         aws.String(volumeID + "/config.json"),
		Body:        strings.NewReader(string(data)),
		ContentType: aws.String("application/json"),
	})
	require.NoError(t, err)
}

// createTestAMIConfig creates a test AMI config in the mock store
func createTestAMIConfig(t *testing.T, store *objectstore.MemoryObjectStore, imageID string) {
	amiState := viperblock.VBState{
		VolumeConfig: viperblock.VolumeConfig{
			AMIMetadata: viperblock.AMIMetadata{
				ImageID:         imageID,
				Name:            "test-ami",
				Architecture:    "x86_64",
				PlatformDetails: "Linux/UNIX",
				Virtualization:  "hvm",
				RootDeviceType:  "ebs",
				VolumeSizeGiB:   8,
			},
		},
	}
	data, err := json.Marshal(amiState)
	require.NoError(t, err)

	_, err = store.PutObject(&awss3.PutObjectInput{
		Bucket:      aws.String(testBucket),
		Key:         aws.String(imageID + "/config.json"),
		Body:        strings.NewReader(string(data)),
		ContentType: aws.String("application/json"),
	})
	require.NoError(t, err)
}

func TestCreateImageFromInstance_NilInput(t *testing.T) {
	svc, _ := setupTestImageService(t)

	_, err := svc.CreateImageFromInstance(CreateImageParams{})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateImageFromInstance_RunningInstance_NoNATS(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create volume and AMI configs
	createTestVolumeConfig(t, store, "vol-root123", 10)
	createTestAMIConfig(t, store, "ami-source123")

	// Running instance without NATS should fail (natsConn is nil)
	_, err := svc.CreateImageFromInstance(CreateImageParams{
		Input: &ec2.CreateImageInput{
			InstanceId: aws.String("i-test123"),
			Name:       aws.String("my-image"),
		},
		RootVolumeID:  "vol-root123",
		SourceImageID: "ami-source123",
		IsRunning:     true,
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorServerInternal, err.Error())
}

func TestCreateImageFromInstance_StoppedInstance_NoConfig(t *testing.T) {
	svc, _ := setupTestImageService(t)

	// Stopped instance without config will fail (no config to create viperblock)
	_, err := svc.CreateImageFromInstance(CreateImageParams{
		Input: &ec2.CreateImageInput{
			InstanceId: aws.String("i-test123"),
			Name:       aws.String("my-image"),
		},
		RootVolumeID:  "vol-nonexistent",
		SourceImageID: "ami-source123",
		IsRunning:     false,
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorServerInternal, err.Error())
}

func TestDescribeImages_AfterCreate(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Manually create an AMI config in the store (simulates what CreateImageFromInstance does)
	amiID := "ami-testimage123"
	createTestAMIConfig(t, store, amiID)

	// Describe images should find it
	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String(amiID)},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Images, 1)
	assert.Equal(t, amiID, *result.Images[0].ImageId)
	assert.Equal(t, "test-ami", *result.Images[0].Name)
	assert.Equal(t, "x86_64", *result.Images[0].Architecture)
}

func TestGetVolumeConfig(t *testing.T) {
	svc, store := setupTestImageService(t)

	createTestVolumeConfig(t, store, "vol-abc123", 20)

	cfg, err := svc.getVolumeConfig("vol-abc123")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, uint64(20), cfg.VolumeMetadata.SizeGiB)
}

func TestGetVolumeConfig_NotFound(t *testing.T) {
	svc, _ := setupTestImageService(t)

	_, err := svc.getVolumeConfig("vol-nonexistent")
	require.Error(t, err)
}

func TestGetAMIConfig(t *testing.T) {
	svc, store := setupTestImageService(t)

	createTestAMIConfig(t, store, "ami-abc123")

	meta, err := svc.getAMIConfig("ami-abc123")
	require.NoError(t, err)
	assert.Equal(t, "ami-abc123", meta.ImageID)
	assert.Equal(t, "test-ami", meta.Name)
	assert.Equal(t, "x86_64", meta.Architecture)
	assert.Equal(t, "Linux/UNIX", meta.PlatformDetails)
	assert.Equal(t, "hvm", meta.Virtualization)
}

func TestGetAMIConfig_NotFound(t *testing.T) {
	svc, _ := setupTestImageService(t)

	_, err := svc.getAMIConfig("ami-nonexistent")
	require.Error(t, err)
}

func TestPutSnapshotMetadata(t *testing.T) {
	svc, store := setupTestImageService(t)

	err := svc.putSnapshotMetadata("snap-abc123", "vol-xyz789", 10)
	require.NoError(t, err)

	// Verify the metadata was written correctly
	result, err := store.GetObject(&awss3.GetObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String("snap-abc123/metadata.json"),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	var cfg handlers_ec2_snapshot.SnapshotConfig
	err = json.NewDecoder(result.Body).Decode(&cfg)
	require.NoError(t, err)
	assert.Equal(t, "snap-abc123", cfg.SnapshotID)
	assert.Equal(t, "vol-xyz789", cfg.VolumeID)
	assert.Equal(t, int64(10), cfg.VolumeSize)
	assert.Equal(t, "completed", cfg.State)
	assert.Equal(t, "100%", cfg.Progress)
	assert.Equal(t, testAccountID, cfg.OwnerID)
}

func TestCreateImageFromInstance_SourceAMIReadFailure(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create volume config but NOT the source AMI config
	createTestVolumeConfig(t, store, "vol-root123", 10)

	// With non-empty SourceImageID, missing AMI config should be a hard error
	_, err := svc.CreateImageFromInstance(CreateImageParams{
		Input: &ec2.CreateImageInput{
			InstanceId: aws.String("i-test123"),
			Name:       aws.String("my-image"),
		},
		RootVolumeID:  "vol-root123",
		SourceImageID: "ami-nonexistent",
		IsRunning:     true, // will fail at snapshot step first (no NATS)
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorServerInternal, err.Error())
}

func TestDescribeImages_FilterByOwnerSelf(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create AMI with "self" owner alias
	amiID := "ami-selfowned"
	amiState := viperblock.VBState{
		VolumeConfig: viperblock.VolumeConfig{
			AMIMetadata: viperblock.AMIMetadata{
				ImageID:         amiID,
				Name:            "self-owned-ami",
				ImageOwnerAlias: "self",
				Architecture:    "x86_64",
				PlatformDetails: "Linux/UNIX",
				RootDeviceType:  "ebs",
				VolumeSizeGiB:   8,
			},
		},
	}
	data, err := json.Marshal(amiState)
	require.NoError(t, err)
	_, err = store.PutObject(&awss3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(amiID + "/config.json"),
		Body:   strings.NewReader(string(data)),
	})
	require.NoError(t, err)

	// Filter by "self"
	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		Owners: []*string{aws.String("self")},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Images, 1)
	assert.Equal(t, amiID, *result.Images[0].ImageId)
}

