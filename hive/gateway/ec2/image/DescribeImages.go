package gateway_ec2_image

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_image "github.com/mulgadc/hive/hive/handlers/ec2/image"
	"github.com/nats-io/nats.go"
)

func ValidateDescribeImagesInput(input *ec2.DescribeImagesInput) (err error) {
	if input == nil {
		return nil
	}

	// Validate ImageId format if provided
	if input.ImageIds != nil {
		for _, imageId := range input.ImageIds {
			if imageId != nil && !strings.HasPrefix(*imageId, "ami-") {
				return errors.New(awserrors.ErrorInvalidAMIIDMalformed)
			}
		}
	}

	return
}

func DescribeImages(input *ec2.DescribeImagesInput, natsConn *nats.Conn) (output ec2.DescribeImagesOutput, err error) {

	// Validate input
	err = ValidateDescribeImagesInput(input)

	if err != nil {
		return output, err
	}

	// Create NATS service and call handler
	imageService := handlers_ec2_image.NewNATSImageService(natsConn)
	result, err := imageService.DescribeImages(input)

	if err != nil {
		return output, err
	}

	// Return result
	output = *result
	return output, nil
}
