package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateGetInstanceMetadataDefaultsInput(input *ec2.GetInstanceMetadataDefaultsInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func GetInstanceMetadataDefaults(input *ec2.GetInstanceMetadataDefaultsInput, natsConn *nats.Conn) (ec2.GetInstanceMetadataDefaultsOutput, error) {
	var output ec2.GetInstanceMetadataDefaultsOutput

	if err := ValidateGetInstanceMetadataDefaultsInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.GetInstanceMetadataDefaults(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
