package handlers_ec2_snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
)

// Ensure SnapshotServiceImpl implements SnapshotService
var _ SnapshotService = (*SnapshotServiceImpl)(nil)

// SnapshotServiceImpl implements SnapshotService with S3-backed storage
type SnapshotServiceImpl struct {
	config *config.Config
	store  objectstore.ObjectStore
	mutex  sync.RWMutex
}

// SnapshotConfig represents snapshot metadata stored in S3
type SnapshotConfig struct {
	SnapshotID       string            `json:"snapshot_id"`
	VolumeID         string            `json:"volume_id"`
	VolumeSize       int64             `json:"volume_size"`
	State            string            `json:"state"`
	Progress         string            `json:"progress"`
	StartTime        time.Time         `json:"start_time"`
	Description      string            `json:"description"`
	Encrypted        bool              `json:"encrypted"`
	OwnerID          string            `json:"owner_id"`
	AvailabilityZone string            `json:"availability_zone"`
	Tags             map[string]string `json:"tags"`
}

// NewSnapshotServiceImpl creates a new snapshot service implementation
func NewSnapshotServiceImpl(cfg *config.Config) *SnapshotServiceImpl {
	store := objectstore.NewS3ObjectStoreFromConfig(
		cfg.Predastore.Host,
		cfg.Predastore.Region,
		cfg.Predastore.AccessKey,
		cfg.Predastore.SecretKey,
	)

	return &SnapshotServiceImpl{
		config: cfg,
		store:  store,
	}
}

// NewSnapshotServiceImplWithStore creates a snapshot service with a custom ObjectStore (for testing)
func NewSnapshotServiceImplWithStore(cfg *config.Config, store objectstore.ObjectStore) *SnapshotServiceImpl {
	return &SnapshotServiceImpl{
		config: cfg,
		store:  store,
	}
}

// getSnapshotKey returns the S3 key for storing snapshot config
func getSnapshotKey(snapshotID string) string {
	return fmt.Sprintf("%s/config.json", snapshotID)
}

// getSnapshotConfig retrieves snapshot config from S3
func (s *SnapshotServiceImpl) getSnapshotConfig(snapshotID string) (*SnapshotConfig, error) {
	key := getSnapshotKey(snapshotID)

	result, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.config.Predastore.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) {
			return nil, errors.New(awserrors.ErrorInvalidSnapshotNotFound)
		}
		return nil, err
	}
	defer result.Body.Close()

	var cfg SnapshotConfig
	if err := json.NewDecoder(result.Body).Decode(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// putSnapshotConfig stores snapshot config to S3
func (s *SnapshotServiceImpl) putSnapshotConfig(snapshotID string, cfg *SnapshotConfig) error {
	key := getSnapshotKey(snapshotID)

	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	_, err = s.store.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(s.config.Predastore.Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})

	return err
}

// snapshotConfigToEC2 converts a SnapshotConfig to an EC2 Snapshot response object
func snapshotConfigToEC2(cfg *SnapshotConfig) *ec2.Snapshot {
	snapshot := &ec2.Snapshot{
		SnapshotId:  aws.String(cfg.SnapshotID),
		VolumeId:    aws.String(cfg.VolumeID),
		VolumeSize:  aws.Int64(cfg.VolumeSize),
		State:       aws.String(cfg.State),
		Progress:    aws.String(cfg.Progress),
		StartTime:   aws.Time(cfg.StartTime),
		Description: aws.String(cfg.Description),
		Encrypted:   aws.Bool(cfg.Encrypted),
		OwnerId:     aws.String(cfg.OwnerID),
	}

	if len(cfg.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(cfg.Tags))
		for key, value := range cfg.Tags {
			tags = append(tags, &ec2.Tag{
				Key:   aws.String(key),
				Value: aws.String(value),
			})
		}
		snapshot.Tags = tags
	}

	return snapshot
}

