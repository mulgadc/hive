package gateway_ec2_image

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/hive/awserrors"
	"github.com/stretchr/testify/assert"
)

// Handler tests — call handlers directly to cover validation + NATS error paths

func TestCreateImage_ValidationErrors(t *testing.T) {
	_, err := CreateImage(nil, nil, 1, "")
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)

	_, err = CreateImage(&ec2.CreateImageInput{}, nil, 1, "")
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)

	_, err = CreateImage(&ec2.CreateImageInput{
		InstanceId: aws.String("bad-id"),
		Name:       aws.String("test"),
	}, nil, 1, "")
	assert.EqualError(t, err, awserrors.ErrorInvalidInstanceIDMalformed)

	_, err = CreateImage(&ec2.CreateImageInput{
		InstanceId: aws.String("i-1234567890abcdef0"),
	}, nil, 1, "")
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateImage_NilNATS(t *testing.T) {
	_, err := CreateImage(&ec2.CreateImageInput{
		InstanceId: aws.String("i-1234567890abcdef0"),
		Name:       aws.String("my-image"),
	}, nil, 1, "acct-123")
	assert.Error(t, err)
}

func TestDescribeImages_ValidationErrors(t *testing.T) {
	_, err := DescribeImages(&ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String("bad-id")},
	}, nil, "")
	assert.EqualError(t, err, awserrors.ErrorInvalidAMIIDMalformed)
}

func TestDescribeImages_NilNATS(t *testing.T) {
	_, err := DescribeImages(nil, nil, "acct-123")
	assert.Error(t, err)

	_, err = DescribeImages(&ec2.DescribeImagesInput{}, nil, "acct-123")
	assert.Error(t, err)
}
