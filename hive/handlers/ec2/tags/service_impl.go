package handlers_ec2_tags

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/objectstore"
)

// Ensure TagsServiceImpl implements TagsService
var _ TagsService = (*TagsServiceImpl)(nil)

// TagsServiceImpl implements TagsService with S3-backed storage
type TagsServiceImpl struct {
	config *config.Config
	store  objectstore.ObjectStore
	mutex  sync.RWMutex
}

// NewTagsServiceImpl creates a new tags service implementation
func NewTagsServiceImpl(cfg *config.Config) *TagsServiceImpl {
	store := objectstore.NewS3ObjectStoreFromConfig(
		cfg.Predastore.Host,
		cfg.Predastore.Region,
		cfg.Predastore.AccessKey,
		cfg.Predastore.SecretKey,
	)

	return &TagsServiceImpl{
		config: cfg,
		store:  store,
	}
}

// NewTagsServiceImplWithStore creates a tags service with a custom ObjectStore (for testing)
func NewTagsServiceImplWithStore(cfg *config.Config, store objectstore.ObjectStore) *TagsServiceImpl {
	return &TagsServiceImpl{
		config: cfg,
		store:  store,
	}
}

// getResourceType extracts resource type from resource ID prefix
func getResourceType(resourceID string) string {
	if strings.HasPrefix(resourceID, "i-") {
		return "instance"
	}
	if strings.HasPrefix(resourceID, "vol-") {
		return "volume"
	}
	if strings.HasPrefix(resourceID, "ami-") {
		return "image"
	}
	if strings.HasPrefix(resourceID, "snap-") {
		return "snapshot"
	}
	if strings.HasPrefix(resourceID, "vpc-") {
		return "vpc"
	}
	if strings.HasPrefix(resourceID, "subnet-") {
		return "subnet"
	}
	if strings.HasPrefix(resourceID, "sg-") {
		return "security-group"
	}
	if strings.HasPrefix(resourceID, "rtb-") {
		return "route-table"
	}
	if strings.HasPrefix(resourceID, "igw-") {
		return "internet-gateway"
	}
	return "unknown"
}

// getTagsKey returns the S3 key for storing tags for a resource
func getTagsKey(resourceID string) string {
	return "tags/" + resourceID + ".json"
}

// collectFilterValues adds non-nil string pointer values to the target map
func collectFilterValues(values []*string, target map[string]bool) {
	for _, v := range values {
		if v != nil {
			target[*v] = true
		}
	}
}

// getResourceTags retrieves tags for a specific resource from S3
func (s *TagsServiceImpl) getResourceTags(resourceID string) (map[string]string, error) {
	key := getTagsKey(resourceID)

	result, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.config.Predastore.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}
	defer result.Body.Close()

	var tags map[string]string
	if err := json.NewDecoder(result.Body).Decode(&tags); err != nil {
		return nil, err
	}

	return tags, nil
}

