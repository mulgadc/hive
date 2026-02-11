package gateway_ec2_image

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_image "github.com/mulgadc/hive/hive/handlers/ec2/image"
	"github.com/nats-io/nats.go"
)

// ValidateCreateImageInput validates the input parameters for CreateImage
func ValidateCreateImageInput(input *ec2.CreateImageInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.InstanceId == nil || *input.InstanceId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if !strings.HasPrefix(*input.InstanceId, "i-") {
		return errors.New(awserrors.ErrorInvalidInstanceIDMalformed)
	}

	if input.Name == nil || *input.Name == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	return nil
}

// CreateImage handles the EC2 CreateImage API call
func CreateImage(input *ec2.CreateImageInput, natsConn *nats.Conn) (ec2.CreateImageOutput, error) {
	var output ec2.CreateImageOutput

	if err := ValidateCreateImageInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_image.NewNATSImageService(natsConn)
	result, err := svc.CreateImage(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
