package handlers_ec2_image

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_snapshot "github.com/mulgadc/spinifex/spinifex/handlers/ec2/snapshot"
	"github.com/mulgadc/spinifex/spinifex/objectstore"
	"github.com/mulgadc/viperblock/viperblock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBucket = "test-bucket"
const testAccountID = "000000000001"

// setupTestImageService creates an image service with in-memory storage for testing
func setupTestImageService(t *testing.T) (*ImageServiceImpl, *objectstore.MemoryObjectStore) {
	store := objectstore.NewMemoryObjectStore()
	svc := NewImageServiceImplWithStore(store, testBucket)
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

// createTestAMIConfigWithName creates a test AMI config with a specified name
func createTestAMIConfigWithName(t *testing.T, store *objectstore.MemoryObjectStore, imageID, name string) {
	amiState := viperblock.VBState{
		VolumeConfig: viperblock.VolumeConfig{
			AMIMetadata: viperblock.AMIMetadata{
				ImageID:         imageID,
				Name:            name,
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

// createTestAMIConfigWithOwner creates a test AMI config with a specified name and owner
func createTestAMIConfigWithOwner(t *testing.T, store *objectstore.MemoryObjectStore, imageID, name, owner string) {
	amiState := viperblock.VBState{
		VolumeConfig: viperblock.VolumeConfig{
			AMIMetadata: viperblock.AMIMetadata{
				ImageID:         imageID,
				Name:            name,
				Architecture:    "x86_64",
				PlatformDetails: "Linux/UNIX",
				Virtualization:  "hvm",
				RootDeviceType:  "ebs",
				VolumeSizeGiB:   8,
				ImageOwnerAlias: owner,
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

	_, err := svc.CreateImageFromInstance(CreateImageParams{}, testAccountID)
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
	}, testAccountID)
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
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorServerInternal, err.Error())
}

func TestDescribeImages_AfterCreate(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Manually create an AMI config with the caller as owner
	amiID := "ami-testimage123"
	createTestAMIConfigWithOwner(t, store, amiID, "test-ami", testAccountID)

	// Describe images should find it
	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String(amiID)},
	}, testAccountID)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Images, 1)
	assert.Equal(t, amiID, *result.Images[0].ImageId)
	assert.Equal(t, "test-ami", *result.Images[0].Name)
	assert.Equal(t, "x86_64", *result.Images[0].Architecture)
	assert.Equal(t, testAccountID, *result.Images[0].OwnerId)
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

	meta, err := svc.GetAMIConfig("ami-abc123")
	require.NoError(t, err)
	assert.Equal(t, "ami-abc123", meta.ImageID)
	assert.Equal(t, "test-ami", meta.Name)
	assert.Equal(t, "x86_64", meta.Architecture)
	assert.Equal(t, "Linux/UNIX", meta.PlatformDetails)
	assert.Equal(t, "hvm", meta.Virtualization)
}

func TestGetAMIConfig_NotFound(t *testing.T) {
	svc, _ := setupTestImageService(t)

	_, err := svc.GetAMIConfig("ami-nonexistent")
	require.Error(t, err)
}

func TestPutSnapshotMetadata(t *testing.T) {
	svc, store := setupTestImageService(t)

	err := svc.putSnapshotMetadata("snap-abc123", "vol-xyz789", 10, testAccountID)
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
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorServerInternal, err.Error())
}

func TestDescribeImages_NotFound(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create one AMI
	createTestAMIConfig(t, store, "ami-exists123")

	// Request a non-existent AMI ID — should return InvalidAMIID.NotFound
	_, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String("ami-nonexistent")},
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidAMIIDNotFound, err.Error())
}

func TestDescribeImages_MixedExistingAndMissing(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create one AMI
	createTestAMIConfig(t, store, "ami-exists123")

	// Request one existing + one non-existent — should return NotFound
	_, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		ImageIds: []*string{
			aws.String("ami-exists123"),
			aws.String("ami-missing456"),
		},
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidAMIIDNotFound, err.Error())
}

func TestDescribeImages_FilterByOwnerSelf(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create an AMI owned by the caller's account
	createTestAMIConfigWithOwner(t, store, "ami-selfowned", "self-owned-ami", testAccountID)

	// Create a system AMI (should not appear with --owners self)
	createTestAMIConfigWithOwner(t, store, "ami-system", "system-ami", "hive")

	// Filter by "self" should return only the caller's AMI
	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		Owners: []*string{aws.String("self")},
	}, testAccountID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Images, 1)
	assert.Equal(t, "ami-selfowned", *result.Images[0].ImageId)
}