// putResourceTags stores tags for a specific resource in S3
func (s *TagsServiceImpl) putResourceTags(resourceID string, tags map[string]string) error {
	key := getTagsKey(resourceID)

	data, err := json.Marshal(tags)
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

// CreateTags adds or overwrites tags for the specified resources
func (s *TagsServiceImpl) CreateTags(input *ec2.CreateTagsInput) (*ec2.CreateTagsOutput, error) {
	if input == nil {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if len(input.Resources) == 0 {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	if len(input.Tags) == 0 {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	slog.Info("CreateTags request", "resources", len(input.Resources), "tags", len(input.Tags))

	for _, resourceID := range input.Resources {
		if resourceID == nil {
			continue
		}

		// Get existing tags
		existingTags, err := s.getResourceTags(*resourceID)
		if err != nil {
			slog.Error("CreateTags failed to get existing tags", "resourceId", *resourceID, "err", err)
			return nil, errors.New(awserrors.ErrorServerInternal)
		}

		// Add/update new tags
		for _, tag := range input.Tags {
			if tag.Key != nil && tag.Value != nil {
				existingTags[*tag.Key] = *tag.Value
			}
		}

		// Save tags
		if err := s.putResourceTags(*resourceID, existingTags); err != nil {
			slog.Error("CreateTags failed to save tags", "resourceId", *resourceID, "err", err)
			return nil, errors.New(awserrors.ErrorServerInternal)
		}

		slog.Info("CreateTags applied", "resourceId", *resourceID, "tagCount", len(existingTags))
	}

	return &ec2.CreateTagsOutput{}, nil
}

// DescribeTags returns tags matching the specified filters
func (s *TagsServiceImpl) DescribeTags(input *ec2.DescribeTagsInput) (*ec2.DescribeTagsOutput, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	slog.Info("DescribeTags request")

	var tags []*ec2.TagDescription

	// List all tag files from S3
	listResult, err := s.store.ListObjects(&s3.ListObjectsInput{
		Bucket: aws.String(s.config.Predastore.Bucket),
		Prefix: aws.String("tags/"),
	})
	if err != nil {
		slog.Error("DescribeTags failed to list objects", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Build filter maps for efficient lookup
	resourceIDFilter := make(map[string]bool)
	resourceTypeFilter := make(map[string]bool)
	keyFilter := make(map[string]bool)
	valueFilter := make(map[string]bool)

	if input != nil {
		for _, filter := range input.Filters {
			if filter.Name == nil {
				continue
			}
			switch *filter.Name {
			case "resource-id":
				collectFilterValues(filter.Values, resourceIDFilter)
			case "resource-type":
				collectFilterValues(filter.Values, resourceTypeFilter)
			case "key":
				collectFilterValues(filter.Values, keyFilter)
			case "value":
				collectFilterValues(filter.Values, valueFilter)
			default:
				return nil, errors.New(awserrors.ErrorInvalidParameterValue)
			}
		}
	}

	// Process each tag file
	for _, obj := range listResult.Contents {
		if obj.Key == nil {
			continue
		}

		// Extract resource ID from key (tags/i-xxx.json -> i-xxx)
		resourceID := strings.TrimPrefix(*obj.Key, "tags/")
		resourceID = strings.TrimSuffix(resourceID, ".json")
		resourceType := getResourceType(resourceID)

		// Apply resource-id filter
		if len(resourceIDFilter) > 0 && !resourceIDFilter[resourceID] {
			continue
		}

		// Apply resource-type filter
		if len(resourceTypeFilter) > 0 && !resourceTypeFilter[resourceType] {
			continue
		}

		// Get tags for this resource
		resourceTags, err := s.getResourceTags(resourceID)
		if err != nil {
			slog.Warn("DescribeTags failed to get tags", "resourceId", resourceID, "err", err)
			continue
		}

		for key, value := range resourceTags {
			// Apply key filter
			if len(keyFilter) > 0 && !keyFilter[key] {
				continue
			}

			// Apply value filter
			if len(valueFilter) > 0 && !valueFilter[value] {
				continue
			}

			tags = append(tags, &ec2.TagDescription{
				ResourceId:   aws.String(resourceID),
				ResourceType: aws.String(resourceType),
				Key:          aws.String(key),
				Value:        aws.String(value),
			})
		}
	}

	slog.Info("DescribeTags completed", "count", len(tags))

	return &ec2.DescribeTagsOutput{
		Tags: tags,
	}, nil
}

// DeleteTags removes tags from the specified resources
func (s *TagsServiceImpl) DeleteTags(input *ec2.DeleteTagsInput) (*ec2.DeleteTagsOutput, error) {
	if input == nil {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if len(input.Resources) == 0 {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	slog.Info("DeleteTags request", "resources", len(input.Resources), "tags", len(input.Tags))

	for _, resourceID := range input.Resources {
		if resourceID == nil {
			continue
		}

		// Get existing tags
		existingTags, err := s.getResourceTags(*resourceID)
		if err != nil {
			slog.Error("DeleteTags failed to get existing tags", "resourceId", *resourceID, "err", err)
			return nil, errors.New(awserrors.ErrorServerInternal)
		}

		if len(input.Tags) == 0 {
			// Delete all tags if no specific tags provided
			existingTags = make(map[string]string)
		} else {
			// Delete specified tags — per AWS API, when Value is specified
			// the tag is only deleted if the stored value matches
			for _, tag := range input.Tags {
				if tag.Key == nil {
					continue
				}
				if tag.Value == nil {
					// No value specified: delete unconditionally
					delete(existingTags, *tag.Key)
				} else {
					// Value specified: only delete if current value matches
					if current, exists := existingTags[*tag.Key]; exists && current == *tag.Value {
						delete(existingTags, *tag.Key)
					}
				}
			}
		}

		// Save updated tags
		if err := s.putResourceTags(*resourceID, existingTags); err != nil {
			slog.Error("DeleteTags failed to save tags", "resourceId", *resourceID, "err", err)
			return nil, errors.New(awserrors.ErrorServerInternal)
		}

		slog.Info("DeleteTags applied", "resourceId", *resourceID, "remainingTags", len(existingTags))
	}

	return &ec2.DeleteTagsOutput{}, nil
}
