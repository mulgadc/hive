package handlers_ec2_image

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
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/viperblock/viperblock"
)

// ImageServiceImpl handles AMI image operations with S3 storage
type ImageServiceImpl struct {
	config    *config.Config
	s3Client  *s3.S3
	accountID string // AWS account ID for S3 key storage path
}

// NewImageServiceImpl creates a new daemon-side image service
func NewImageServiceImpl(cfg *config.Config) *ImageServiceImpl {
	// Create HTTP client with TLS verification disabled for local S3-compatible endpoints
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // Skip TLS verification for self-signed certs
			},
		},
	}

	// Create AWS SDK S3 client configured for Predastore endpoint
	sess := session.Must(session.NewSession(&aws.Config{
		Endpoint:         aws.String(cfg.Predastore.Host),
		Region:           aws.String(cfg.Predastore.Region),
		Credentials:      credentials.NewStaticCredentials(cfg.Predastore.AccessKey, cfg.Predastore.SecretKey, ""),
		S3ForcePathStyle: aws.Bool(true), // Required for S3-compatible endpoints
		HTTPClient:       httpClient,
	}))

	s3Client := s3.New(sess)

	return &ImageServiceImpl{
		config:    cfg,
		s3Client:  s3Client,
		accountID: "123456789", // TODO: Implement proper account ID management
	}
}

// DescribeImages lists available AMI images by reading config.json files from S3
func (s *ImageServiceImpl) DescribeImages(input *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
	if input == nil {
		input = &ec2.DescribeImagesInput{}
	}

	slog.Info("Describing images", "filters", input.Filters, "imageIds", input.ImageIds)

	// List all prefixes in the bucket (AMIs are stored as ami-<id>/ directories)
	result, err := s.s3Client.ListObjects(&s3.ListObjectsInput{
		Bucket:    aws.String(s.config.Predastore.Bucket),
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

// Stub implementations for other ImageService methods
func (s *ImageServiceImpl) CreateImage(input *ec2.CreateImageInput) (*ec2.CreateImageOutput, error) {
	return nil, errors.New("CreateImage not yet implemented")
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
