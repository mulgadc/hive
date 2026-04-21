package handlers_ec2_image

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/config"
	"github.com/mulgadc/spinifex/spinifex/filterutil"
	handlers_ec2_snapshot "github.com/mulgadc/spinifex/spinifex/handlers/ec2/snapshot"
	"github.com/mulgadc/spinifex/spinifex/objectstore"
	"github.com/mulgadc/spinifex/spinifex/types"
	"github.com/mulgadc/spinifex/spinifex/utils"
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
	}
}

// NewImageServiceImplWithStore creates an image service with a custom object store (for testing)
func NewImageServiceImplWithStore(store objectstore.ObjectStore, bucketName string) *ImageServiceImpl {
	return &ImageServiceImpl{
		store:      store,
		bucketName: bucketName,
	}
}

// describeImagesValidFilters defines the set of filter names accepted by DescribeImages.
var describeImagesValidFilters = map[string]bool{
	"name":         true,
	"state":        true,
	"architecture": true,
	"image-id":     true,
	"is-public":    true,
	"owner-id":     true,
	"description":  true,
	"image-type":   true,
}

// DescribeImages lists available AMI images by reading config.json files from S3
func (s *ImageServiceImpl) DescribeImages(input *ec2.DescribeImagesInput, accountID string) (*ec2.DescribeImagesOutput, error) {
	if input == nil {
		input = &ec2.DescribeImagesInput{}
	}

	slog.Info("Describing images", "filters", input.Filters, "imageIds", input.ImageIds)

	parsedFilters, err := filterutil.ParseFilters(input.Filters, describeImagesValidFilters)
	if err != nil {
		slog.Warn("DescribeImages: invalid filter", "err", err)
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	// List all prefixes in the bucket (AMIs are stored as ami-<id>/ directories)
	result, err := s.store.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket:    aws.String(s.bucketName),
		Delimiter: aws.String("/"),
	})

	if err != nil {
		slog.Error("Failed to list S3 objects", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Extract image-id filter values for early prefix skipping to avoid
	// unnecessary S3 GetObject calls on non-matching AMIs.
	var imageIDFilterValues []string
	if parsedFilters != nil {
		imageIDFilterValues = parsedFilters["image-id"]
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

		// Early skip: if image-id filter is set, check the prefix (ami-xxx/)
		// against filter values before fetching config from S3.
		if len(imageIDFilterValues) > 0 {
			amiID := strings.TrimSuffix(prefixStr, "/")
			if !filterutil.MatchesAny(imageIDFilterValues, amiID) {
				continue
			}
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

		// Determine AMI ownership. Phase4+ AMIs store the creator's account ID
		// in ImageOwnerAlias. Pre-phase4 AMIs have non-account values like "self"
		// or "spinifex" and are treated as system/public images visible to all.
		amiOwner := amiMeta.ImageOwnerAlias
		isSystemAMI := !utils.IsAccountID(amiOwner)

		// Visibility check: callers can only see their own AMIs and system AMIs.
		// This runs regardless of whether an owner filter is specified.
		if !isSystemAMI && amiOwner != accountID {
			continue
		}

		// Filter by Owner if specified
		if len(input.Owners) > 0 {
			found := false
			for _, owner := range input.Owners {
				if owner == nil {
					continue
				}
				switch *owner {
				case "self":
					// "self" matches only AMIs owned by the caller
					if amiOwner == accountID {
						found = true
					}
				default:
					// Match by explicit account ID. System AMIs are stored
					// with a non-account owner (e.g. "spinifex") but report
					// GlobalAccountID in the response, so match against both.
					if amiOwner == *owner {
						found = true
					} else if isSystemAMI && *owner == utils.GlobalAccountID {
						found = true
					}
				}
				if found {
					break
				}
			}
			if !found {
				continue
			}
		}

		// Resolve the OwnerId for the response. System AMIs use global account.
		ownerID := amiOwner
		if isSystemAMI {
			ownerID = utils.GlobalAccountID
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
			OwnerId:            aws.String(ownerID),
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

		// Apply filters against the fully-built image
		if len(parsedFilters) > 0 && !imageMatchesFilters(image, parsedFilters, amiMeta.Tags) {
			continue
		}

		images = append(images, image)
	}

	// If specific ImageIds were requested, verify all were found
	if len(input.ImageIds) > 0 {
		foundIDs := make(map[string]bool, len(images))
		for _, img := range images {
			if img.ImageId != nil {
				foundIDs[*img.ImageId] = true
			}
		}
		for _, reqID := range input.ImageIds {
			if reqID != nil && !foundIDs[*reqID] {
				return nil, errors.New(awserrors.ErrorInvalidAMIIDNotFound)
			}
		}
	}

	slog.Info("DescribeImages completed", "count", len(images))

	return &ec2.DescribeImagesOutput{
		Images: images,
	}, nil
}

// imageMatchesFilters checks whether an ec2.Image satisfies all parsed filters.
func imageMatchesFilters(image *ec2.Image, filters map[string][]string, tags map[string]string) bool {
	for name, values := range filters {
		if strings.HasPrefix(name, "tag:") {
			continue
		}

		var field string
		switch name {
		case "name":
			if image.Name != nil {
				field = *image.Name
			}
		case "state":
			if image.State != nil {
				field = *image.State
			}
		case "architecture":
			if image.Architecture != nil {
				field = *image.Architecture
			}
		case "image-id":
			if image.ImageId != nil {
				field = *image.ImageId
			}
		case "is-public":
			if image.Public != nil {
				field = strconv.FormatBool(*image.Public)
			}
		case "owner-id":
			if image.OwnerId != nil {
				field = *image.OwnerId
			}
		case "description":
			if image.Description != nil {
				field = *image.Description
			}
		case "image-type":
			if image.ImageType != nil {
				field = *image.ImageType
			}
		default:
			return false
		}

		if !filterutil.MatchesAny(values, field) {
			return false
		}
	}

	return filterutil.MatchesTags(filters, tags)
}

// CreateImage is the generic interface method — on the daemon side, the handler
// calls CreateImageFromInstance directly with the extra instance context.
func (s *ImageServiceImpl) CreateImage(input *ec2.CreateImageInput, accountID string) (*ec2.CreateImageOutput, error) {
	return nil, errors.New("CreateImage requires instance context; use CreateImageFromInstance on daemon side")
}

// CreateImageFromInstance creates an AMI from an instance by snapshotting the root
// volume and storing a new AMI config in S3.
func (s *ImageServiceImpl) CreateImageFromInstance(params CreateImageParams, accountID string) (*ec2.CreateImageOutput, error) {
	input := params.Input
	if input == nil || input.InstanceId == nil {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	// Check for duplicate AMI name before doing any expensive work
	name := aws.StringValue(input.Name)
	if name != "" {
		if exists, err := s.amiNameExists(name); err != nil {
			slog.Error("CreateImageFromInstance: failed to check AMI name uniqueness", "name", name, "err", err)
			return nil, errors.New(awserrors.ErrorServerInternal)
		} else if exists {
			return nil, errors.New(awserrors.ErrorInvalidAMINameDuplicate)
		}
	}

	amiID := utils.GenerateResourceID("ami")
	snapshotID := utils.GenerateResourceID("snap")

	slog.Info("CreateImageFromInstance", "instanceId", *input.InstanceId,
		"rootVolumeId", params.RootVolumeID, "amiId", amiID, "snapshotId", snapshotID,
		"isRunning", params.IsRunning)

	// Step 1: Snapshot root volume (live via NATS or offline from S3)
	snapshotFn := s.snapshotStoppedVolume
	if params.IsRunning {
		snapshotFn = s.snapshotRunningVolume
	}
	if err := snapshotFn(params.RootVolumeID, snapshotID); err != nil {
		return nil, err
	}

	// Step 2: Read source volume config for size
	volumeConfig, err := s.getVolumeConfig(params.RootVolumeID)
	if err != nil {
		slog.Error("CreateImageFromInstance: failed to read volume config", "volumeId", params.RootVolumeID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	volumeSizeGiB := volumeConfig.VolumeMetadata.SizeGiB

	// Step 3: Read source AMI config for architecture, platform, etc.
	sourceAMI := viperblock.AMIMetadata{
		Architecture:    "x86_64",
		PlatformDetails: "Linux/UNIX",
		Virtualization:  "hvm",
	}
	if params.SourceImageID != "" {
		srcCfg, err := s.GetAMIConfig(params.SourceImageID)
		if err != nil {
			slog.Error("CreateImageFromInstance: failed to read source AMI config", "sourceImageId", params.SourceImageID, "err", err)
			return nil, errors.New(awserrors.ErrorServerInternal)
		}
		sourceAMI = srcCfg
	}

	// Step 4: Store snapshot metadata
	if err := s.putSnapshotMetadata(snapshotID, params.RootVolumeID, volumeSizeGiB, accountID); err != nil {
		slog.Error("CreateImageFromInstance: failed to write snapshot metadata", "snapshotId", snapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Step 5: Build and store AMI config
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
				ImageOwnerAlias: accountID,
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

	snapReq := types.EBSSnapshotRequest{Volume: volumeID, SnapshotID: snapshotID}
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

	var snapResp types.EBSSnapshotResponse
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

	// Read volume config to get the size (required by viperblock.New)
	volConfig, err := s.getVolumeConfig(volumeID)
	if err != nil {
		slog.Error("snapshotStoppedVolume: failed to read volume config", "volumeId", volumeID, "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}
	volumeSize := volConfig.VolumeMetadata.SizeGiB * 1024 * 1024 * 1024

	cfg := vbs3.S3Config{
		VolumeName: volumeID,
		VolumeSize: volumeSize,
		Bucket:     s.config.Predastore.Bucket,
		Region:     s.config.Predastore.Region,
		AccessKey:  s.config.Predastore.AccessKey,
		SecretKey:  s.config.Predastore.SecretKey,
		Host:       s.config.Predastore.Host,
	}

	vbconfig := viperblock.VB{
		VolumeName: volumeID,
		VolumeSize: volumeSize,
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

	// Clean up local WAL files regardless of snapshot success or failure
	defer func() {
		if err := vb.RemoveLocalFiles(); err != nil {
			slog.Error("snapshotStoppedVolume: failed to remove local files", "volumeId", volumeID, "err", err)
		}
	}()

	if _, err := vb.CreateSnapshot(snapshotID); err != nil {
		slog.Error("snapshotStoppedVolume: CreateSnapshot failed", "volumeId", volumeID, "snapshotId", snapshotID, "err", err)
		return errors.New(awserrors.ErrorServerInternal)
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

// amiNameExists checks if any existing AMI already uses the given name.
func (s *ImageServiceImpl) amiNameExists(name string) (bool, error) {
	listResult, err := s.store.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket:    aws.String(s.bucketName),
		Prefix:    aws.String("ami-"),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		return false, fmt.Errorf("amiNameExists: failed to list AMIs: %w", err)
	}

	for _, prefix := range listResult.CommonPrefixes {
		if prefix.Prefix == nil {
			continue
		}
		configKey := *prefix.Prefix + "config.json"
		result, err := s.store.GetObject(&s3.GetObjectInput{
			Bucket: aws.String(s.bucketName),
			Key:    aws.String(configKey),
		})
		if err != nil {
			continue
		}

		var vbState viperblock.VBState
		decodeErr := json.NewDecoder(result.Body).Decode(&vbState)
		_ = result.Body.Close()
		if decodeErr != nil {
			continue
		}

		if vbState.VolumeConfig.AMIMetadata.Name == name {
			return true, nil
		}
	}

	return false, nil
}

// GetAMIConfig retrieves the AMI metadata for a given image ID from S3.
// Returns NoSuchKeyError if the AMI does not exist.
func (s *ImageServiceImpl) GetAMIConfig(imageID string) (viperblock.AMIMetadata, error) {
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

// putAMIConfig writes AMI metadata to s3://{bucket}/{imageID}/config.json,
// preserving the same VBState wrapper used by GetAMIConfig.
func (s *ImageServiceImpl) putAMIConfig(imageID string, meta viperblock.AMIMetadata) error {
	state := viperblock.VBState{
		VolumeConfig: viperblock.VolumeConfig{
			AMIMetadata: meta,
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	configKey := fmt.Sprintf("%s/config.json", imageID)
	_, err = s.store.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(configKey),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	return err
}

// checkAMIOwnership returns ErrorUnauthorizedOperation if accountID cannot
// mutate the AMI. System AMIs (non-account owner) are immutable via this
// API regardless of caller.
func (s *ImageServiceImpl) checkAMIOwnership(meta viperblock.AMIMetadata, accountID string) error {
	if !utils.IsAccountID(meta.ImageOwnerAlias) || meta.ImageOwnerAlias != accountID {
		return errors.New(awserrors.ErrorUnauthorizedOperation)
	}
	return nil
}

// loadAMIForMutation validates the ID format, fetches the config, converts
// NoSuchKey to InvalidAMIID.NotFound, and runs the ownership check. Used by
// deregister, modify, and reset paths where cross-account callers must see
// UnauthorizedOperation rather than NotFound (the caller already knows the ID).
func (s *ImageServiceImpl) loadAMIForMutation(imageID, accountID string) (viperblock.AMIMetadata, error) {
	if !strings.HasPrefix(imageID, "ami-") {
		return viperblock.AMIMetadata{}, errors.New(awserrors.ErrorInvalidAMIIDMalformed)
	}

	meta, err := s.GetAMIConfig(imageID)
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) {
			return viperblock.AMIMetadata{}, errors.New(awserrors.ErrorInvalidAMIIDNotFound)
		}
		slog.Error("loadAMIForMutation: failed to read AMI config", "imageId", imageID, "err", err)
		return viperblock.AMIMetadata{}, errors.New(awserrors.ErrorServerInternal)
	}

	if err := s.checkAMIOwnership(meta, accountID); err != nil {
		return viperblock.AMIMetadata{}, err
	}
	return meta, nil
}

// putSnapshotMetadata stores snapshot metadata in S3 using the canonical SnapshotConfig type
func (s *ImageServiceImpl) putSnapshotMetadata(snapshotID, volumeID string, volumeSizeGiB uint64, accountID string) error {
	cfg := handlers_ec2_snapshot.SnapshotConfig{
		SnapshotID: snapshotID,
		VolumeID:   volumeID,
		VolumeSize: utils.SafeUint64ToInt64(volumeSizeGiB),
		State:      "completed",
		Progress:   "100%",
		StartTime:  time.Now(),
		OwnerID:    accountID,
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

func (s *ImageServiceImpl) CopyImage(input *ec2.CopyImageInput, accountID string) (*ec2.CopyImageOutput, error) {
	return nil, errors.New("CopyImage not yet implemented")
}

func (s *ImageServiceImpl) DescribeImageAttribute(input *ec2.DescribeImageAttributeInput, accountID string) (*ec2.DescribeImageAttributeOutput, error) {
	return nil, errors.New("DescribeImageAttribute not yet implemented")
}

// RegisterImage creates an AMI metadata record that points at an existing
// snapshot. It is pointer-only: it never moves, copies, or writes block data.
// Validation done in the gateway layer guarantees the input shape; here we
// only enforce semantic checks (name uniqueness, snapshot existence/ownership,
// volume sizing).
func (s *ImageServiceImpl) RegisterImage(input *ec2.RegisterImageInput, accountID string) (*ec2.RegisterImageOutput, error) {
	if input == nil || input.Name == nil || *input.Name == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	name := *input.Name

	if exists, err := s.amiNameExists(name); err != nil {
		slog.Error("RegisterImage: failed to check AMI name uniqueness", "name", name, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	} else if exists {
		return nil, errors.New(awserrors.ErrorInvalidAMINameDuplicate)
	}

	rootBDM := pickRootSnapshotBDM(input.BlockDeviceMappings, input.RootDeviceName)
	if rootBDM == nil {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	snapshotID := *rootBDM.Ebs.SnapshotId

	snapCfg, err := s.getSnapshotMetadata(snapshotID)
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) {
			return nil, errors.New(awserrors.ErrorInvalidSnapshotNotFound)
		}
		slog.Error("RegisterImage: failed to read snapshot metadata", "snapshotId", snapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Snapshot ownership: callers can only register from their own snapshots
	// or from system snapshots (non-account-ID owner, mirroring system AMIs).
	if utils.IsAccountID(snapCfg.OwnerID) && snapCfg.OwnerID != accountID {
		return nil, errors.New(awserrors.ErrorUnauthorizedOperation)
	}

	snapSizeGiB := uint64(0)
	if snapCfg.VolumeSize > 0 {
		snapSizeGiB = uint64(snapCfg.VolumeSize)
	}

	volumeSizeGiB := snapSizeGiB
	if rootBDM.Ebs.VolumeSize != nil && *rootBDM.Ebs.VolumeSize > 0 {
		requested := uint64(*rootBDM.Ebs.VolumeSize)
		if requested < snapSizeGiB {
			return nil, errors.New(awserrors.ErrorInvalidParameterValue)
		}
		volumeSizeGiB = requested
	}

	architecture := "x86_64"
	if input.Architecture != nil && *input.Architecture != "" {
		architecture = *input.Architecture
	}
	virtualization := "hvm"
	if input.VirtualizationType != nil && *input.VirtualizationType != "" {
		virtualization = *input.VirtualizationType
	}
	description := ""
	if input.Description != nil {
		description = *input.Description
	}

	tags := tagsFromImageSpecifications(input.TagSpecifications)

	amiID := utils.GenerateResourceID("ami")
	meta := viperblock.AMIMetadata{
		ImageID:         amiID,
		Name:            name,
		Description:     description,
		SnapshotID:      snapshotID,
		Architecture:    architecture,
		PlatformDetails: "Linux/UNIX",
		Virtualization:  virtualization,
		VolumeSizeGiB:   volumeSizeGiB,
		RootDeviceType:  "ebs",
		ImageOwnerAlias: accountID,
		CreationDate:    time.Now(),
		Tags:            tags,
	}

	if err := s.putAMIConfig(amiID, meta); err != nil {
		slog.Error("RegisterImage: failed to write AMI config", "amiId", amiID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("RegisterImage completed", "amiId", amiID, "snapshotId", snapshotID, "accountId", accountID)
	return &ec2.RegisterImageOutput{ImageId: aws.String(amiID)}, nil
}

// pickRootSnapshotBDM finds the BDM that backs the root volume and carries an
// EBS snapshot reference. When RootDeviceName is set, only the matching device
// counts; otherwise the first BDM with a snapshot wins. Returns nil when no
// suitable entry exists — mirroring the gateway's validation but applied again
// at the service boundary so direct callers (tests, future internal users)
// don't bypass the check.
func pickRootSnapshotBDM(mappings []*ec2.BlockDeviceMapping, rootDeviceName *string) *ec2.BlockDeviceMapping {
	wantName := ""
	if rootDeviceName != nil {
		wantName = *rootDeviceName
	}

	for _, bdm := range mappings {
		if bdm == nil || bdm.Ebs == nil || bdm.Ebs.SnapshotId == nil || *bdm.Ebs.SnapshotId == "" {
			continue
		}
		if wantName != "" {
			if bdm.DeviceName == nil || *bdm.DeviceName != wantName {
				continue
			}
		}
		return bdm
	}
	return nil
}

// tagsFromImageSpecifications flattens TagSpecifications entries with
// ResourceType=="image" into a key→value map. Non-image specifications and
// nil/empty entries are skipped.
func tagsFromImageSpecifications(specs []*ec2.TagSpecification) map[string]string {
	if len(specs) == 0 {
		return nil
	}
	tags := make(map[string]string)
	for _, spec := range specs {
		if spec == nil || spec.ResourceType == nil || *spec.ResourceType != "image" {
			continue
		}
		for _, tag := range spec.Tags {
			if tag == nil || tag.Key == nil {
				continue
			}
			value := ""
			if tag.Value != nil {
				value = *tag.Value
			}
			tags[*tag.Key] = value
		}
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}

// getSnapshotMetadata reads the SnapshotConfig stored at {snapshotId}/metadata.json.
// Returns the underlying object-store error (preserving NoSuchKey detectability)
// so callers can map it to AWS-specific errors as appropriate.
func (s *ImageServiceImpl) getSnapshotMetadata(snapshotID string) (*handlers_ec2_snapshot.SnapshotConfig, error) {
	key := fmt.Sprintf("%s/metadata.json", snapshotID)
	result, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer result.Body.Close()

	var cfg handlers_ec2_snapshot.SnapshotConfig
	if err := json.NewDecoder(result.Body).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// DeregisterImage hard-deletes the AMI's config.json. The backing snapshot is
// untouched (matches AWS: deregister does not delete EBS snapshots). Operators
// must run delete-snapshot separately to reclaim block storage.
func (s *ImageServiceImpl) DeregisterImage(input *ec2.DeregisterImageInput, accountID string) (*ec2.DeregisterImageOutput, error) {
	if input == nil || input.ImageId == nil || *input.ImageId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	imageID := *input.ImageId

	if _, err := s.loadAMIForMutation(imageID, accountID); err != nil {
		return nil, err
	}

	configKey := fmt.Sprintf("%s/config.json", imageID)
	if _, err := s.store.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(configKey),
	}); err != nil {
		slog.Error("DeregisterImage: failed to delete AMI config", "imageId", imageID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeregisterImage completed", "imageId", imageID, "accountId", accountID)
	return &ec2.DeregisterImageOutput{}, nil
}

func (s *ImageServiceImpl) ModifyImageAttribute(input *ec2.ModifyImageAttributeInput, accountID string) (*ec2.ModifyImageAttributeOutput, error) {
	return nil, errors.New("ModifyImageAttribute not yet implemented")
}

func (s *ImageServiceImpl) ResetImageAttribute(input *ec2.ResetImageAttributeInput, accountID string) (*ec2.ResetImageAttributeOutput, error) {
	return nil, errors.New("ResetImageAttribute not yet implemented")
}
