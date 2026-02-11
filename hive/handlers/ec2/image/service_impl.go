package handlers_ec2_image

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	handlers_ec2_snapshot "github.com/mulgadc/hive/hive/handlers/ec2/snapshot"
	"github.com/mulgadc/hive/hive/objectstore"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/viperblock/viperblock"
	vbs3 "github.com/mulgadc/viperblock/viperblock/backends/s3"
	"github.com/nats-io/nats.go"
)

// Ensure ImageServiceImpl implements ImageService
var _ ImageService = (*ImageServiceImpl)(nil)

// CreateImageParams holds parameters for creating an AMI from an instance.
// Used by the daemon handler which extracts instance state before calling the service.
type CreateImageParams struct {
	Input         *ec2.CreateImageInput
	RootVolumeID  string
	SourceImageID string
	IsRunning     bool // true = use ebs.snapshot NATS, false = snapshot from S3 state
}

// ImageServiceImpl handles AMI image operations with S3 storage
type ImageServiceImpl struct {
	config     *config.Config
	store      objectstore.ObjectStore
	natsConn   *nats.Conn
	bucketName string
	accountID  string // AWS account ID for S3 key storage path
}

// NewImageServiceImpl creates a new daemon-side image service
func NewImageServiceImpl(cfg *config.Config, natsConn *nats.Conn) *ImageServiceImpl {
	store := objectstore.NewS3ObjectStoreFromConfig(
		cfg.Predastore.Host,
		cfg.Predastore.Region,
		cfg.Predastore.AccessKey,
		cfg.Predastore.SecretKey,
	)

	return &ImageServiceImpl{
		config:     cfg,
		store:      store,
		natsConn:   natsConn,
		bucketName: cfg.Predastore.Bucket,
		accountID:  "123456789", // TODO: Implement proper account ID management
	}
}

// NewImageServiceImplWithStore creates an image service with a custom object store (for testing)
func NewImageServiceImplWithStore(store objectstore.ObjectStore, bucketName, accountID string) *ImageServiceImpl {
	return &ImageServiceImpl{
		store:      store,
		bucketName: bucketName,
		accountID:  accountID,
	}
}

