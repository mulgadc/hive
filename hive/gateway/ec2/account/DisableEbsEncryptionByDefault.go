package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateDisableEbsEncryptionByDefaultInput(input *ec2.DisableEbsEncryptionByDefaultInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func DisableEbsEncryptionByDefault(input *ec2.DisableEbsEncryptionByDefaultInput, natsConn *nats.Conn) (ec2.DisableEbsEncryptionByDefaultOutput, error) {
	var output ec2.DisableEbsEncryptionByDefaultOutput

	if err := ValidateDisableEbsEncryptionByDefaultInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.DisableEbsEncryptionByDefault(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
