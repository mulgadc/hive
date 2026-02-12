package handlers_ec2_volume

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/objectstore"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/viperblock/viperblock"
	s3backend "github.com/mulgadc/viperblock/viperblock/backends/s3"
	"github.com/nats-io/nats.go"
)

const defaultGP3IOPS = 3000

// Ensure VolumeServiceImpl implements VolumeService
var _ VolumeService = (*VolumeServiceImpl)(nil)

// VolumeServiceImpl handles EBS volume operations with S3 storage
type VolumeServiceImpl struct {
	config     *config.Config
	store      objectstore.ObjectStore
	bucketName string
	natsConn   *nats.Conn
	snapshotKV nats.KeyValue
}

// NewVolumeServiceImpl creates a new daemon-side volume service.
// snapshotKV is optional — when non-nil, DeleteVolume uses O(1) KV lookup
// instead of scanning all snapshots in S3.
func NewVolumeServiceImpl(cfg *config.Config, natsConn *nats.Conn, snapshotKV nats.KeyValue) *VolumeServiceImpl {
	store := objectstore.NewS3ObjectStoreFromConfig(
		cfg.Predastore.Host,
		cfg.Predastore.Region,
		cfg.Predastore.AccessKey,
		cfg.Predastore.SecretKey,
	)

	return &VolumeServiceImpl{
		config:     cfg,
		store:      store,
		bucketName: cfg.Predastore.Bucket,
		natsConn:   natsConn,
		snapshotKV: snapshotKV,
	}
}

// NewVolumeServiceImplWithStore creates a volume service with a custom ObjectStore (for testing)
func NewVolumeServiceImplWithStore(cfg *config.Config, store objectstore.ObjectStore, natsConn *nats.Conn, snapshotKV ...nats.KeyValue) *VolumeServiceImpl {
	bucketName := ""
	if cfg != nil {
		bucketName = cfg.Predastore.Bucket
	}
	svc := &VolumeServiceImpl{
		config:     cfg,
		store:      store,
		bucketName: bucketName,
		natsConn:   natsConn,
	}
	if len(snapshotKV) > 0 {
		svc.snapshotKV = snapshotKV[0]
	}
	return svc
}

