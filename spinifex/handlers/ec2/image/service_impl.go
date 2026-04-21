package handlers_ec2_image

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
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
		isSystemAMI := amiOwner != "" && !utils.IsAccountID(amiOwner)

		// Visibility check: callers see their own AMIs and system AMIs only.
		// Empty-owner (corrupt) configs are filtered out here too.
		if !callerCanReadAMI(amiMeta, accountID) {
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
	meta := viperblock.AMIMetadata{
		ImageID:         amiID,
		Name:            name,
		Description:     aws.StringValue(input.Description),
		SnapshotID:      snapshotID,
		Architecture:    sourceAMI.Architecture,
		PlatformDetails: sourceAMI.PlatformDetails,
		Virtualization:  sourceAMI.Virtualization,
		VolumeSizeGiB:   volumeSizeGiB,
		CreationDate:    time.Now(),
		RootDeviceType:  ec2.DeviceTypeEbs,
		ImageOwnerAlias: accountID,
	}

	if err := s.putAMIConfig(amiID, meta); err != nil {
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
// NoSuchKey on a per-AMI config.json is treated as "not present" and skipped
// (a concurrent deregister can race with us). Any other GetObject failure or
// JSON decode error is surfaced — we'd rather return ServerInternal than
// silently under-count names and allow a duplicate.
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
			if objectstore.IsNoSuchKeyError(err) {
				continue
			}
			return false, fmt.Errorf("amiNameExists: failed to read %s: %w", configKey, err)
		}

		var vbState viperblock.VBState
		decodeErr := json.NewDecoder(result.Body).Decode(&vbState)
		_ = result.Body.Close()
		if decodeErr != nil {
			return false, fmt.Errorf("amiNameExists: failed to decode %s: %w", configKey, decodeErr)
		}

		if vbState.VolumeConfig.AMIMetadata.Name == name {
			return true, nil
		}
	}

	return false, nil
}

// ErrCorruptAMIConfig wraps JSON decode failures on AMI config.json so callers
// can distinguish a truly-missing AMI from one whose config.json exists but
// can't be parsed. Use errors.Is to detect. Mirrors ErrCorruptSnapshotMetadata.
var ErrCorruptAMIConfig = errors.New("corrupt AMI config")

// GetAMIConfig retrieves the AMI metadata for a given image ID from S3.
// Returns NoSuchKeyError if the AMI does not exist; wraps ErrCorruptAMIConfig
// on decode failure with the config key in the error message.
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
		return viperblock.AMIMetadata{}, fmt.Errorf("%w: %s: %v", ErrCorruptAMIConfig, configKey, err)
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
// mutate the AMI. System AMIs (non-account owner) are immutable via this API
// regardless of caller. An empty owner indicates a corrupt config — surface
// that as ServerInternal so operators see the real failure, not a misleading
// 403.
func (s *ImageServiceImpl) checkAMIOwnership(meta viperblock.AMIMetadata, accountID string) error {
	owner := meta.ImageOwnerAlias
	if owner == "" {
		slog.Error("checkAMIOwnership: AMI config has empty ImageOwnerAlias", "imageId", meta.ImageID)
		return errors.New(awserrors.ErrorServerInternal)
	}
	if !utils.IsAccountID(owner) || owner != accountID {
		return errors.New(awserrors.ErrorUnauthorizedOperation)
	}
	return nil
}

// callerCanReadAMI returns true when accountID is allowed to see the AMI.
// Empty owner is treated as invisible (corrupt state should not leak). A
// non-account-ID owner ("spinifex", "system", …) is a system AMI, visible
// to everyone; an account-ID owner is visible only to that account.
func callerCanReadAMI(meta viperblock.AMIMetadata, accountID string) bool {
	owner := meta.ImageOwnerAlias
	if owner == "" {
		return false
	}
	if !utils.IsAccountID(owner) {
		return true
	}
	return owner == accountID
}

// loadAMIForMutation validates the ID format, fetches the config, converts
// NoSuchKey to InvalidAMIID.NotFound, and runs the ownership check. Used by
// deregister, modify, and reset paths where cross-account callers must see
// UnauthorizedOperation rather than NotFound (the caller already knows the ID).
//
// TOCTOU: the returned metadata reflects a read-at-this-instant view. Callers
// that subsequently PutObject (Modify/Reset) or DeleteObject (Deregister) race
// against concurrent mutators on the same imageID — the underlying Predastore
// PUT/DELETE has no If-Match/CAS plumbing here, so two concurrent modifies on
// different fields of the same AMI will last-write-wins on the whole struct.
// Acceptable at current single-operator scale; revisit when multi-writer
// workflows emerge.
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
	return handlers_ec2_snapshot.WriteSnapshotConfig(s.store, s.bucketName, snapshotID, &cfg)
}

