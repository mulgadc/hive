package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateGetImageBlockPublicAccessStateInput(input *ec2.GetImageBlockPublicAccessStateInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func GetImageBlockPublicAccessState(input *ec2.GetImageBlockPublicAccessStateInput, natsConn *nats.Conn) (ec2.GetImageBlockPublicAccessStateOutput, error) {
	var output ec2.GetImageBlockPublicAccessStateOutput

	if err := ValidateGetImageBlockPublicAccessStateInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.GetImageBlockPublicAccessState(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
