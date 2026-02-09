package handlers_ec2_tags

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/objectstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ensure interface compliance
var _ TagsService = (*TagsServiceImpl)(nil)

// setupTestTagsService creates a tags service with in-memory storage for testing
func setupTestTagsService(t *testing.T) (*TagsServiceImpl, *objectstore.MemoryObjectStore) {
	store := objectstore.NewMemoryObjectStore()
	cfg := &config.Config{
		Predastore: config.PredastoreConfig{
			Bucket: "test-bucket",
		},
	}

	svc := NewTagsServiceImplWithStore(cfg, store)
	return svc, store
}

// TestCreateTags tests adding tags to resources
func TestCreateTags(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	// Create tags for an instance
	result, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test123")},
		Tags: []*ec2.Tag{
			{Key: aws.String("Name"), Value: aws.String("test-instance")},
			{Key: aws.String("Environment"), Value: aws.String("test")},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify tags were created
	describeResult, err := svc.DescribeTags(&ec2.DescribeTagsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("resource-id"), Values: []*string{aws.String("i-test123")}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, describeResult.Tags, 2)
}

// TestCreateTags_MultipleResources tests adding tags to multiple resources
func TestCreateTags_MultipleResources(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	// Create tags for multiple resources
	result, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{
			aws.String("i-test1"),
			aws.String("i-test2"),
			aws.String("vol-test1"),
		},
		Tags: []*ec2.Tag{
			{Key: aws.String("Project"), Value: aws.String("test-project")},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify tags were created for all resources
	describeResult, err := svc.DescribeTags(&ec2.DescribeTagsInput{})
	require.NoError(t, err)
	assert.Len(t, describeResult.Tags, 3)
}

// TestCreateTags_UpdateExisting tests updating existing tags
func TestCreateTags_UpdateExisting(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	// Create initial tag
	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test123")},
		Tags: []*ec2.Tag{
			{Key: aws.String("Name"), Value: aws.String("original")},
		},
	})
	require.NoError(t, err)

	// Update the tag
	_, err = svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test123")},
		Tags: []*ec2.Tag{
			{Key: aws.String("Name"), Value: aws.String("updated")},
		},
	})
	require.NoError(t, err)

	// Verify tag was updated
	describeResult, err := svc.DescribeTags(&ec2.DescribeTagsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("resource-id"), Values: []*string{aws.String("i-test123")}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, describeResult.Tags, 1)
	assert.Equal(t, "updated", *describeResult.Tags[0].Value)
}

// TestCreateTags_MissingResources tests creating tags without resources
func TestCreateTags_MissingResources(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Tags: []*ec2.Tag{
			{Key: aws.String("Name"), Value: aws.String("test")},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorMissingParameter)
}

// TestCreateTags_MissingTags tests creating tags without tags
func TestCreateTags_MissingTags(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test123")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorMissingParameter)
}

// TestDescribeTags tests listing all tags
func TestDescribeTags(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	// Create tags for different resource types
	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test1")},
		Tags:      []*ec2.Tag{{Key: aws.String("Name"), Value: aws.String("instance1")}},
	})
	require.NoError(t, err)

	_, err = svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("vol-test1")},
		Tags:      []*ec2.Tag{{Key: aws.String("Name"), Value: aws.String("volume1")}},
	})
	require.NoError(t, err)

	// Describe all tags
	result, err := svc.DescribeTags(&ec2.DescribeTagsInput{})
	require.NoError(t, err)
	assert.Len(t, result.Tags, 2)
}

// TestDescribeTags_FilterByResourceID tests filtering by resource ID
func TestDescribeTags_FilterByResourceID(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	// Create tags for different resources
	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test1")},
		Tags:      []*ec2.Tag{{Key: aws.String("Name"), Value: aws.String("instance1")}},
	})
	require.NoError(t, err)

	_, err = svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test2")},
		Tags:      []*ec2.Tag{{Key: aws.String("Name"), Value: aws.String("instance2")}},
	})
	require.NoError(t, err)

	// Filter by resource ID
	result, err := svc.DescribeTags(&ec2.DescribeTagsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("resource-id"), Values: []*string{aws.String("i-test1")}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, result.Tags, 1)
	assert.Equal(t, "i-test1", *result.Tags[0].ResourceId)
}

// TestDescribeTags_FilterByResourceType tests filtering by resource type
func TestDescribeTags_FilterByResourceType(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	// Create tags for different resource types
	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test1")},
		Tags:      []*ec2.Tag{{Key: aws.String("Name"), Value: aws.String("instance1")}},
	})
	require.NoError(t, err)

	_, err = svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("vol-test1")},
		Tags:      []*ec2.Tag{{Key: aws.String("Name"), Value: aws.String("volume1")}},
	})
	require.NoError(t, err)

	// Filter by resource type
	result, err := svc.DescribeTags(&ec2.DescribeTagsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("resource-type"), Values: []*string{aws.String("instance")}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, result.Tags, 1)
	assert.Equal(t, "instance", *result.Tags[0].ResourceType)
}

