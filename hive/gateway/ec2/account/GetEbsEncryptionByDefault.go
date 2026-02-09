package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateGetEbsEncryptionByDefaultInput(input *ec2.GetEbsEncryptionByDefaultInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func GetEbsEncryptionByDefault(input *ec2.GetEbsEncryptionByDefaultInput, natsConn *nats.Conn) (ec2.GetEbsEncryptionByDefaultOutput, error) {
	var output ec2.GetEbsEncryptionByDefaultOutput

	if err := ValidateGetEbsEncryptionByDefaultInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.GetEbsEncryptionByDefault(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
