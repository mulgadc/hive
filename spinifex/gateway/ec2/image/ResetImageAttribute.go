package gateway_ec2_image

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_image "github.com/mulgadc/spinifex/spinifex/handlers/ec2/image"
	"github.com/nats-io/nats.go"
)

// ValidateResetImageAttributeInput validates the input for ResetImageAttribute.
// Only description is resettable — launchPermission (AWS's only reset target in
// the SDK enum) is deliberately out of scope here.
func ValidateResetImageAttributeInput(input *ec2.ResetImageAttributeInput) error {
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
	if *input.Attribute != ec2.ImageAttributeNameDescription {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

// ResetImageAttribute handles the EC2 ResetImageAttribute API call.
func ResetImageAttribute(input *ec2.ResetImageAttributeInput, natsConn *nats.Conn, accountID string) (ec2.ResetImageAttributeOutput, error) {
	var output ec2.ResetImageAttributeOutput

	if err := ValidateResetImageAttributeInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_image.NewNATSImageService(natsConn, 0)
	result, err := svc.ResetImageAttribute(input, accountID)
	if err != nil {
		return output, err
	}
	if result == nil {
		return output, errors.New(awserrors.ErrorServerInternal)
	}

	return *result, nil
}
