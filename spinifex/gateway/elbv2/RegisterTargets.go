package gateway_elbv2

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_elbv2 "github.com/mulgadc/spinifex/spinifex/handlers/elbv2"
	"github.com/nats-io/nats.go"
)

// RegisterTargets handles the ELBv2 RegisterTargets API call.
func RegisterTargets(input *elbv2.RegisterTargetsInput, natsConn *nats.Conn, accountID string) (elbv2.RegisterTargetsOutput, error) {
	var output elbv2.RegisterTargetsOutput

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
	result, err := svc.RegisterTargets(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