// DescribeImages lists available AMI images by reading config.json files from S3
func (s *ImageServiceImpl) DescribeImages(input *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
	if input == nil {
		input = &ec2.DescribeImagesInput{}
	}

	slog.Info("Describing images", "filters", input.Filters, "imageIds", input.ImageIds)

	// List all prefixes in the bucket (AMIs are stored as ami-<id>/ directories)
	result, err := s.store.ListObjects(&s3.ListObjectsInput{
		Bucket:    aws.String(s.bucketName),
		Delimiter: aws.String("/"),
	})

	if err != nil {
		slog.Error("Failed to list S3 objects", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	var images []*ec2.Image

	// Iterate over CommonPrefixes to find ami-* directories
	for _, prefix := range result.CommonPrefixes {
		if prefix.Prefix == nil {
			continue
		}

		prefixStr := *prefix.Prefix
		slog.Info("Processing S3 prefix", "prefix", prefixStr)

		// Only check prefixes that match pattern: ami-<id>/
		if !strings.HasPrefix(prefixStr, "ami-") {
			continue
		}

		// Construct path to config.json for this AMI directory
		configKey := prefixStr + "config.json"

		// Get the config.json file
		getResult, err := s.store.GetObject(&s3.GetObjectInput{
			Bucket: aws.String(s.bucketName),
			Key:    aws.String(configKey),
		})
		if err != nil {
			if objectstore.IsNoSuchKeyError(err) {
				slog.Debug("Config file not found", "key", configKey)
			} else {
				slog.Debug("Failed to get config file", "key", configKey, "err", err)
			}
			continue
		}

		body, err := io.ReadAll(getResult.Body)
		if err := getResult.Body.Close(); err != nil {
			slog.Debug("Failed to close config body", "key", configKey, "err", err)
		}
		if err != nil {
			slog.Debug("Failed to read config body", "key", configKey, "err", err)
			continue
		}

		// Parse the viperblock config which contains VolumeConfig with AMIMetadata
		var vbConfig struct {
			VolumeConfig viperblock.VolumeConfig `json:"VolumeConfig"`
		}
		if err := json.Unmarshal(body, &vbConfig); err != nil {
			slog.Debug("Failed to unmarshal config", "key", configKey, "err", err)
			continue
		}

		amiMeta := vbConfig.VolumeConfig.AMIMetadata

		// Skip if this is not an AMI (ImageID is empty)
		if amiMeta.ImageID == "" {
			continue
		}

		// Filter by ImageId if specified
		if len(input.ImageIds) > 0 {
			found := false
			for _, filterID := range input.ImageIds {
				if filterID != nil && *filterID == amiMeta.ImageID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Filter by Owner if specified (only support "self" and account IDs for now)
		if len(input.Owners) > 0 {
			found := false
			for _, owner := range input.Owners {
				if owner != nil && (*owner == "self" || *owner == s.accountID || *owner == amiMeta.ImageOwnerAlias) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Build EC2 Image from AMIMetadata
		image := &ec2.Image{
			ImageId:            aws.String(amiMeta.ImageID),
			Name:               aws.String(amiMeta.Name),
			Description:        aws.String(amiMeta.Description),
			Architecture:       aws.String(amiMeta.Architecture),
			PlatformDetails:    aws.String(amiMeta.PlatformDetails),
			CreationDate:       aws.String(amiMeta.CreationDate.Format("2006-01-02T15:04:05.000Z")),
			RootDeviceType:     aws.String(amiMeta.RootDeviceType),
			VirtualizationType: aws.String(amiMeta.Virtualization),
			ImageOwnerAlias:    aws.String(amiMeta.ImageOwnerAlias),
			OwnerId:            aws.String(s.accountID),
			Public:             aws.Bool(false),
			State:              aws.String("available"),
			ImageType:          aws.String("machine"),
			Hypervisor:         aws.String("xen"), // Default hypervisor
		}

		// Add root device mapping
		if amiMeta.RootDeviceType == "ebs" {
			image.RootDeviceName = aws.String("/dev/sda1")
			image.BlockDeviceMappings = []*ec2.BlockDeviceMapping{
				{
					DeviceName: aws.String("/dev/sda1"),
					Ebs: &ec2.EbsBlockDevice{
						VolumeSize:          aws.Int64(utils.SafeUint64ToInt64(amiMeta.VolumeSizeGiB)),
						VolumeType:          aws.String("gp3"),
						DeleteOnTermination: aws.Bool(true),
						Encrypted:           aws.Bool(false),
					},
				},
			}
		}

		// Add tags if available
		if len(amiMeta.Tags) > 0 {
			tags := make([]*ec2.Tag, 0, len(amiMeta.Tags))
			for key, value := range amiMeta.Tags {
				tags = append(tags, &ec2.Tag{
					Key:   aws.String(key),
					Value: aws.String(value),
				})
			}
			image.Tags = tags
		}

		images = append(images, image)
	}

	slog.Info("DescribeImages completed", "count", len(images))

	return &ec2.DescribeImagesOutput{
		Images: images,
	}, nil
}

// CreateImage is the generic interface method — on the daemon side, the handler
// calls CreateImageFromInstance directly with the extra instance context.
func (s *ImageServiceImpl) CreateImage(input *ec2.CreateImageInput) (*ec2.CreateImageOutput, error) {
	return nil, errors.New("CreateImage requires instance context; use CreateImageFromInstance on daemon side")
}

// CreateImageFromInstance creates an AMI from an instance by snapshotting the root
// volume and storing a new AMI config in S3.
func (s *ImageServiceImpl) CreateImageFromInstance(params CreateImageParams) (*ec2.CreateImageOutput, error) {
	input := params.Input
	if input == nil || input.InstanceId == nil {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	amiID := utils.GenerateResourceID("ami")
	snapshotID := utils.GenerateResourceID("snap")

	slog.Info("CreateImageFromInstance", "instanceId", *input.InstanceId,
		"rootVolumeId", params.RootVolumeID, "amiId", amiID, "snapshotId", snapshotID,
		"isRunning", params.IsRunning)

	// Step 1: Snapshot the root volume
	if params.IsRunning {
		if err := s.snapshotRunningVolume(params.RootVolumeID, snapshotID); err != nil {
			return nil, err
		}
	} else {
		if err := s.snapshotStoppedVolume(params.RootVolumeID, snapshotID); err != nil {
			return nil, err
		}
	}

	// Step 2: Read source volume config for size
	volumeConfig, err := s.getVolumeConfig(params.RootVolumeID)
	if err != nil {
		slog.Error("CreateImageFromInstance: failed to read volume config", "volumeId", params.RootVolumeID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	volumeSizeGiB := volumeConfig.VolumeMetadata.SizeGiB

	// Step 3: Read source AMI config for architecture, platform, etc.
	var sourceAMI viperblock.AMIMetadata
	if params.SourceImageID != "" {
		srcCfg, err := s.getAMIConfig(params.SourceImageID)
		if err != nil {
			slog.Error("CreateImageFromInstance: failed to read source AMI config", "sourceImageId", params.SourceImageID, "err", err)
			return nil, errors.New(awserrors.ErrorServerInternal)
		}
		sourceAMI = srcCfg
	} else {
		sourceAMI = viperblock.AMIMetadata{
			Architecture:    "x86_64",
			PlatformDetails: "Linux/UNIX",
			Virtualization:  "hvm",
		}
	}

	// Step 4: Store snapshot metadata
	if err := s.putSnapshotMetadata(snapshotID, params.RootVolumeID, volumeSizeGiB); err != nil {
		slog.Error("CreateImageFromInstance: failed to write snapshot metadata", "snapshotId", snapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Step 5: Build and store AMI config
	name := aws.StringValue(input.Name)
	description := aws.StringValue(input.Description)

	amiConfig := viperblock.VBState{
		VolumeConfig: viperblock.VolumeConfig{
			AMIMetadata: viperblock.AMIMetadata{
				ImageID:         amiID,
				Name:            name,
				Description:     description,
				SnapshotID:      snapshotID,
				Architecture:    sourceAMI.Architecture,
				PlatformDetails: sourceAMI.PlatformDetails,
				Virtualization:  sourceAMI.Virtualization,
				VolumeSizeGiB:   volumeSizeGiB,
				CreationDate:    time.Now(),
				RootDeviceType:  "ebs",
				ImageOwnerAlias: "self",
			},
		},
	}

	configData, err := json.Marshal(amiConfig)
	if err != nil {
		slog.Error("CreateImageFromInstance: failed to marshal AMI config", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	configKey := fmt.Sprintf("%s/config.json", amiID)
	_, err = s.store.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(configKey),
		Body:        bytes.NewReader(configData),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		slog.Error("CreateImageFromInstance: failed to store AMI config", "amiId", amiID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateImageFromInstance completed", "amiId", amiID, "snapshotId", snapshotID)

	return &ec2.CreateImageOutput{
		ImageId: aws.String(amiID),
	}, nil
}

// snapshotRunningVolume triggers a snapshot via NATS on a running viperblockd instance
func (s *ImageServiceImpl) snapshotRunningVolume(volumeID, snapshotID string) error {
	if s.natsConn == nil {
		return errors.New(awserrors.ErrorServerInternal)
	}

	snapReq := config.EBSSnapshotRequest{Volume: volumeID, SnapshotID: snapshotID}
	snapData, err := json.Marshal(snapReq)
	if err != nil {
		slog.Error("snapshotRunningVolume: failed to marshal snapshot request", "volumeId", volumeID, "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	msg, err := s.natsConn.Request(fmt.Sprintf("ebs.snapshot.%s", volumeID), snapData, 30*time.Second)
	if err != nil {
		slog.Error("snapshotRunningVolume: NATS request failed", "volumeId", volumeID, "snapshotId", snapshotID, "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	var snapResp config.EBSSnapshotResponse
	if err := json.Unmarshal(msg.Data, &snapResp); err != nil {
		slog.Error("snapshotRunningVolume: failed to unmarshal response", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}
	if !snapResp.Success || snapResp.Error != "" {
		slog.Error("snapshotRunningVolume: snapshot failed", "volumeId", volumeID, "err", snapResp.Error)
		return errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("snapshotRunningVolume: snapshot created", "volumeId", volumeID, "snapshotId", snapshotID)
	return nil
}

// snapshotStoppedVolume creates a snapshot offline by loading viperblock state from S3
func (s *ImageServiceImpl) snapshotStoppedVolume(volumeID, snapshotID string) error {
	if s.config == nil {
		return errors.New(awserrors.ErrorServerInternal)
	}

	cfg := vbs3.S3Config{
		VolumeName: volumeID,
		VolumeSize: 0, // will be loaded from state
		Bucket:     s.config.Predastore.Bucket,
		Region:     s.config.Predastore.Region,
		AccessKey:  s.config.Predastore.AccessKey,
		SecretKey:  s.config.Predastore.SecretKey,
		Host:       s.config.Predastore.Host,
	}

	vbconfig := viperblock.VB{
		VolumeName: volumeID,
		BaseDir:    s.config.WalDir,
		Cache:      viperblock.Cache{Config: viperblock.CacheConfig{Size: 0}},
	}

	vb, err := viperblock.New(&vbconfig, "s3", cfg)
	if err != nil {
		slog.Error("snapshotStoppedVolume: failed to create viperblock instance", "volumeId", volumeID, "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	if err := vb.Backend.Init(); err != nil {
		slog.Error("snapshotStoppedVolume: failed to init backend", "volumeId", volumeID, "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	if _, err := vb.LoadStateRequest(""); err != nil {
		slog.Error("snapshotStoppedVolume: failed to load state", "volumeId", volumeID, "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	if _, err := vb.CreateSnapshot(snapshotID); err != nil {
		slog.Error("snapshotStoppedVolume: CreateSnapshot failed", "volumeId", volumeID, "snapshotId", snapshotID, "err", err)
		if cleanupErr := vb.RemoveLocalFiles(); cleanupErr != nil {
			slog.Error("snapshotStoppedVolume: failed to remove local files after snapshot failure", "volumeId", volumeID, "err", cleanupErr)
		}
		return errors.New(awserrors.ErrorServerInternal)
	}

	if err := vb.RemoveLocalFiles(); err != nil {
		slog.Error("snapshotStoppedVolume: failed to remove local files", "volumeId", volumeID, "err", err)
	}

	slog.Info("snapshotStoppedVolume: snapshot created", "volumeId", volumeID, "snapshotId", snapshotID)
	return nil
}

// getVolumeConfig reads a volume's VBState config from S3
func (s *ImageServiceImpl) getVolumeConfig(volumeID string) (*viperblock.VolumeConfig, error) {
	configKey := fmt.Sprintf("%s/config.json", volumeID)
	result, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(configKey),
	})
	if err != nil {
		return nil, err
	}
	defer result.Body.Close()

	var vbState viperblock.VBState
	if err := json.NewDecoder(result.Body).Decode(&vbState); err != nil {
		return nil, err
	}
	return &vbState.VolumeConfig, nil
}

// getAMIConfig reads an AMI's metadata from S3
func (s *ImageServiceImpl) getAMIConfig(imageID string) (viperblock.AMIMetadata, error) {
	configKey := fmt.Sprintf("%s/config.json", imageID)
	result, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(configKey),
	})
	if err != nil {
		return viperblock.AMIMetadata{}, err
	}
	defer result.Body.Close()

	var vbState viperblock.VBState
	if err := json.NewDecoder(result.Body).Decode(&vbState); err != nil {
		return viperblock.AMIMetadata{}, err
	}
	return vbState.VolumeConfig.AMIMetadata, nil
}

// putSnapshotMetadata stores snapshot metadata in S3 using the canonical SnapshotConfig type
func (s *ImageServiceImpl) putSnapshotMetadata(snapshotID, volumeID string, volumeSizeGiB uint64) error {
	cfg := handlers_ec2_snapshot.SnapshotConfig{
		SnapshotID: snapshotID,
		VolumeID:   volumeID,
		VolumeSize: utils.SafeUint64ToInt64(volumeSizeGiB),
		State:      "completed",
		Progress:   "100%",
		StartTime:  time.Now(),
		OwnerID:    s.accountID,
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%s/metadata.json", snapshotID)
	_, err = s.store.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	return err
}

func (s *ImageServiceImpl) CopyImage(input *ec2.CopyImageInput) (*ec2.CopyImageOutput, error) {
	return nil, errors.New("CopyImage not yet implemented")
}

func (s *ImageServiceImpl) DescribeImageAttribute(input *ec2.DescribeImageAttributeInput) (*ec2.DescribeImageAttributeOutput, error) {
	return nil, errors.New("DescribeImageAttribute not yet implemented")
}

func (s *ImageServiceImpl) RegisterImage(input *ec2.RegisterImageInput) (*ec2.RegisterImageOutput, error) {
	return nil, errors.New("RegisterImage not yet implemented")
}

func (s *ImageServiceImpl) DeregisterImage(input *ec2.DeregisterImageInput) (*ec2.DeregisterImageOutput, error) {
	return nil, errors.New("DeregisterImage not yet implemented")
}

func (s *ImageServiceImpl) ModifyImageAttribute(input *ec2.ModifyImageAttributeInput) (*ec2.ModifyImageAttributeOutput, error) {
	return nil, errors.New("ModifyImageAttribute not yet implemented")
}

func (s *ImageServiceImpl) ResetImageAttribute(input *ec2.ResetImageAttributeInput) (*ec2.ResetImageAttributeOutput, error) {
	return nil, errors.New("ResetImageAttribute not yet implemented")
}
