package gateway_ec2_image

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_image "github.com/mulgadc/spinifex/spinifex/handlers/ec2/image"
	"github.com/nats-io/nats.go"
)

// ValidateModifyImageAttributeInput validates and normalises the input for
// ModifyImageAttribute.
//
// AWS accepts two shapes for modifying description:
//
//	--description Value=foo                    → input.Description={Value:"foo"}
//	--attribute description --value foo        → input.Attribute="description", input.Value="foo"
//
// We normalise the first form into the second so the daemon only ever has to
// look at Attribute+Value. Anything else we don't support (launchPermission,
// imdsSupport, productCodes, …) is refused at the boundary rather than
// silently accepted.
func ValidateModifyImageAttributeInput(input *ec2.ModifyImageAttributeInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	if input.ImageId == nil || *input.ImageId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	if !strings.HasPrefix(*input.ImageId, "ami-") {
		return errors.New(awserrors.ErrorInvalidAMIIDMalformed)
	}

	// Refuse every top-level shortcut we don't implement. Multi-account AMI
	// sharing (launchPermission) is a separate design problem; imdsSupport is
	// launch-time and not wired today; productCodes is not supported.
	if input.LaunchPermission != nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.ImdsSupport != nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if len(input.ProductCodes) > 0 {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if len(input.UserIds) > 0 || len(input.UserGroups) > 0 ||
		len(input.OrganizationArns) > 0 || len(input.OrganizationalUnitArns) > 0 ||
		(input.OperationType != nil && *input.OperationType != "") {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	hasTopLevelDescription := input.Description != nil
	// Value alone (no Attribute) is a malformed request — treat any set Value
	// as "structured shape" so it can't be silently discarded by the
	// top-level branch. Nil vs empty-string are kept distinct.
	hasStructured := (input.Attribute != nil && *input.Attribute != "") || input.Value != nil

	switch {
	case hasTopLevelDescription && hasStructured:
		return errors.New(awserrors.ErrorInvalidParameterCombination)
	case hasTopLevelDescription:
		value := ""
		if input.Description.Value != nil {
			value = *input.Description.Value
		}
		input.Attribute = aws.String(ec2.ImageAttributeNameDescription)
		input.Value = aws.String(value)
	case hasStructured:
		if input.Attribute == nil || *input.Attribute == "" {
			return errors.New(awserrors.ErrorMissingParameter)
		}
		if *input.Attribute != ec2.ImageAttributeNameDescription {
			return errors.New(awserrors.ErrorInvalidParameterValue)
		}
	default:
		return errors.New(awserrors.ErrorMissingParameter)
	}

	return nil
}

// ModifyImageAttribute handles the EC2 ModifyImageAttribute API call.
func ModifyImageAttribute(input *ec2.ModifyImageAttributeInput, natsConn *nats.Conn, accountID string) (ec2.ModifyImageAttributeOutput, error) {
	var output ec2.ModifyImageAttributeOutput

	if err := ValidateModifyImageAttributeInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_image.NewNATSImageService(natsConn, 0)
	result, err := svc.ModifyImageAttribute(input, accountID)
	if err != nil {
		return output, err
	}
	if result == nil {
		return output, errors.New(awserrors.ErrorServerInternal)
	}

	return *result, nil
}
