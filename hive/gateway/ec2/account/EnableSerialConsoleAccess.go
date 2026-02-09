package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateEnableSerialConsoleAccessInput(input *ec2.EnableSerialConsoleAccessInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func EnableSerialConsoleAccess(input *ec2.EnableSerialConsoleAccessInput, natsConn *nats.Conn) (ec2.EnableSerialConsoleAccessOutput, error) {
	var output ec2.EnableSerialConsoleAccessOutput

	if err := ValidateEnableSerialConsoleAccessInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.EnableSerialConsoleAccess(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