// TestDescribeTags_FilterByKey tests filtering by tag key
func TestDescribeTags_FilterByKey(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	// Create tags with different keys
	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test1")},
		Tags: []*ec2.Tag{
			{Key: aws.String("Name"), Value: aws.String("instance1")},
			{Key: aws.String("Environment"), Value: aws.String("test")},
		},
	})
	require.NoError(t, err)

	// Filter by key
	result, err := svc.DescribeTags(&ec2.DescribeTagsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("key"), Values: []*string{aws.String("Name")}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, result.Tags, 1)
	assert.Equal(t, "Name", *result.Tags[0].Key)
}

// TestDescribeTags_FilterByValue tests filtering by tag value
func TestDescribeTags_FilterByValue(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	// Create tags with different values
	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test1")},
		Tags: []*ec2.Tag{
			{Key: aws.String("Environment"), Value: aws.String("production")},
		},
	})
	require.NoError(t, err)

	_, err = svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test2")},
		Tags: []*ec2.Tag{
			{Key: aws.String("Environment"), Value: aws.String("test")},
		},
	})
	require.NoError(t, err)

	// Filter by value
	result, err := svc.DescribeTags(&ec2.DescribeTagsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("value"), Values: []*string{aws.String("production")}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, result.Tags, 1)
	assert.Equal(t, "production", *result.Tags[0].Value)
}

// TestDescribeTags_Empty tests listing tags when none exist
func TestDescribeTags_Empty(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	result, err := svc.DescribeTags(&ec2.DescribeTagsInput{})
	require.NoError(t, err)
	assert.Empty(t, result.Tags)
}

// TestDeleteTags tests deleting specific tags
func TestDeleteTags(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	// Create tags
	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test123")},
		Tags: []*ec2.Tag{
			{Key: aws.String("Name"), Value: aws.String("test")},
			{Key: aws.String("Environment"), Value: aws.String("test")},
		},
	})
	require.NoError(t, err)

	// Delete one tag
	_, err = svc.DeleteTags(&ec2.DeleteTagsInput{
		Resources: []*string{aws.String("i-test123")},
		Tags: []*ec2.Tag{
			{Key: aws.String("Name")},
		},
	})
	require.NoError(t, err)

	// Verify only one tag remains
	result, err := svc.DescribeTags(&ec2.DescribeTagsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("resource-id"), Values: []*string{aws.String("i-test123")}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, result.Tags, 1)
	assert.Equal(t, "Environment", *result.Tags[0].Key)
}

// TestDeleteTags_AllTags tests deleting all tags from a resource
func TestDeleteTags_AllTags(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	// Create tags
	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-test123")},
		Tags: []*ec2.Tag{
			{Key: aws.String("Name"), Value: aws.String("test")},
			{Key: aws.String("Environment"), Value: aws.String("test")},
		},
	})
	require.NoError(t, err)

	// Delete all tags (no tags specified)
	_, err = svc.DeleteTags(&ec2.DeleteTagsInput{
		Resources: []*string{aws.String("i-test123")},
	})
	require.NoError(t, err)

	// Verify no tags remain
	result, err := svc.DescribeTags(&ec2.DescribeTagsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("resource-id"), Values: []*string{aws.String("i-test123")}},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Tags)
}

// TestDeleteTags_MissingResources tests deleting tags without resources
func TestDeleteTags_MissingResources(t *testing.T) {
	svc, _ := setupTestTagsService(t)

	_, err := svc.DeleteTags(&ec2.DeleteTagsInput{
		Tags: []*ec2.Tag{
			{Key: aws.String("Name")},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorMissingParameter)
}

// TestGetResourceType tests the resource type detection helper
func TestGetResourceType(t *testing.T) {
	tests := []struct {
		resourceID   string
		expectedType string
	}{
		{"i-abc123", "instance"},
		{"vol-abc123", "volume"},
		{"ami-abc123", "image"},
		{"snap-abc123", "snapshot"},
		{"vpc-abc123", "vpc"},
		{"subnet-abc123", "subnet"},
		{"sg-abc123", "security-group"},
		{"rtb-abc123", "route-table"},
		{"igw-abc123", "internet-gateway"},
		{"unknown-abc123", "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.resourceID, func(t *testing.T) {
			assert.Equal(t, tc.expectedType, getResourceType(tc.resourceID))
		})
	}
}

// TestMemoryObjectStore tests the in-memory object store
func TestMemoryObjectStore(t *testing.T) {
	store := objectstore.NewMemoryObjectStore()

	// Test that GetObject returns NoSuchKeyError for missing objects
	_, err := store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String("test-bucket"),
		Key:    aws.String("nonexistent"),
	})
	require.Error(t, err)
	assert.True(t, objectstore.IsNoSuchKeyError(err))
}
