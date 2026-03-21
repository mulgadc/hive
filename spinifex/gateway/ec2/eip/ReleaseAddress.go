package gateway_ec2_eip

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_eip "github.com/mulgadc/spinifex/spinifex/handlers/ec2/eip"
	"github.com/nats-io/nats.go"
)

// ReleaseAddress handles the EC2 ReleaseAddress API call.
func ReleaseAddress(input *ec2.ReleaseAddressInput, natsConn *nats.Conn, accountID string) (ec2.ReleaseAddressOutput, error) {
	var output ec2.ReleaseAddressOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.AllocationId == nil || *input.AllocationId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_eip.NewNATSEIPService(natsConn)
	result, err := svc.ReleaseAddress(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