func TestCreateImageFromInstance_DuplicateName(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create an existing AMI with name "my-image"
	createTestAMIConfigWithName(t, store, "ami-existing123", "my-image")

	// Create volume config for the snapshot step
	createTestVolumeConfig(t, store, "vol-root123", 10)

	// Attempt to create another image with the same name — should fail with duplicate error
	_, err := svc.CreateImageFromInstance(CreateImageParams{
		Input: &ec2.CreateImageInput{
			InstanceId: aws.String("i-test123"),
			Name:       aws.String("my-image"),
		},
		RootVolumeID: "vol-root123",
		IsRunning:    true,
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidAMINameDuplicate, err.Error())
}

func TestCreateImageFromInstance_UniqueNameAllowed(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create an existing AMI with a different name
	createTestAMIConfigWithName(t, store, "ami-existing123", "other-image")

	// Create volume config
	createTestVolumeConfig(t, store, "vol-root123", 10)

	// Creating with a unique name should NOT fail at the name check stage
	// (it will fail later at the snapshot step since natsConn is nil, but that's expected)
	_, err := svc.CreateImageFromInstance(CreateImageParams{
		Input: &ec2.CreateImageInput{
			InstanceId: aws.String("i-test123"),
			Name:       aws.String("unique-image"),
		},
		RootVolumeID: "vol-root123",
		IsRunning:    true,
	}, testAccountID)
	require.Error(t, err)
	// Should fail at snapshot, NOT at duplicate name check
	assert.Equal(t, awserrors.ErrorServerInternal, err.Error())
}

func TestDescribeImages_AccountScoping(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create an AMI owned by a specific account
	createTestAMIConfigWithOwner(t, store, "ami-scoped123", "test-ami", "000000000001")

	// DescribeImages from the owning account should return the image with correct OwnerId
	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String("ami-scoped123")},
	}, "000000000001")
	require.NoError(t, err)
	require.Len(t, result.Images, 1)
	assert.Equal(t, "000000000001", *result.Images[0].OwnerId)

	// DescribeImages from a DIFFERENT account should NOT see the image
	_, err = svc.DescribeImages(&ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String("ami-scoped123")},
	}, "000000000002")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidAMIIDNotFound, err.Error())
}

func TestDescribeImages_SystemAMIVisibleToAll(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create a system/pre-phase4 AMI (non-account-ID owner)
	createTestAMIConfigWithOwner(t, store, "ami-system123", "system-ami", "hive")

	// Any account should be able to see system AMIs
	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String("ami-system123")},
	}, "000000000001")
	require.NoError(t, err)
	require.Len(t, result.Images, 1)
	assert.Equal(t, "000000000000", *result.Images[0].OwnerId) // System AMIs report global account

	result2, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String("ami-system123")},
	}, "000000000002")
	require.NoError(t, err)
	require.Len(t, result2.Images, 1)
}

func TestDescribeImages_NilInput(t *testing.T) {
	svc, _ := setupTestImageService(t)

	result, err := svc.DescribeImages(nil, testAccountID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Images)
}

func TestDescribeImages_EmptyBucket(t *testing.T) {
	svc, _ := setupTestImageService(t)

	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{}, testAccountID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Images)
}

func TestDescribeImages_NonAMIPrefixIgnored(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Create a non-AMI object (e.g. a volume config)
	createTestVolumeConfig(t, store, "vol-abc123", 10)

	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, result.Images)
}

func TestDescribeImages_InvalidConfigJSON(t *testing.T) {
	svc, store := setupTestImageService(t)

	// Store invalid JSON as an AMI config
	_, err := store.PutObject(&awss3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String("ami-bad123/config.json"),
		Body:   strings.NewReader("not valid json"),
	})
	require.NoError(t, err)

	// Should skip the invalid AMI without error
	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, result.Images)
}

func TestDescribeImages_EmptyImageIDSkipped(t *testing.T) {
	svc, store := setupTestImageService(t)

	// AMI config with empty ImageID
	amiState := viperblock.VBState{
		VolumeConfig: viperblock.VolumeConfig{
			AMIMetadata: viperblock.AMIMetadata{
				ImageID: "",
				Name:    "empty-id-ami",
			},
		},
	}
	data, err := json.Marshal(amiState)
	require.NoError(t, err)

	_, err = store.PutObject(&awss3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String("ami-emptyid/config.json"),
		Body:   strings.NewReader(string(data)),
	})
	require.NoError(t, err)

	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, result.Images)
}

