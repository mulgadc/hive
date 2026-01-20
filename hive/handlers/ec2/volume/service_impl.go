package handlers_ec2_volume

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/viperblock/viperblock"
)

// VolumeServiceImpl handles EBS volume operations with S3 storage
type VolumeServiceImpl struct {
	config   *config.Config
	s3Client *s3.S3
}

// NewVolumeServiceImpl creates a new daemon-side volume service
func NewVolumeServiceImpl(cfg *config.Config) *VolumeServiceImpl {
	// Create HTTP client with TLS verification disabled for local S3-compatible endpoints
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

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
			slog.Debug("Failed to get volume", "volumeId", volumeID, "err", err)
			continue
		}

		volumes = append(volumes, vol)
	}

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
		Size:             aws.Int64(int64(volMeta.SizeGiB)),
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
