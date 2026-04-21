package gateway_ec2_image

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_image "github.com/mulgadc/spinifex/spinifex/handlers/ec2/image"
	"github.com/nats-io/nats.go"
)

// supportedImageAttributes is the set of AMI attribute names spinifex exposes.
// description is modifiable; blockDeviceMapping is a read-only synthesis from
// AMIMetadata. Every other AWS-defined attribute is refused rather than padded
// with an empty response — see plan "Known limitation" on launchPermission.
var supportedImageAttributes = map[string]bool{
	ec2.ImageAttributeNameDescription:        true,
	ec2.ImageAttributeNameBlockDeviceMapping: true,
}

// ValidateDescribeImageAttributeInput validates the input for DescribeImageAttribute.
func ValidateDescribeImageAttributeInput(input *ec2.DescribeImageAttributeInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	if input.ImageId == nil || *input.ImageId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	if !strings.HasPrefix(*input.ImageId, "ami-") {
		return errors.New(awserrors.ErrorInvalidAMIIDMalformed)
	}
	if input.Attribute == nil || *input.Attribute == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	if !supportedImageAttributes[*input.Attribute] {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

// DescribeImageAttribute handles the EC2 DescribeImageAttribute API call.
func DescribeImageAttribute(input *ec2.DescribeImageAttributeInput, natsConn *nats.Conn, accountID string) (ec2.DescribeImageAttributeOutput, error) {
	var output ec2.DescribeImageAttributeOutput

	if err := ValidateDescribeImageAttributeInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_image.NewNATSImageService(natsConn, 0)
	result, err := svc.DescribeImageAttribute(input, accountID)
	if err != nil {
		return output, err
	}
	if result == nil {
		return output, errors.New(awserrors.ErrorServerInternal)
	}

	return *result, nil
}