// CreateSnapshot creates a new snapshot from a volume
func (s *SnapshotServiceImpl) CreateSnapshot(input *ec2.CreateSnapshotInput) (*ec2.Snapshot, error) {
	if input == nil || input.VolumeId == nil {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	volumeID := *input.VolumeId

	slog.Info("CreateSnapshot request", "volumeId", volumeID)

	snapshotID := viperblock.GenerateVolumeID("snap", fmt.Sprintf("snap-%d", time.Now().UnixNano()), s.config.Predastore.Bucket, time.Now().Unix())

	volumeConfigKey := fmt.Sprintf("%s/config.json", volumeID)
	volumeResult, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.config.Predastore.Bucket),
		Key:    aws.String(volumeConfigKey),
	})
	if err != nil {
		slog.Error("CreateSnapshot failed to get volume config", "volumeId", volumeID, "err", err)
		return nil, errors.New(awserrors.ErrorInvalidVolumeNotFound)
	}
	defer volumeResult.Body.Close()

	var volumeState viperblock.VBState
	if err := json.NewDecoder(volumeResult.Body).Decode(&volumeState); err != nil {
		slog.Error("CreateSnapshot failed to decode volume config", "volumeId", volumeID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	volumeConfig := volumeState.VolumeConfig

	if volumeConfig.VolumeMetadata.SizeGiB == 0 {
		slog.Error("CreateSnapshot: source volume has zero size in config", "volumeId", volumeID)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	now := time.Now()

	// Mark as completed immediately -- Hive v1 stores snapshot metadata
	// pointing to the source volume rather than copying actual block data.
	snapshotCfg := &SnapshotConfig{
		SnapshotID:       snapshotID,
		VolumeID:         volumeID,
		VolumeSize:       utils.SafeUint64ToInt64(volumeConfig.VolumeMetadata.SizeGiB),
		State:            "completed",
		Progress:         "100%",
		StartTime:        now,
		Encrypted:        volumeConfig.VolumeMetadata.IsEncrypted,
		OwnerID:          s.config.Predastore.AccessKey,
		AvailabilityZone: volumeConfig.VolumeMetadata.AvailabilityZone,
		Tags:             make(map[string]string),
	}

	if input.Description != nil {
		snapshotCfg.Description = *input.Description
	}

	for _, tagSpec := range input.TagSpecifications {
		if tagSpec.ResourceType != nil && *tagSpec.ResourceType == "snapshot" {
			for _, tag := range tagSpec.Tags {
				if tag.Key != nil && tag.Value != nil {
					snapshotCfg.Tags[*tag.Key] = *tag.Value
				}
			}
		}
	}

	if err := s.putSnapshotConfig(snapshotID, snapshotCfg); err != nil {
		slog.Error("CreateSnapshot failed to write config", "snapshotId", snapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateSnapshot completed", "snapshotId", snapshotID, "volumeId", volumeID)

	return snapshotConfigToEC2(snapshotCfg), nil
}

// DescribeSnapshots lists snapshots matching the specified criteria
func (s *SnapshotServiceImpl) DescribeSnapshots(input *ec2.DescribeSnapshotsInput) (*ec2.DescribeSnapshotsOutput, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	slog.Info("DescribeSnapshots request", "snapshotIds", input.SnapshotIds)

	snapshotIDFilter := make(map[string]bool)
	for _, id := range input.SnapshotIds {
		if id != nil {
			snapshotIDFilter[*id] = true
		}
	}

	listResult, err := s.store.ListObjects(&s3.ListObjectsInput{
		Bucket:    aws.String(s.config.Predastore.Bucket),
		Prefix:    aws.String("snap-"),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		slog.Error("DescribeSnapshots failed to list objects", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	var snapshots []*ec2.Snapshot
	for _, prefix := range listResult.CommonPrefixes {
		if prefix.Prefix == nil {
			continue
		}

		snapshotID := strings.TrimSuffix(*prefix.Prefix, "/")

		if len(snapshotIDFilter) > 0 && !snapshotIDFilter[snapshotID] {
			continue
		}

		cfg, err := s.getSnapshotConfig(snapshotID)
		if err != nil {
			slog.Warn("DescribeSnapshots failed to get config", "snapshotId", snapshotID, "err", err)
			continue
		}

		snapshots = append(snapshots, snapshotConfigToEC2(cfg))
	}

	slog.Info("DescribeSnapshots completed", "count", len(snapshots))

	return &ec2.DescribeSnapshotsOutput{
		Snapshots: snapshots,
	}, nil
}

// DeleteSnapshot deletes a snapshot
func (s *SnapshotServiceImpl) DeleteSnapshot(input *ec2.DeleteSnapshotInput) (*ec2.DeleteSnapshotOutput, error) {
	if input == nil || input.SnapshotId == nil {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	snapshotID := *input.SnapshotId

	slog.Info("DeleteSnapshot request", "snapshotId", snapshotID)

	_, err := s.getSnapshotConfig(snapshotID)
	if err != nil {
		slog.Error("DeleteSnapshot snapshot not found", "snapshotId", snapshotID, "err", err)
		return nil, err
	}

	listResult, err := s.store.ListObjects(&s3.ListObjectsInput{
		Bucket: aws.String(s.config.Predastore.Bucket),
		Prefix: aws.String(snapshotID + "/"),
	})
	if err != nil {
		slog.Error("DeleteSnapshot failed to list objects", "snapshotId", snapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	for _, obj := range listResult.Contents {
		if obj.Key == nil {
			continue
		}
		_, err := s.store.DeleteObject(&s3.DeleteObjectInput{
			Bucket: aws.String(s.config.Predastore.Bucket),
			Key:    obj.Key,
		})
		if err != nil {
			slog.Warn("DeleteSnapshot failed to delete object", "key", *obj.Key, "err", err)
		}
	}

	slog.Info("DeleteSnapshot completed", "snapshotId", snapshotID)

	return &ec2.DeleteSnapshotOutput{}, nil
}

// CopySnapshot copies a snapshot (within same region for now)
func (s *SnapshotServiceImpl) CopySnapshot(input *ec2.CopySnapshotInput) (*ec2.CopySnapshotOutput, error) {
	if input == nil || input.SourceSnapshotId == nil {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	sourceSnapshotID := *input.SourceSnapshotId

	slog.Info("CopySnapshot request", "sourceSnapshotId", sourceSnapshotID)

	sourceCfg, err := s.getSnapshotConfig(sourceSnapshotID)
	if err != nil {
		slog.Error("CopySnapshot source snapshot not found", "snapshotId", sourceSnapshotID, "err", err)
		return nil, err
	}

	newSnapshotID := viperblock.GenerateVolumeID("snap", fmt.Sprintf("snap-%d", time.Now().UnixNano()), s.config.Predastore.Bucket, time.Now().Unix())

	newCfg := &SnapshotConfig{
		SnapshotID:       newSnapshotID,
		VolumeID:         sourceCfg.VolumeID,
		VolumeSize:       sourceCfg.VolumeSize,
		State:            "completed",
		Progress:         "100%",
		StartTime:        time.Now(),
		Description:      sourceCfg.Description,
		Encrypted:        sourceCfg.Encrypted,
		OwnerID:          sourceCfg.OwnerID,
		AvailabilityZone: sourceCfg.AvailabilityZone,
		Tags:             make(map[string]string),
	}

	if input.Description != nil {
		newCfg.Description = *input.Description
	}

	for k, v := range sourceCfg.Tags {
		newCfg.Tags[k] = v
	}

	if err := s.putSnapshotConfig(newSnapshotID, newCfg); err != nil {
		slog.Error("CopySnapshot failed to write config", "snapshotId", newSnapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CopySnapshot completed", "sourceSnapshotId", sourceSnapshotID, "newSnapshotId", newSnapshotID)

	return &ec2.CopySnapshotOutput{
		SnapshotId: aws.String(newSnapshotID),
	}, nil
}
