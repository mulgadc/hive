package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateEnableImageBlockPublicAccessInput(input *ec2.EnableImageBlockPublicAccessInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.ImageBlockPublicAccessState == nil || *input.ImageBlockPublicAccessState == "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func EnableImageBlockPublicAccess(input *ec2.EnableImageBlockPublicAccessInput, natsConn *nats.Conn) (ec2.EnableImageBlockPublicAccessOutput, error) {
	var output ec2.EnableImageBlockPublicAccessOutput

	if err := ValidateEnableImageBlockPublicAccessInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.EnableImageBlockPublicAccess(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
