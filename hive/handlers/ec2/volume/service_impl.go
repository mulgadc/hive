package handlers_ec2_volume

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/viperblock/viperblock"
	"golang.org/x/net/http2"
)

// VolumeServiceImpl handles EBS volume operations with S3 storage
type VolumeServiceImpl struct {
	config   *config.Config
	s3Client *s3.S3
}

// NewVolumeServiceImpl creates a new daemon-side volume service
func NewVolumeServiceImpl(cfg *config.Config) *VolumeServiceImpl {
	// Create HTTP client with TLS verification disabled and HTTP/2 support
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2", "http/1.1"},
		},
		ForceAttemptHTTP2: true,
	}

	// CRITICAL: Configure HTTP/2 support with custom TLS config
	if err := http2.ConfigureTransport(tr); err != nil {
		slog.Warn("Failed to configure HTTP/2", "error", err)
	}

	httpClient := &http.Client{Transport: tr}

	// Create AWS SDK S3 client configured for Predastore endpoint
	sess := session.Must(session.NewSession(&aws.Config{
		Endpoint:         aws.String(cfg.Predastore.Host),
		Region:           aws.String(cfg.Predastore.Region),
		Credentials:      credentials.NewStaticCredentials(cfg.Predastore.AccessKey, cfg.Predastore.SecretKey, ""),
		S3ForcePathStyle: aws.Bool(true),
		HTTPClient:       httpClient,
	}))

	s3Client := s3.New(sess)

	return &VolumeServiceImpl{
		config:   cfg,
		s3Client: s3Client,
	}
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
	result, err := s.s3Client.ListObjects(&s3.ListObjectsInput{
		Bucket:    aws.String(s.config.Predastore.Bucket),
		Delimiter: aws.String("/"),
	})

	if err != nil {
		slog.Error("Failed to list S3 objects", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	fmt.Println("Result", result)

	// Iterate over CommonPrefixes to find vol-* directories
	for _, prefix := range result.CommonPrefixes {
		if prefix.Prefix == nil {
			continue
		}

		prefixStr := *prefix.Prefix
		slog.Debug("Processing S3 prefix", "prefix", prefixStr)

		// Only check prefixes that match pattern: vol-<id>/
		if !strings.HasPrefix(prefixStr, "vol-") {
			continue
		}

		// Extract volume ID from prefix (remove trailing slash)
		volumeID := strings.TrimSuffix(prefixStr, "/")

		vol, err := s.getVolumeByID(volumeID)
		if err != nil {
			slog.Error("Failed to get volume", "volumeId", volumeID, "err", err)
			continue
		}

		volumes = append(volumes, vol)
	}

	fmt.Println("Volumes", volumes)

	slog.Info("DescribeVolumes completed", "count", len(volumes))

	return &ec2.DescribeVolumesOutput{
		Volumes: volumes,
	}, nil
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
	configKey := volumeID + "/config.json"

	getResult, err := s.s3Client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.config.Predastore.Bucket),
		Key:    aws.String(configKey),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && (aerr.Code() == s3.ErrCodeNoSuchKey || aerr.Code() == "NotFound") {
			slog.Debug("Config file not found", "key", configKey)
			return nil, errors.New("volume not found")
		}
		slog.Debug("Failed to get config file", "key", configKey, "err", err)
		return nil, err
	}
	defer getResult.Body.Close()

	body, err := io.ReadAll(getResult.Body)
	if err != nil {
		slog.Debug("Failed to read config body", "key", configKey, "err", err)
		return nil, err
	}

	var vbConfig struct {
		VolumeConfig viperblock.VolumeConfig `json:"VolumeConfig"`
	}
	if err := json.Unmarshal(body, &vbConfig); err != nil {
		slog.Debug("Failed to unmarshal config", "key", configKey, "err", err)
		return nil, err
	}

	volMeta := vbConfig.VolumeConfig.VolumeMetadata

	if volMeta.VolumeID == "" {
		slog.Debug("Volume ID is empty in config", "key", configKey)
		return nil, errors.New("volume ID is empty")
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
				DeleteOnTermination: aws.Bool(true),
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

// getVolumeConfig reads the raw VolumeConfig from S3 for a given volume ID
func (s *VolumeServiceImpl) getVolumeConfig(volumeID string) (*viperblock.VolumeConfig, error) {
	configKey := volumeID + "/config.json"

	getResult, err := s.s3Client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.config.Predastore.Bucket),
		Key:    aws.String(configKey),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && (aerr.Code() == s3.ErrCodeNoSuchKey || aerr.Code() == "NotFound") {
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

	_, err = s.s3Client.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(s.config.Predastore.Bucket),
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
	getResult, err := s.s3Client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.config.Predastore.Bucket),
		Key:    aws.String(configKey),
	})
	if err != nil {
		// No existing config.json -- write wrapper for new volume
		return json.Marshal(volumeConfigWrapper{VolumeConfig: *cfg})
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

// ModifyVolume modifies an EBS volume (grow-only, requires stopped instance)
func (s *VolumeServiceImpl) ModifyVolume(input *ec2.ModifyVolumeInput) (*ec2.ModifyVolumeOutput, error) {
	if input.VolumeId == nil || *input.VolumeId == "" {
		return nil, errors.New(awserrors.ErrorInvalidVolumeIDMalformed)
	}

	volumeID := *input.VolumeId
	slog.Info("ModifyVolume request", "volumeId", volumeID)

	cfg, err := s.getVolumeConfig(volumeID)
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
	if input.Size != nil {
		newSize := *input.Size
		if newSize <= originalSize {
			return nil, errors.New(awserrors.ErrorInvalidParameterValue)
		}
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
