package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateEnableSnapshotBlockPublicAccessInput(input *ec2.EnableSnapshotBlockPublicAccessInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.State == nil || *input.State == "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func EnableSnapshotBlockPublicAccess(input *ec2.EnableSnapshotBlockPublicAccessInput, natsConn *nats.Conn) (ec2.EnableSnapshotBlockPublicAccessOutput, error) {
	var output ec2.EnableSnapshotBlockPublicAccessOutput

	if err := ValidateEnableSnapshotBlockPublicAccessInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.EnableSnapshotBlockPublicAccess(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