// CopyImage clones an AMI into a new one owned by the caller. Same-region,
// metadata-only: the new snapshot shares the source's VolumeID (no block
// copy), and a fresh `ami-xxx/config.json` points at it. Mirrors the narrow
// scope. cross-region / encryption / Outposts are rejected at the gateway before we
// get here.
//
// Source-existence and visibility are checked before the O(n) name-uniqueness
// scan so common fast-fail cases (typo'd source, cross-account source) return
// without reading every AMI config.
func (s *ImageServiceImpl) CopyImage(input *ec2.CopyImageInput, accountID string) (*ec2.CopyImageOutput, error) {
	if input == nil || input.Name == nil || *input.Name == "" ||
		input.SourceImageId == nil || *input.SourceImageId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	name := *input.Name
	sourceImageID := *input.SourceImageId

	srcMeta, err := s.GetAMIConfig(sourceImageID)
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) || errors.Is(err, ErrCorruptAMIConfig) {
			// Corrupt source config is treated as if the AMI doesn't exist —
			// consistent with how corrupt source snapshot metadata is handled
			// below, so callers can't tell which half of the pair is broken.
			return nil, errors.New(awserrors.ErrorInvalidAMIIDNotFound)
		}
		slog.Error("CopyImage: failed to read source AMI config", "sourceImageId", sourceImageID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Visibility check: callers see their own AMIs or system AMIs. Anything
	// else hides existence by returning NotFound, mirroring DescribeImages.
	if !callerCanReadAMI(srcMeta, accountID) {
		return nil, errors.New(awserrors.ErrorInvalidAMIIDNotFound)
	}

	// Orphaned source: either the AMI doesn't record a SnapshotID (admin-
	// imported bundled-storage AMIs are not copyable by this API), the
	// snapshot is gone, or its metadata is corrupt. Either way we treat it
	// as if the AMI were missing — don't leak the half-broken state.
	if srcMeta.SnapshotID == "" {
		return nil, errors.New(awserrors.ErrorInvalidAMIIDNotFound)
	}
	srcSnap, err := handlers_ec2_snapshot.ReadSnapshotConfig(s.store, s.bucketName, srcMeta.SnapshotID)
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) || errors.Is(err, handlers_ec2_snapshot.ErrCorruptSnapshotMetadata) {
			return nil, errors.New(awserrors.ErrorInvalidAMIIDNotFound)
		}
		slog.Error("CopyImage: failed to read source snapshot metadata",
			"sourceImageId", sourceImageID, "snapshotId", srcMeta.SnapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	if exists, err := s.amiNameExists(name); err != nil {
		slog.Error("CopyImage: failed to check AMI name uniqueness", "name", name, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	} else if exists {
		return nil, errors.New(awserrors.ErrorInvalidAMINameDuplicate)
	}

	newSnapshotID := utils.GenerateResourceID("snap")
	newImageID := utils.GenerateResourceID("ami")

	// New snap inherits source VolumeID — blocks are shared, no copy runs.
	snapSizeGiB := uint64(0)
	if srcSnap.VolumeSize > 0 {
		snapSizeGiB = uint64(srcSnap.VolumeSize)
	}
	if err := s.putSnapshotMetadata(newSnapshotID, srcSnap.VolumeID, snapSizeGiB, accountID); err != nil {
		slog.Error("CopyImage: failed to write snapshot metadata", "snapshotId", newSnapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	description := srcMeta.Description
	if input.Description != nil {
		description = *input.Description
	}

	rootDeviceType := srcMeta.RootDeviceType
	if rootDeviceType == "" {
		rootDeviceType = "ebs"
	}

	tags := mergeCopyImageTags(srcMeta.Tags, input.TagSpecifications, aws.BoolValue(input.CopyImageTags))

	meta := viperblock.AMIMetadata{
		ImageID:         newImageID,
		Name:            name,
		Description:     description,
		SnapshotID:      newSnapshotID,
		Architecture:    srcMeta.Architecture,
		PlatformDetails: srcMeta.PlatformDetails,
		Virtualization:  srcMeta.Virtualization,
		VolumeSizeGiB:   srcMeta.VolumeSizeGiB,
		RootDeviceType:  rootDeviceType,
		ImageOwnerAlias: accountID,
		CreationDate:    time.Now(),
		Tags:            tags,
	}

	if err := s.putAMIConfig(newImageID, meta); err != nil {
		slog.Error("CopyImage: failed to write AMI config",
			"amiId", newImageID, "orphanSnapshotId", newSnapshotID, "err", err)
		// The new snapshot metadata we wrote above is now orphaned (no AMI
		// refers to it). Best-effort delete so it doesn't linger. If this
		// cleanup itself fails, log so operators can find the orphan.
		snapKey := handlers_ec2_snapshot.GetSnapshotKey(newSnapshotID)
		if _, delErr := s.store.DeleteObject(&s3.DeleteObjectInput{
			Bucket: aws.String(s.bucketName),
			Key:    aws.String(snapKey),
		}); delErr != nil {
			slog.Error("CopyImage: failed to roll back orphaned snapshot metadata",
				"snapshotId", newSnapshotID, "err", delErr)
		}
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CopyImage completed",
		"sourceImageId", sourceImageID, "newImageId", newImageID,
		"sourceSnapshotId", srcMeta.SnapshotID, "newSnapshotId", newSnapshotID,
		"accountId", accountID)

	return &ec2.CopyImageOutput{ImageId: aws.String(newImageID)}, nil
}

// mergeCopyImageTags returns the tag map for the copied AMI. When CopyImageTags
// is true the source's tags seed the result; explicit image-resource tags in
// TagSpecifications override any colliding source keys. Non-image tag specs
// are ignored (matches RegisterImage's behaviour).
func mergeCopyImageTags(srcTags map[string]string, specs []*ec2.TagSpecification, copyImageTags bool) map[string]string {
	merged := make(map[string]string)
	if copyImageTags {
		maps.Copy(merged, srcTags)
	}
	maps.Copy(merged, utils.ExtractTags(specs, "image"))
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// DescribeImageAttribute returns a single AMI attribute. Only description and
// blockDeviceMapping are supported; the gateway validator should reject the
// rest, but we re-check here so direct callers (tests, future internal users)
// can't bypass the allowlist.
//
// Cross-account / orphan reads are hidden as InvalidAMIID.NotFound, mirroring
// DescribeImages' silent cross-account filter — the caller shouldn't learn
// that the ID exists in another account.
func (s *ImageServiceImpl) DescribeImageAttribute(input *ec2.DescribeImageAttributeInput, accountID string) (*ec2.DescribeImageAttributeOutput, error) {
	if input == nil || input.ImageId == nil || *input.ImageId == "" ||
		input.Attribute == nil || *input.Attribute == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	imageID := *input.ImageId
	attribute := *input.Attribute

	meta, err := s.GetAMIConfig(imageID)
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) {
			return nil, errors.New(awserrors.ErrorInvalidAMIIDNotFound)
		}
		slog.Error("DescribeImageAttribute: failed to read AMI config", "imageId", imageID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Visibility check matches DescribeImages: caller owns it OR it's a
	// system AMI. Cross-account reads hide existence.
	if !callerCanReadAMI(meta, accountID) {
		return nil, errors.New(awserrors.ErrorInvalidAMIIDNotFound)
	}

	output := &ec2.DescribeImageAttributeOutput{
		ImageId: aws.String(imageID),
	}

	switch attribute {
	case ec2.ImageAttributeNameDescription:
		output.Description = &ec2.AttributeValue{Value: aws.String(meta.Description)}
	case ec2.ImageAttributeNameBlockDeviceMapping:
		output.BlockDeviceMappings = synthesizeRootBlockDeviceMapping(meta)
	default:
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	slog.Info("DescribeImageAttribute completed", "imageId", imageID, "attribute", attribute, "accountId", accountID)
	return output, nil
}

// synthesizeRootBlockDeviceMapping builds the single-root BDM that DescribeImages
// would report for this AMI. EBS-backed AMIs get /dev/sda1 with the snapshot
// pointer and size from AMIMetadata; non-EBS or empty AMIs get nil.
func synthesizeRootBlockDeviceMapping(meta viperblock.AMIMetadata) []*ec2.BlockDeviceMapping {
	if meta.RootDeviceType != "ebs" {
		return nil
	}
	ebs := &ec2.EbsBlockDevice{
		VolumeSize:          aws.Int64(utils.SafeUint64ToInt64(meta.VolumeSizeGiB)),
		VolumeType:          aws.String("gp3"),
		DeleteOnTermination: aws.Bool(true),
		Encrypted:           aws.Bool(false),
	}
	if meta.SnapshotID != "" {
		ebs.SnapshotId = aws.String(meta.SnapshotID)
	}
	return []*ec2.BlockDeviceMapping{{
		DeviceName: aws.String("/dev/sda1"),
		Ebs:        ebs,
	}}
}

// RegisterImage creates an AMI metadata record that points at an existing
// snapshot. It is pointer-only: it never moves, copies, or writes block data.
// Validation done in the gateway layer guarantees the input shape; here we
// only enforce semantic checks (snapshot existence/ownership, volume sizing,
// name uniqueness).
//
// The snapshot read runs before the O(n) amiNameExists scan so a missing
// snapshot fast-fails without paying for a full AMI listing.
func (s *ImageServiceImpl) RegisterImage(input *ec2.RegisterImageInput, accountID string) (*ec2.RegisterImageOutput, error) {
	if input == nil || input.Name == nil || *input.Name == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	name := *input.Name

	rootBDM := pickRootSnapshotBDM(input.BlockDeviceMappings, input.RootDeviceName)
	if rootBDM == nil {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	snapshotID := *rootBDM.Ebs.SnapshotId

	snapCfg, err := handlers_ec2_snapshot.ReadSnapshotConfig(s.store, s.bucketName, snapshotID)
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) || errors.Is(err, handlers_ec2_snapshot.ErrCorruptSnapshotMetadata) {
			// A corrupt snapshot record is indistinguishable from a missing
			// one from the caller's perspective — mirrors CopyImage's handling.
			return nil, errors.New(awserrors.ErrorInvalidSnapshotNotFound)
		}
		slog.Error("RegisterImage: failed to read snapshot metadata", "snapshotId", snapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Snapshot ownership: callers can only register from their own snapshots
	// or from system snapshots (non-account-ID owner, mirroring system AMIs).
	if utils.IsAccountID(snapCfg.OwnerID) && snapCfg.OwnerID != accountID {
		slog.Warn("RegisterImage: rejected cross-account snapshot",
			"snapshotId", snapshotID, "snapshotOwner", snapCfg.OwnerID, "accountId", accountID)
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

	if exists, err := s.amiNameExists(name); err != nil {
		slog.Error("RegisterImage: failed to check AMI name uniqueness", "name", name, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	} else if exists {
		return nil, errors.New(awserrors.ErrorInvalidAMINameDuplicate)
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

	tags := utils.ExtractTags(input.TagSpecifications, "image")

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

// ModifyImageAttribute persists a new value for a modifiable AMI attribute.
// The gateway validator normalises the input so we only ever see the
// Attribute+Value pair (top-level Description is folded in upstream). Today
// only description is writable — everything else is refused.
//
// Ownership is checked before the attribute switch so cross-account callers
// consistently see UnauthorizedOperation regardless of whether the attribute
// name is one we support.
func (s *ImageServiceImpl) ModifyImageAttribute(input *ec2.ModifyImageAttributeInput, accountID string) (*ec2.ModifyImageAttributeOutput, error) {
	if input == nil || input.ImageId == nil || *input.ImageId == "" ||
		input.Attribute == nil || *input.Attribute == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	imageID := *input.ImageId
	attribute := *input.Attribute

	meta, err := s.loadAMIForMutation(imageID, accountID)
	if err != nil {
		return nil, err
	}

	if attribute != ec2.ImageAttributeNameDescription {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	newValue := ""
	if input.Value != nil {
		newValue = *input.Value
	}
	// Skip the PutObject when nothing changes — drift-refresh loops (Terraform
	// aws_ami read + replay) would otherwise churn out no-op writes.
	if meta.Description == newValue {
		slog.Info("ModifyImageAttribute no-op", "imageId", imageID, "attribute", attribute, "accountId", accountID)
		return &ec2.ModifyImageAttributeOutput{}, nil
	}
	meta.Description = newValue

	if err := s.putAMIConfig(imageID, meta); err != nil {
		slog.Error("ModifyImageAttribute: failed to write AMI config", "imageId", imageID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("ModifyImageAttribute completed", "imageId", imageID, "attribute", attribute, "accountId", accountID)
	return &ec2.ModifyImageAttributeOutput{}, nil
}

// ResetImageAttribute restores an AMI attribute to its default. Only
// description is supported; it resets to empty string. launchPermission (AWS's
// default reset target) is out of scope.
//
// Ownership is checked before the attribute switch so cross-account callers
// consistently see UnauthorizedOperation.
func (s *ImageServiceImpl) ResetImageAttribute(input *ec2.ResetImageAttributeInput, accountID string) (*ec2.ResetImageAttributeOutput, error) {
	if input == nil || input.ImageId == nil || *input.ImageId == "" ||
		input.Attribute == nil || *input.Attribute == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	imageID := *input.ImageId
	attribute := *input.Attribute

	meta, err := s.loadAMIForMutation(imageID, accountID)
	if err != nil {
		return nil, err
	}

	if attribute != ec2.ImageAttributeNameDescription {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	// Skip the PutObject when the description is already empty.
	if meta.Description == "" {
		slog.Info("ResetImageAttribute no-op", "imageId", imageID, "attribute", attribute, "accountId", accountID)
		return &ec2.ResetImageAttributeOutput{}, nil
	}
	meta.Description = ""

	if err := s.putAMIConfig(imageID, meta); err != nil {
		slog.Error("ResetImageAttribute: failed to write AMI config", "imageId", imageID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("ResetImageAttribute completed", "imageId", imageID, "attribute", attribute, "accountId", accountID)
	return &ec2.ResetImageAttributeOutput{}, nil
}
