package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateGetSnapshotBlockPublicAccessStateInput(input *ec2.GetSnapshotBlockPublicAccessStateInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func GetSnapshotBlockPublicAccessState(input *ec2.GetSnapshotBlockPublicAccessStateInput, natsConn *nats.Conn) (ec2.GetSnapshotBlockPublicAccessStateOutput, error) {
	var output ec2.GetSnapshotBlockPublicAccessStateOutput

	if err := ValidateGetSnapshotBlockPublicAccessStateInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.GetSnapshotBlockPublicAccessState(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