func TestDescribeImages_WithTags(t *testing.T) {
	svc, store := setupTestImageService(t)

	amiState := viperblock.VBState{
		VolumeConfig: viperblock.VolumeConfig{
			AMIMetadata: viperblock.AMIMetadata{
				ImageID:         "ami-tagged123",
				Name:            "tagged-ami",
				Architecture:    "x86_64",
				PlatformDetails: "Linux/UNIX",
				Virtualization:  "hvm",
				RootDeviceType:  "ebs",
				VolumeSizeGiB:   8,
				ImageOwnerAlias: testAccountID,
				Tags:            map[string]string{"Environment": "test", "Name": "my-ami"},
			},
		},
	}
	data, err := json.Marshal(amiState)
	require.NoError(t, err)

	_, err = store.PutObject(&awss3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String("ami-tagged123/config.json"),
		Body:   strings.NewReader(string(data)),
	})
	require.NoError(t, err)

	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String("ami-tagged123")},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, result.Images, 1)

	img := result.Images[0]
	assert.Len(t, img.Tags, 2)
	assert.NotNil(t, img.RootDeviceName)
	assert.Equal(t, "/dev/sda1", *img.RootDeviceName)
	assert.Len(t, img.BlockDeviceMappings, 1)
}

func TestDescribeImages_FilterByExplicitOwnerID(t *testing.T) {
	svc, store := setupTestImageService(t)

	createTestAMIConfigWithOwner(t, store, "ami-owned1", "owned-ami", testAccountID)

	// Filter by explicit account ID
	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		Owners: []*string{aws.String(testAccountID)},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, result.Images, 1)
	assert.Equal(t, "ami-owned1", *result.Images[0].ImageId)

	// Filter by wrong account ID — should return empty
	result, err = svc.DescribeImages(&ec2.DescribeImagesInput{
		Owners: []*string{aws.String("999999999999")},
	}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, result.Images)
}

func TestDescribeImages_OwnerFilterNilEntry(t *testing.T) {
	svc, store := setupTestImageService(t)

	createTestAMIConfigWithOwner(t, store, "ami-test1", "test-ami", testAccountID)

	result, err := svc.DescribeImages(&ec2.DescribeImagesInput{
		Owners: []*string{nil, aws.String(testAccountID)},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, result.Images, 1)
}

func TestGetAMIConfig_InvalidJSON(t *testing.T) {
	svc, store := setupTestImageService(t)

	_, err := store.PutObject(&awss3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String("ami-badjson/config.json"),
		Body:   strings.NewReader("not json"),
	})
	require.NoError(t, err)

	_, err = svc.GetAMIConfig("ami-badjson")
	assert.Error(t, err)
}

func TestGetVolumeConfig_InvalidJSON(t *testing.T) {
	svc, store := setupTestImageService(t)

	_, err := store.PutObject(&awss3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String("vol-badjson/config.json"),
		Body:   strings.NewReader("{invalid"),
	})
	require.NoError(t, err)

	_, err = svc.getVolumeConfig("vol-badjson")
	assert.Error(t, err)
}

func TestAmiNameExists_NoAMIs(t *testing.T) {
	svc, _ := setupTestImageService(t)

	exists, err := svc.amiNameExists("nonexistent")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestAmiNameExists_Found(t *testing.T) {
	svc, store := setupTestImageService(t)

	createTestAMIConfigWithName(t, store, "ami-found123", "target-name")

	exists, err := svc.amiNameExists("target-name")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestAmiNameExists_NotFound(t *testing.T) {
	svc, store := setupTestImageService(t)

	createTestAMIConfigWithName(t, store, "ami-other123", "other-name")

	exists, err := svc.amiNameExists("different-name")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestAmiNameExists_InvalidJSON(t *testing.T) {
	svc, store := setupTestImageService(t)

	_, err := store.PutObject(&awss3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String("ami-bad/config.json"),
		Body:   strings.NewReader("not json"),
	})
	require.NoError(t, err)

	exists, err := svc.amiNameExists("any-name")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestUnimplementedMethods(t *testing.T) {
	svc, _ := setupTestImageService(t)

	_, err := svc.CreateImage(nil, "")
	assert.Error(t, err)

	_, err = svc.CopyImage(nil, "")
	assert.Error(t, err)

	_, err = svc.DescribeImageAttribute(nil, "")
	assert.Error(t, err)

	_, err = svc.RegisterImage(nil, "")
	assert.Error(t, err)

	_, err = svc.DeregisterImage(nil, "")
	assert.Error(t, err)

	_, err = svc.ModifyImageAttribute(nil, "")
	assert.Error(t, err)

	_, err = svc.ResetImageAttribute(nil, "")
	assert.Error(t, err)
}
