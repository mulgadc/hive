package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateDisableSerialConsoleAccessInput(input *ec2.DisableSerialConsoleAccessInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func DisableSerialConsoleAccess(input *ec2.DisableSerialConsoleAccessInput, natsConn *nats.Conn) (ec2.DisableSerialConsoleAccessOutput, error) {
	var output ec2.DisableSerialConsoleAccessOutput

	if err := ValidateDisableSerialConsoleAccessInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.DisableSerialConsoleAccess(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
