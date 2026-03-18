package gateway_ec2_tags

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/hive/awserrors"
	"github.com/stretchr/testify/assert"
)

// Handler tests — call handlers directly to cover validation + NATS error paths

func TestCreateTags_ValidationErrors(t *testing.T) {
	_, err := CreateTags(nil, nil, "")
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)

	_, err = CreateTags(&ec2.CreateTagsInput{}, nil, "")
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)

	_, err = CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-123")},
	}, nil, "")
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)

	_, err = CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-123")},
		Tags:      []*ec2.Tag{{Key: aws.String(""), Value: aws.String("v")}},
	}, nil, "")
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateTags_NilNATS(t *testing.T) {
	_, err := CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String("i-1234567890abcdef0")},
		Tags:      []*ec2.Tag{{Key: aws.String("Name"), Value: aws.String("test")}},
	}, nil, "acct-123")
	assert.Error(t, err)
}

func TestDeleteTags_ValidationErrors(t *testing.T) {
	_, err := DeleteTags(nil, nil, "")
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)

	_, err = DeleteTags(&ec2.DeleteTagsInput{}, nil, "")
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteTags_NilNATS(t *testing.T) {
	_, err := DeleteTags(&ec2.DeleteTagsInput{
		Resources: []*string{aws.String("i-1234567890abcdef0")},
	}, nil, "acct-123")
	assert.Error(t, err)
}

func TestDescribeTags_NilNATS(t *testing.T) {
	_, err := DescribeTags(nil, nil, "acct-123")
	assert.Error(t, err)

	_, err = DescribeTags(&ec2.DescribeTagsInput{}, nil, "acct-123")
	assert.Error(t, err)
}
