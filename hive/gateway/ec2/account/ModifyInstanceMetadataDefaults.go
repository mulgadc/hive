package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateModifyInstanceMetadataDefaultsInput(input *ec2.ModifyInstanceMetadataDefaultsInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func ModifyInstanceMetadataDefaults(input *ec2.ModifyInstanceMetadataDefaultsInput, natsConn *nats.Conn) (ec2.ModifyInstanceMetadataDefaultsOutput, error) {
	var output ec2.ModifyInstanceMetadataDefaultsOutput

	if err := ValidateModifyInstanceMetadataDefaultsInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.ModifyInstanceMetadataDefaults(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
