package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateDisableImageBlockPublicAccessInput(input *ec2.DisableImageBlockPublicAccessInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func DisableImageBlockPublicAccess(input *ec2.DisableImageBlockPublicAccessInput, natsConn *nats.Conn) (ec2.DisableImageBlockPublicAccessOutput, error) {
	var output ec2.DisableImageBlockPublicAccessOutput

	if err := ValidateDisableImageBlockPublicAccessInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.DisableImageBlockPublicAccess(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
