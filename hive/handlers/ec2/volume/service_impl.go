package handlers_ec2_volume

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

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

	// List all prefixes in the bucket (volumes are stored as vol-<id>/ directories)
	result, err := s.s3Client.ListObjects(&s3.ListObjectsInput{
		Bucket:    aws.String(s.config.Predastore.Bucket),
		Delimiter: aws.String("/"),
	})

	if err != nil {
		slog.Error("Failed to list S3 objects", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	var volumes []*ec2.Volume

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

		// Construct path to config.json for this volume directory
		configKey := prefixStr + "config.json"

		// Get the config.json file
		getResult, err := s.s3Client.GetObject(&s3.GetObjectInput{
			Bucket: aws.String(s.config.Predastore.Bucket),
			Key:    aws.String(configKey),
		})
		if err != nil {
			if aerr, ok := err.(awserr.Error); ok && (aerr.Code() == s3.ErrCodeNoSuchKey || aerr.Code() == "NotFound") {
				slog.Debug("Config file not found", "key", configKey)
			} else {
				slog.Debug("Failed to get config file", "key", configKey, "err", err)
			}
			continue
		}

		body, err := io.ReadAll(getResult.Body)
		getResult.Body.Close()
		if err != nil {
			slog.Debug("Failed to read config body", "key", configKey, "err", err)
			continue
		}

		// Parse the viperblock config which contains VolumeConfig with VolumeMetadata
		var vbConfig struct {
			VolumeConfig viperblock.VolumeConfig `json:"VolumeConfig"`
		}
		if err := json.Unmarshal(body, &vbConfig); err != nil {
			slog.Debug("Failed to unmarshal config", "key", configKey, "err", err)
			continue
		}

		volMeta := vbConfig.VolumeConfig.VolumeMetadata

		// Skip if VolumeID is empty
		if volMeta.VolumeID == "" {
			continue
		}

		// Filter by VolumeId if specified
		if len(input.VolumeIds) > 0 {
			found := false
			for _, filterID := range input.VolumeIds {
				if filterID != nil && *filterID == volMeta.VolumeID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Build EC2 Volume from VolumeMetadata
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

		// Add IOPS if set
		if volMeta.IOPS > 0 {
			volume.Iops = aws.Int64(int64(volMeta.IOPS))
		}

		// Add SnapshotId if set
		if volMeta.SnapshotID != "" {
			volume.SnapshotId = aws.String(volMeta.SnapshotID)
		}

		// Add attachment info if volume is attached
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

		// Add tags if available
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

		volumes = append(volumes, volume)
	}

	slog.Info("DescribeVolumes completed", "count", len(volumes))

	return &ec2.DescribeVolumesOutput{
		Volumes: volumes,
	}, nil
}
