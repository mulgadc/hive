package gateway_ec2_account

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_account "github.com/mulgadc/hive/hive/handlers/ec2/account"
	"github.com/nats-io/nats.go"
)

func ValidateDisableSnapshotBlockPublicAccessInput(input *ec2.DisableSnapshotBlockPublicAccessInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	return nil
}

func DisableSnapshotBlockPublicAccess(input *ec2.DisableSnapshotBlockPublicAccessInput, natsConn *nats.Conn) (ec2.DisableSnapshotBlockPublicAccessOutput, error) {
	var output ec2.DisableSnapshotBlockPublicAccessOutput

	if err := ValidateDisableSnapshotBlockPublicAccessInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_account.NewNATSAccountSettingsService(natsConn)
	result, err := svc.DisableSnapshotBlockPublicAccess(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