// CreateVolume creates a new EBS volume via viperblock and persists its config to S3
func (s *VolumeServiceImpl) CreateVolume(input *ec2.CreateVolumeInput) (*ec2.Volume, error) {
	if input == nil {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	// Validate volume type: only gp3 supported (or empty defaults to gp3)
	if input.VolumeType != nil && *input.VolumeType != "" && *input.VolumeType != "gp3" {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	volumeType := "gp3"

	// Validate availability zone matches this node's AZ
	if input.AvailabilityZone == nil || *input.AvailabilityZone == "" {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if *input.AvailabilityZone != s.config.AZ {
		return nil, errors.New(awserrors.ErrorInvalidAvailabilityZone)
	}

	// If creating from snapshot, read snapshot metadata to get defaults
	var snapshotID string
	var sourceVolumeName string
	var snapshotSizeGiB int64

	if input.SnapshotId != nil && *input.SnapshotId != "" {
		snapshotID = *input.SnapshotId
		snapMeta, err := s.getSnapshotMetadata(snapshotID)
		if err != nil {
			slog.Error("CreateVolume: snapshot not found", "snapshotId", snapshotID, "err", err)
			return nil, errors.New(awserrors.ErrorInvalidSnapshotNotFound)
		}
		sourceVolumeName = snapMeta.VolumeID
		snapshotSizeGiB = snapMeta.VolumeSize
	}

	// Validate size (1-16384 GiB). When creating from snapshot, size can be
	// omitted (defaults to snapshot size) or must be >= snapshot size.
	var size int64
	if input.Size != nil {
		if *input.Size < 1 || *input.Size > 16384 {
			return nil, errors.New(awserrors.ErrorInvalidParameterValue)
		}
		if snapshotSizeGiB > 0 && *input.Size < snapshotSizeGiB {
			slog.Error("CreateVolume: requested size smaller than snapshot", "size", *input.Size, "snapshotSize", snapshotSizeGiB)
			return nil, errors.New(awserrors.ErrorInvalidParameterValue)
		}
		size = *input.Size
	} else if snapshotSizeGiB > 0 {
		size = snapshotSizeGiB
	} else {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	now := time.Now()
	volumeID := utils.GenerateResourceID("vol")

	iops := defaultGP3IOPS

	slog.Info("CreateVolume", "volumeId", volumeID, "size", size, "type", volumeType,
		"az", *input.AvailabilityZone, "snapshotId", snapshotID)

	// Volume size in bytes for viperblock
	sizeGiB := utils.SafeInt64ToUint64(size)
	volumeSizeBytes := sizeGiB * 1024 * 1024 * 1024

	// Build VolumeConfig with metadata
	volumeConfig := viperblock.VolumeConfig{
		VolumeMetadata: viperblock.VolumeMetadata{
			VolumeID:         volumeID,
			SizeGiB:          sizeGiB,
			State:            "available",
			CreatedAt:        now,
			AvailabilityZone: *input.AvailabilityZone,
			VolumeType:       volumeType,
			IOPS:             iops,
			IsEncrypted:      false,
			SnapshotID:       snapshotID,
		},
	}

	// Create S3 backend config
	cfg := s3backend.S3Config{
		VolumeName: volumeID,
		VolumeSize: volumeSizeBytes,
		Bucket:     s.bucketName,
		Region:     s.config.Predastore.Region,
		AccessKey:  s.config.Predastore.AccessKey,
		SecretKey:  s.config.Predastore.SecretKey,
		Host:       s.config.Predastore.Host,
	}

	vbconfig := viperblock.VB{
		VolumeName:   volumeID,
		VolumeSize:   volumeSizeBytes,
		BaseDir:      s.config.WalDir,
		Cache:        viperblock.Cache{Config: viperblock.CacheConfig{Size: 0}},
		VolumeConfig: volumeConfig,
	}

	// If created from a snapshot, set the snapshot fields so viperblock's
	// LoadState will call OpenFromSnapshot to load the base block map.
	if snapshotID != "" {
		vbconfig.SnapshotID = snapshotID
		vbconfig.SourceVolumeName = sourceVolumeName
	}

	vb, err := viperblock.New(&vbconfig, "s3", cfg)
	if err != nil {
		slog.Error("CreateVolume failed to create viperblock instance", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	vb.SetDebug(false)

	// Initialize the backend (creates bucket structure in S3)
	if err := vb.Backend.Init(); err != nil {
		slog.Error("CreateVolume failed to initialize backend", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Persist volume state to S3 (writes config.json)
	if err := vb.SaveState(); err != nil {
		slog.Error("CreateVolume failed to save state", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateVolume completed", "volumeId", volumeID, "size", size, "type", volumeType)

	vol := &ec2.Volume{
		VolumeId:         aws.String(volumeID),
		Size:             aws.Int64(size),
		VolumeType:       aws.String(volumeType),
		State:            aws.String("available"),
		AvailabilityZone: input.AvailabilityZone,
		CreateTime:       aws.Time(now),
		Iops:             aws.Int64(int64(iops)),
		Encrypted:        aws.Bool(false),
	}

	if snapshotID != "" {
		vol.SnapshotId = aws.String(snapshotID)
	}

	return vol, nil
}

// DescribeVolumes lists EBS volumes by reading config.json files from S3
func (s *VolumeServiceImpl) DescribeVolumes(input *ec2.DescribeVolumesInput) (*ec2.DescribeVolumesOutput, error) {
	if input == nil {
		input = &ec2.DescribeVolumesInput{}
	}

	slog.Info("Describing volumes", "volumeIds", input.VolumeIds)

	var volumes []*ec2.Volume

	// Fast path: if specific volume IDs are requested, fetch them directly
	if len(input.VolumeIds) > 0 {
		volumes = s.fetchVolumesByIDs(input.VolumeIds)
		slog.Info("DescribeVolumes completed", "count", len(volumes))
		return &ec2.DescribeVolumesOutput{Volumes: volumes}, nil
	}

	// Slow path: list all volumes (no filter provided)
	volumeIDs, err := s.listAllVolumeIDs()
	if err != nil {
		return nil, err
	}

	for _, volumeID := range volumeIDs {
		vol, err := s.getVolumeByID(volumeID)
		if err != nil {
			slog.Error("Failed to get volume", "volumeId", volumeID, "err", err)
			continue
		}

		volumes = append(volumes, vol)
	}

	slog.Info("DescribeVolumes completed", "count", len(volumes))

	return &ec2.DescribeVolumesOutput{
		Volumes: volumes,
	}, nil
}

// DescribeVolumeStatus returns the status of one or more EBS volumes
func (s *VolumeServiceImpl) DescribeVolumeStatus(input *ec2.DescribeVolumeStatusInput) (*ec2.DescribeVolumeStatusOutput, error) {
	if input == nil {
		input = &ec2.DescribeVolumeStatusInput{}
	}

	slog.Info("DescribeVolumeStatus", "volumeIds", input.VolumeIds)

	var statusItems []*ec2.VolumeStatusItem

	// Fast path: if specific volume IDs are requested, fetch them directly
	if len(input.VolumeIds) > 0 {
		for _, vid := range input.VolumeIds {
			if vid == nil {
				continue
			}
			item, err := s.getVolumeStatusByID(*vid)
			if err != nil {
				slog.Error("DescribeVolumeStatus volume not found", "volumeId", *vid, "err", err)
				return nil, errors.New(awserrors.ErrorInvalidVolumeNotFound)
			}
			statusItems = append(statusItems, item)
		}
		slog.Info("DescribeVolumeStatus completed", "count", len(statusItems))
		return &ec2.DescribeVolumeStatusOutput{VolumeStatuses: statusItems}, nil
	}

	// Slow path: list all volumes (no filter provided)
	volumeIDs, err := s.listAllVolumeIDs()
	if err != nil {
		return nil, err
	}

	for _, volumeID := range volumeIDs {
		item, err := s.getVolumeStatusByID(volumeID)
		if err != nil {
			slog.Error("Failed to get volume status", "volumeId", volumeID, "err", err)
			continue
		}

		statusItems = append(statusItems, item)
	}

	slog.Info("DescribeVolumeStatus completed", "count", len(statusItems))

	return &ec2.DescribeVolumeStatusOutput{
		VolumeStatuses: statusItems,
	}, nil
}

// getVolumeStatusByID builds a VolumeStatusItem by reusing getVolumeByID
// to validate the volume exists, then returning static health status.
func (s *VolumeServiceImpl) getVolumeStatusByID(volumeID string) (*ec2.VolumeStatusItem, error) {
	vol, err := s.getVolumeByID(volumeID)
	if err != nil {
		return nil, err
	}

	return &ec2.VolumeStatusItem{
		VolumeId:         vol.VolumeId,
		AvailabilityZone: vol.AvailabilityZone,
		VolumeStatus: &ec2.VolumeStatusInfo{
			Status: aws.String("ok"),
			Details: []*ec2.VolumeStatusDetails{
				{
					Name:   aws.String("io-enabled"),
					Status: aws.String("passed"),
				},
				{
					Name:   aws.String("io-performance"),
					Status: aws.String("not-applicable"),
				},
			},
		},
		Actions: []*ec2.VolumeStatusAction{},
		Events:  []*ec2.VolumeStatusEvent{},
	}, nil
}

// listAllVolumeIDs lists all volume IDs from S3 by scanning bucket prefixes.
// It filters for vol-* prefixes and skips internal sub-volumes (EFI and cloud-init).
func (s *VolumeServiceImpl) listAllVolumeIDs() ([]string, error) {
	result, err := s.store.ListObjects(&s3.ListObjectsInput{
		Bucket:    aws.String(s.bucketName),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		slog.Error("Failed to list S3 objects", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	var volumeIDs []string
	for _, prefix := range result.CommonPrefixes {
		if prefix.Prefix == nil {
			continue
		}

		prefixStr := *prefix.Prefix

		if !strings.HasPrefix(prefixStr, "vol-") {
			continue
		}

		volumeID := strings.TrimSuffix(prefixStr, "/")

		// Skip internal sub-volumes (EFI and cloud-init partitions)
		if strings.HasSuffix(volumeID, "-efi") || strings.HasSuffix(volumeID, "-cloudinit") {
			continue
		}

		volumeIDs = append(volumeIDs, volumeID)
	}

	return volumeIDs, nil
}

// fetchVolumesByIDs fetches multiple volumes in parallel by their IDs
func (s *VolumeServiceImpl) fetchVolumesByIDs(volumeIDs []*string) []*ec2.Volume {
	var (
		volumes []*ec2.Volume
		mu      sync.Mutex
		wg      sync.WaitGroup
	)

	for _, volumeID := range volumeIDs {
		if volumeID == nil {
			continue
		}

		wg.Add(1)
		go func(volID string) {
			defer wg.Done()

			vol, err := s.getVolumeByID(volID)
			if err != nil {
				slog.Debug("Volume not found", "volumeId", volID, "err", err)
				return
			}

			mu.Lock()
			volumes = append(volumes, vol)
			mu.Unlock()
		}(*volumeID)
	}

	wg.Wait()
	return volumes
}

// getVolumeByID fetches a single volume's config from S3 and builds an EC2 Volume
func (s *VolumeServiceImpl) getVolumeByID(volumeID string) (*ec2.Volume, error) {
	cfg, err := s.GetVolumeConfig(volumeID)
	if err != nil {
		return nil, err
	}

	volMeta := cfg.VolumeMetadata

	if volMeta.VolumeID == "" {
		slog.Debug("Volume ID is empty in config", "key", volumeID+"/config.json")
		return nil, errors.New("volume ID is empty")
	}

	if volMeta.SizeGiB == 0 {
		slog.Error("Volume has zero size in config", "volumeId", volumeID)
		return nil, fmt.Errorf("volume %s has zero size in config", volumeID)
	}

	state := volMeta.State
	if state == "" {
		state = "available"
	}
	volumeType := volMeta.VolumeType
	if volumeType == "" {
		volumeType = "gp3"
	}

	volume := &ec2.Volume{
		VolumeId:         aws.String(volMeta.VolumeID),
		Size:             aws.Int64(utils.SafeUint64ToInt64(volMeta.SizeGiB)),
		State:            aws.String(state),
		AvailabilityZone: aws.String(volMeta.AvailabilityZone),
		CreateTime:       aws.Time(volMeta.CreatedAt),
		VolumeType:       aws.String(volumeType),
		Encrypted:        aws.Bool(volMeta.IsEncrypted),
	}

	if volMeta.IOPS > 0 {
		volume.Iops = aws.Int64(int64(volMeta.IOPS))
	}

	if volMeta.SnapshotID != "" {
		volume.SnapshotId = aws.String(volMeta.SnapshotID)
	}

	if volMeta.AttachedInstance != "" {
		attachState := "attached"
		if volMeta.State != "in-use" {
			attachState = "detached"
		}
		volume.Attachments = []*ec2.VolumeAttachment{
			{
				VolumeId:            aws.String(volMeta.VolumeID),
				InstanceId:          aws.String(volMeta.AttachedInstance),
				Device:              aws.String(volMeta.DeviceName),
				State:               aws.String(attachState),
				DeleteOnTermination: aws.Bool(volMeta.DeleteOnTermination),
				AttachTime:          aws.Time(volMeta.AttachedAt),
			},
		}
	}

	if len(volMeta.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(volMeta.Tags))
		for key, value := range volMeta.Tags {
			tags = append(tags, &ec2.Tag{
				Key:   aws.String(key),
				Value: aws.String(value),
			})
		}
		volume.Tags = tags
	}

	return volume, nil
}

// volumeConfigWrapper matches the JSON structure stored in S3 config.json files
type volumeConfigWrapper struct {
	VolumeConfig viperblock.VolumeConfig `json:"VolumeConfig"`
}

// GetVolumeConfig reads the raw VolumeConfig from S3 for a given volume ID.
func (s *VolumeServiceImpl) GetVolumeConfig(volumeID string) (*viperblock.VolumeConfig, error) {
	configKey := volumeID + "/config.json"

	getResult, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(configKey),
	})
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) {
			return nil, errors.New(awserrors.ErrorInvalidVolumeNotFound)
		}
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	defer getResult.Body.Close()

	body, err := io.ReadAll(getResult.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read config body: %w", err)
	}

	var wrapper volumeConfigWrapper
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &wrapper.VolumeConfig, nil
}

// putVolumeConfig writes a VolumeConfig back to S3 as config.json.
// It performs a read-modify-write to preserve full VBState if viperblock
// has already written state (BlockSize, SeqNum, WALNum, etc.) to config.json.
func (s *VolumeServiceImpl) putVolumeConfig(volumeID string, cfg *viperblock.VolumeConfig) error {
	configKey := volumeID + "/config.json"

	data, err := s.mergeVolumeConfig(configKey, cfg)
	if err != nil {
		return err
	}

	_, err = s.store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(configKey),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("failed to write config to S3: %w", err)
	}

	return nil
}

// mergeVolumeConfig reads existing config.json from S3 and merges the new
// VolumeConfig into it, preserving full VBState when present. If no existing
// VBState is found, it returns a plain volumeConfigWrapper.
func (s *VolumeServiceImpl) mergeVolumeConfig(configKey string, cfg *viperblock.VolumeConfig) ([]byte, error) {
	getResult, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(configKey),
	})
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) {
			// No existing config.json -- write wrapper for new volume
			return json.Marshal(volumeConfigWrapper{VolumeConfig: *cfg})
		}
		return nil, fmt.Errorf("failed to read existing config for merge: %w", err)
	}
	defer getResult.Body.Close()

	body, err := io.ReadAll(getResult.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read existing config: %w", err)
	}

	var state viperblock.VBState
	if json.Unmarshal(body, &state) != nil || state.BlockSize == 0 {
		// Not a full VBState (new volume or wrapper-only) -- write wrapper
		return json.Marshal(volumeConfigWrapper{VolumeConfig: *cfg})
	}

	// Full VBState exists -- update VolumeConfig and reconcile VolumeSize
	state.VolumeConfig = *cfg
	configSizeBytes := uint64(cfg.VolumeMetadata.SizeGiB) * 1024 * 1024 * 1024
	if configSizeBytes > 0 && configSizeBytes > state.VolumeSize {
		state.VolumeSize = configSizeBytes
	}

	slog.Info("putVolumeConfig: preserved VBState", "volumeId", strings.TrimSuffix(configKey, "/config.json"),
		"blockSize", state.BlockSize, "seqNum", state.SeqNum)

	return json.Marshal(state)
}

// UpdateVolumeState updates volume metadata (state, attachment, device) in the object store.
func (s *VolumeServiceImpl) UpdateVolumeState(volumeID, state, attachedInstance, deviceName string) error {
	cfg, err := s.GetVolumeConfig(volumeID)
	if err != nil {
		return fmt.Errorf("failed to get volume config for state update: %w", err)
	}

	cfg.VolumeMetadata.State = state
	cfg.VolumeMetadata.AttachedInstance = attachedInstance
	cfg.VolumeMetadata.DeviceName = deviceName
	if attachedInstance != "" {
		cfg.VolumeMetadata.AttachedAt = time.Now()
	}

	if err := s.putVolumeConfig(volumeID, cfg); err != nil {
		return fmt.Errorf("failed to write volume config for state update: %w", err)
	}

	slog.Info("Updated volume state", "volumeId", volumeID, "state", state, "attachedInstance", attachedInstance, "deviceName", deviceName)
	return nil
}

// ModifyVolume modifies an EBS volume (grow-only, requires stopped instance)
func (s *VolumeServiceImpl) ModifyVolume(input *ec2.ModifyVolumeInput) (*ec2.ModifyVolumeOutput, error) {
	if input.VolumeId == nil || *input.VolumeId == "" {
		return nil, errors.New(awserrors.ErrorInvalidVolumeIDMalformed)
	}

	volumeID := *input.VolumeId
	slog.Info("ModifyVolume request", "volumeId", volumeID)

	cfg, err := s.GetVolumeConfig(volumeID)
	if err != nil {
		slog.Error("ModifyVolume failed to get volume config", "volumeId", volumeID, "err", err)
		return nil, err
	}

	volMeta := &cfg.VolumeMetadata

	// Record original values before modification
	originalSize := utils.SafeUint64ToInt64(volMeta.SizeGiB)
	originalType := volMeta.VolumeType
	if originalType == "" {
		originalType = "gp3"
	}
	originalIOPS := int64(volMeta.IOPS)

	// Validate: grow only (new size must be greater than current)
	if input.Size != nil && *input.Size <= originalSize {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	// Validate: if volume is attached, instance must not be in-use (must be stopped)
	if volMeta.AttachedInstance != "" && volMeta.State == "in-use" {
		return nil, errors.New(awserrors.ErrorIncorrectState)
	}

	// Apply modifications
	if input.Size != nil {
		volMeta.SizeGiB = utils.SafeInt64ToUint64(*input.Size)
	}
	if input.VolumeType != nil {
		volMeta.VolumeType = *input.VolumeType
	}
	if input.Iops != nil {
		volMeta.IOPS = int(*input.Iops)
	}

	// Persist updated config
	if err := s.putVolumeConfig(volumeID, cfg); err != nil {
		slog.Error("ModifyVolume failed to write config", "volumeId", volumeID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Build target values (after modification)
	targetSize := utils.SafeUint64ToInt64(volMeta.SizeGiB)
	targetType := volMeta.VolumeType
	if targetType == "" {
		targetType = "gp3"
	}
	targetIOPS := int64(volMeta.IOPS)

	now := time.Now()
	modification := &ec2.VolumeModification{
		VolumeId:           aws.String(volumeID),
		ModificationState:  aws.String("completed"),
		Progress:           aws.Int64(100),
		OriginalSize:       aws.Int64(originalSize),
		OriginalVolumeType: aws.String(originalType),
		OriginalIops:       aws.Int64(originalIOPS),
		TargetSize:         aws.Int64(targetSize),
		TargetVolumeType:   aws.String(targetType),
		TargetIops:         aws.Int64(targetIOPS),
		StartTime:          aws.Time(now),
		EndTime:            aws.Time(now),
	}

	slog.Info("ModifyVolume completed", "volumeId", volumeID,
		"originalSize", originalSize, "targetSize", targetSize)

	return &ec2.ModifyVolumeOutput{
		VolumeModification: modification,
	}, nil
}

// DeleteVolume deletes an EBS volume: validates state, notifies viperblockd, and removes S3 data
func (s *VolumeServiceImpl) DeleteVolume(input *ec2.DeleteVolumeInput) (*ec2.DeleteVolumeOutput, error) {
	if input == nil || input.VolumeId == nil || *input.VolumeId == "" {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	volumeID := *input.VolumeId
	slog.Info("DeleteVolume request", "volumeId", volumeID)

	// Fetch volume config to validate state
	cfg, err := s.GetVolumeConfig(volumeID)
	if err != nil {
		slog.Error("DeleteVolume failed to get volume config", "volumeId", volumeID, "err", err)
		return nil, err
	}

	// Validate: volume must be available and not attached
	if cfg.VolumeMetadata.State != "available" || cfg.VolumeMetadata.AttachedInstance != "" {
		slog.Error("DeleteVolume: volume is in use", "volumeId", volumeID, "state", cfg.VolumeMetadata.State, "attachedInstance", cfg.VolumeMetadata.AttachedInstance)
		return nil, errors.New(awserrors.ErrorVolumeInUse)
	}

	// Check if any snapshots reference this volume. Snapshot-backed clones
	// read chunk files from the source volume's S3 prefix via ReadFrom().
	// Deleting the source volume would silently break all clones.
	if err := s.checkVolumeHasNoSnapshots(volumeID); err != nil {
		return nil, err
	}

	// Notify viperblockd to stop nbdkit/WAL syncer (best-effort)
	if s.natsConn != nil {
		deleteReq := config.EBSDeleteRequest{Volume: volumeID}
		deleteData, err := json.Marshal(deleteReq)
		if err != nil {
			slog.Error("DeleteVolume failed to marshal ebs.delete request", "volumeId", volumeID, "err", err)
		} else {
			msg, err := s.natsConn.Request("ebs.delete", deleteData, 5*time.Second)
			if err != nil {
				slog.Warn("ebs.delete notification failed (volume may not be mounted)", "volumeId", volumeID, "err", err)
			} else {
				var deleteResp config.EBSDeleteResponse
				if json.Unmarshal(msg.Data, &deleteResp) == nil && deleteResp.Error != "" {
					slog.Error("ebs.delete returned error", "volumeId", volumeID, "err", deleteResp.Error)
					return nil, errors.New(awserrors.ErrorServerInternal)
				}
			}
		}
	} else {
		slog.Warn("DeleteVolume: natsConn is nil, skipping viperblockd notification", "volumeId", volumeID)
	}

	// Delete all S3 objects for this volume and its sub-volumes.
	// Auxiliary prefixes are deleted first so the main config.json remains
	// available for retry if an auxiliary deletion fails.
	prefixes := []string{
		volumeID + "-efi/",
		volumeID + "-cloudinit/",
		volumeID + "/",
	}

	for _, prefix := range prefixes {
		if err := s.deleteS3Prefix(prefix); err != nil {
			slog.Error("DeleteVolume failed to delete S3 prefix", "prefix", prefix, "err", err)
			return nil, errors.New(awserrors.ErrorServerInternal)
		}
	}

	slog.Info("DeleteVolume completed", "volumeId", volumeID)

	return &ec2.DeleteVolumeOutput{}, nil
}

// deleteS3Prefix deletes all S3 objects under the given prefix
func (s *VolumeServiceImpl) deleteS3Prefix(prefix string) error {
	bucket := s.bucketName

	var marker *string
	for {
		listOutput, err := s.store.ListObjects(&s3.ListObjectsInput{
			Bucket: aws.String(bucket),
			Prefix: aws.String(prefix),
			Marker: marker,
		})
		if err != nil {
			return fmt.Errorf("failed to list objects with prefix %s: %w", prefix, err)
		}

		if len(listOutput.Contents) == 0 {
			break
		}

		for _, obj := range listOutput.Contents {
			_, err := s.store.DeleteObject(&s3.DeleteObjectInput{
				Bucket: aws.String(bucket),
				Key:    obj.Key,
			})
			if err != nil {
				return fmt.Errorf("failed to delete object %s: %w", *obj.Key, err)
			}
		}

		if !aws.BoolValue(listOutput.IsTruncated) {
			break
		}
		// Use the last key as the marker for the next page
		lastKey := listOutput.Contents[len(listOutput.Contents)-1].Key
		marker = lastKey
	}

	return nil
}

// snapshotMetadata holds the subset of snapshot metadata needed by CreateVolume.
// Matches the JSON written by the snapshot service's SnapshotConfig.
type snapshotMetadata struct {
	VolumeID   string `json:"volume_id"`
	VolumeSize int64  `json:"volume_size"`
}

// getSnapshotMetadata reads snapshot metadata.json from S3 for CreateVolume.
func (s *VolumeServiceImpl) getSnapshotMetadata(snapshotID string) (*snapshotMetadata, error) {
	key := snapshotID + "/metadata.json"

	getResult, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) {
			return nil, errors.New(awserrors.ErrorInvalidSnapshotNotFound)
		}
		return nil, fmt.Errorf("failed to get snapshot metadata: %w", err)
	}
	defer getResult.Body.Close()

	body, err := io.ReadAll(getResult.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot metadata: %w", err)
	}

	var meta snapshotMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("failed to decode snapshot metadata: %w", err)
	}

	return &meta, nil
}

// snapshotVolumeRef reads VolumeID from hive metadata.json (volume_id)
// or viperblock config.json (SourceVolumeName).
type snapshotVolumeRef struct {
	VolumeID         string `json:"volume_id"`
	SourceVolumeName string `json:"SourceVolumeName"`
}

func (r snapshotVolumeRef) referencesVolume(volumeID string) bool {
	return r.VolumeID == volumeID || r.SourceVolumeName == volumeID
}

// checkVolumeHasNoSnapshots checks if a volume has dependent snapshots.
// Uses the JetStream KV index for O(1) lookup when available, falling back
// to the S3 scan when KV is nil or the lookup fails.
func (s *VolumeServiceImpl) checkVolumeHasNoSnapshots(volumeID string) error {
	if s.snapshotKV != nil {
		has, err := s.volumeHasSnapshotsKV(volumeID)
		if err != nil {
			slog.Warn("checkVolumeHasNoSnapshots: KV lookup failed, falling back to S3 scan", "volumeId", volumeID, "err", err)
		} else if has {
			slog.Error("DeleteVolume blocked: volume has snapshots (KV)", "volumeId", volumeID)
			return fmt.Errorf("%s: volume %s has existing snapshots. Delete snapshots first", awserrors.ErrorVolumeInUse, volumeID)
		} else {
			return nil
		}
	}

	return s.checkVolumeHasNoSnapshotsS3(volumeID)
}

// volumeHasSnapshotsKV checks the JetStream KV index for snapshot references.
func (s *VolumeServiceImpl) volumeHasSnapshotsKV(volumeID string) (bool, error) {
	entry, err := s.snapshotKV.Get(volumeID)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return false, nil
		}
		return false, err
	}

	var snapshots []string
	if err := json.Unmarshal(entry.Value(), &snapshots); err != nil {
		return false, err
	}

	return len(snapshots) > 0, nil
}

// checkVolumeHasNoSnapshotsS3 is the original S3-scanning fallback.
func (s *VolumeServiceImpl) checkVolumeHasNoSnapshotsS3(volumeID string) error {
	listResult, err := s.store.ListObjects(&s3.ListObjectsInput{
		Bucket:    aws.String(s.bucketName),
		Prefix:    aws.String("snap-"),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		slog.Error("checkVolumeHasNoSnapshots: failed to list snapshots", "volumeId", volumeID, "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	for _, prefix := range listResult.CommonPrefixes {
		if prefix.Prefix == nil {
			continue
		}

		snapshotID := strings.TrimSuffix(*prefix.Prefix, "/")

		if err := s.snapshotReferencesVolume(snapshotID, volumeID); err != nil {
			return err
		}
	}

	return nil
}

// snapshotReferencesVolume checks if a snapshot references the given volume.
// Returns an error if the snapshot references the volume or if a non-recoverable
// S3/IO error occurs. Returns nil if the snapshot does not reference the volume.
func (s *VolumeServiceImpl) snapshotReferencesVolume(snapshotID, volumeID string) error {
	// Try metadata.json first (hive format), then config.json (viperblock format)
	for _, filename := range []string{"metadata.json", "config.json"} {
		key := snapshotID + "/" + filename

		getResult, err := s.store.GetObject(&s3.GetObjectInput{
			Bucket: aws.String(s.bucketName),
			Key:    aws.String(key),
		})
		if err != nil {
			if objectstore.IsNoSuchKeyError(err) {
				continue // Expected: this format file doesn't exist for this snapshot
			}
			slog.Error("checkVolumeHasNoSnapshots: failed to read snapshot metadata",
				"volumeId", volumeID, "snapshotId", snapshotID, "key", key, "err", err)
			return errors.New(awserrors.ErrorServerInternal)
		}

		body, err := io.ReadAll(getResult.Body)
		if closeErr := getResult.Body.Close(); closeErr != nil {
			slog.Error("checkVolumeHasNoSnapshots: failed to close response body",
				"volumeId", volumeID, "snapshotId", snapshotID, "key", key, "err", closeErr)
		}
		if err != nil {
			slog.Error("checkVolumeHasNoSnapshots: failed to read snapshot body",
				"volumeId", volumeID, "snapshotId", snapshotID, "key", key, "err", err)
			return errors.New(awserrors.ErrorServerInternal)
		}

		var ref snapshotVolumeRef
		if err := json.Unmarshal(body, &ref); err != nil {
			slog.Warn("checkVolumeHasNoSnapshots: failed to parse snapshot metadata, skipping file",
				"volumeId", volumeID, "snapshotId", snapshotID, "key", key, "err", err)
			continue // JSON parse errors for individual files are not fatal -- try next file
		}

		if ref.referencesVolume(volumeID) {
			slog.Error("DeleteVolume blocked: volume has snapshots", "volumeId", volumeID, "snapshotId", snapshotID)
			return fmt.Errorf("%s: volume %s has existing snapshot %s. Delete snapshots first", awserrors.ErrorVolumeInUse, volumeID, snapshotID)
		}
	}

	return nil
}
