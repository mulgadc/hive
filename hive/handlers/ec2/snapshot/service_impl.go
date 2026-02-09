package handlers_ec2_snapshot

import (
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
		if objectstore.IsNoSuchKeyError(err) || strings.Contains(err.Error(), "NoSuchKey") || strings.Contains(err.Error(), "404") {
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
		Body:        strings.NewReader(string(data)),
		ContentType: aws.String("application/json"),
	})

	return err
}

// CreateSnapshot creates a new snapshot from a volume
func (s *SnapshotServiceImpl) CreateSnapshot(input *ec2.CreateSnapshotInput) (*ec2.Snapshot, error) {
	if input == nil || input.VolumeId == nil {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	volumeID := *input.VolumeId

	slog.Info("CreateSnapshot request", "volumeId", volumeID)

	// Generate snapshot ID
	snapshotID := viperblock.GenerateVolumeID("snap", fmt.Sprintf("snap-%d", time.Now().UnixNano()), s.config.Predastore.Bucket, time.Now().Unix())

	// Get volume config to get size and availability zone
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

	var volumeConfig viperblock.VolumeConfig
	if err := json.NewDecoder(volumeResult.Body).Decode(&volumeConfig); err != nil {
		slog.Error("CreateSnapshot failed to decode volume config", "volumeId", volumeID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	now := time.Now()

	// Create snapshot config
	snapshotCfg := &SnapshotConfig{
		SnapshotID:       snapshotID,
		VolumeID:         volumeID,
		VolumeSize:       int64(volumeConfig.VolumeMetadata.SizeGiB),
		State:            "pending",
		Progress:         "0%",
		StartTime:        now,
		Encrypted:        volumeConfig.VolumeMetadata.IsEncrypted,
		OwnerID:          s.config.Predastore.AccessKey,
		AvailabilityZone: volumeConfig.VolumeMetadata.AvailabilityZone,
		Tags:             make(map[string]string),
	}

	if input.Description != nil {
		snapshotCfg.Description = *input.Description
	}

	// Copy tags if provided
	if input.TagSpecifications != nil {
		for _, tagSpec := range input.TagSpecifications {
			if tagSpec.ResourceType != nil && *tagSpec.ResourceType == "snapshot" {
				for _, tag := range tagSpec.Tags {
					if tag.Key != nil && tag.Value != nil {
						snapshotCfg.Tags[*tag.Key] = *tag.Value
					}
				}
			}
		}
	}

	// Store snapshot config
	if err := s.putSnapshotConfig(snapshotID, snapshotCfg); err != nil {
		slog.Error("CreateSnapshot failed to write config", "snapshotId", snapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// For Hive v1, we mark snapshots as completed immediately since actual data
	// copying would require additional infrastructure. The snapshot metadata
	// points to the volume for restore operations.
	snapshotCfg.State = "completed"
	snapshotCfg.Progress = "100%"
	if err := s.putSnapshotConfig(snapshotID, snapshotCfg); err != nil {
		slog.Error("CreateSnapshot failed to update state", "snapshotId", snapshotID, "err", err)
		// Don't fail - snapshot exists even if state update failed
	}

	// Build response
	snapshot := &ec2.Snapshot{
		SnapshotId:  aws.String(snapshotID),
		VolumeId:    aws.String(volumeID),
		VolumeSize:  aws.Int64(snapshotCfg.VolumeSize),
		State:       aws.String(snapshotCfg.State),
		Progress:    aws.String(snapshotCfg.Progress),
		StartTime:   aws.Time(now),
		Description: aws.String(snapshotCfg.Description),
		Encrypted:   aws.Bool(snapshotCfg.Encrypted),
		OwnerId:     aws.String(snapshotCfg.OwnerID),
	}

	// Add tags to response
	if len(snapshotCfg.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(snapshotCfg.Tags))
		for key, value := range snapshotCfg.Tags {
			tags = append(tags, &ec2.Tag{
				Key:   aws.String(key),
				Value: aws.String(value),
			})
		}
		snapshot.Tags = tags
	}

	slog.Info("CreateSnapshot completed", "snapshotId", snapshotID, "volumeId", volumeID)

	return snapshot, nil
}

// DescribeSnapshots lists snapshots matching the specified criteria
func (s *SnapshotServiceImpl) DescribeSnapshots(input *ec2.DescribeSnapshotsInput) (*ec2.DescribeSnapshotsOutput, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	slog.Info("DescribeSnapshots request", "snapshotIds", input.SnapshotIds)

	var snapshots []*ec2.Snapshot

	// Build filter for specific snapshot IDs if provided
	snapshotIDFilter := make(map[string]bool)
	if input.SnapshotIds != nil && len(input.SnapshotIds) > 0 {
		for _, id := range input.SnapshotIds {
			if id != nil {
				snapshotIDFilter[*id] = true
			}
		}
	}

	// List all snapshot directories from S3
	listResult, err := s.store.ListObjects(&s3.ListObjectsInput{
		Bucket:    aws.String(s.config.Predastore.Bucket),
		Prefix:    aws.String("snap-"),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		slog.Error("DescribeSnapshots failed to list objects", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Process each snapshot directory
	for _, prefix := range listResult.CommonPrefixes {
		if prefix.Prefix == nil {
			continue
		}

		snapshotID := strings.TrimSuffix(*prefix.Prefix, "/")

		// Filter by snapshot ID if specified
		if len(snapshotIDFilter) > 0 && !snapshotIDFilter[snapshotID] {
			continue
		}

		// Get snapshot config
		cfg, err := s.getSnapshotConfig(snapshotID)
		if err != nil {
			slog.Warn("DescribeSnapshots failed to get config", "snapshotId", snapshotID, "err", err)
			continue
		}

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

		// Add tags
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

		snapshots = append(snapshots, snapshot)
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

	// Verify snapshot exists
	_, err := s.getSnapshotConfig(snapshotID)
	if err != nil {
		slog.Error("DeleteSnapshot snapshot not found", "snapshotId", snapshotID, "err", err)
		return nil, err
	}

	// Delete all objects under snapshotID/ prefix
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

	// Get source snapshot config
	sourceCfg, err := s.getSnapshotConfig(sourceSnapshotID)
	if err != nil {
		slog.Error("CopySnapshot source snapshot not found", "snapshotId", sourceSnapshotID, "err", err)
		return nil, err
	}

	// Generate new snapshot ID
	newSnapshotID := viperblock.GenerateVolumeID("snap", fmt.Sprintf("snap-%d", time.Now().UnixNano()), s.config.Predastore.Bucket, time.Now().Unix())

	// Create new snapshot config as copy
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

	// Copy tags
	for k, v := range sourceCfg.Tags {
		newCfg.Tags[k] = v
	}

	// Store new snapshot config
	if err := s.putSnapshotConfig(newSnapshotID, newCfg); err != nil {
		slog.Error("CopySnapshot failed to write config", "snapshotId", newSnapshotID, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CopySnapshot completed", "sourceSnapshotId", sourceSnapshotID, "newSnapshotId", newSnapshotID)

	return &ec2.CopySnapshotOutput{
		SnapshotId: aws.String(newSnapshotID),
	}, nil
}

// isValidSnapshotID checks if snapshot ID format is valid
func isValidSnapshotID(id string) bool {
	return strings.HasPrefix(id, "snap-") && len(id) > 5
}
