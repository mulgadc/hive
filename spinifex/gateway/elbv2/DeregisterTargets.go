package gateway_elbv2

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_elbv2 "github.com/mulgadc/spinifex/spinifex/handlers/elbv2"
	"github.com/nats-io/nats.go"
)

// DeregisterTargets handles the ELBv2 DeregisterTargets API call.
func DeregisterTargets(input *elbv2.DeregisterTargetsInput, natsConn *nats.Conn, accountID string) (elbv2.DeregisterTargetsOutput, error) {
	var output elbv2.DeregisterTargetsOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.TargetGroupArn == nil || *input.TargetGroupArn == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	if len(input.Targets) == 0 {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_elbv2.NewNATSELBv2Service(natsConn)
	result, err := svc.DeregisterTargets(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
