package gateway_elbv2

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_elbv2 "github.com/mulgadc/spinifex/spinifex/handlers/elbv2"
	"github.com/nats-io/nats.go"
)

// CreateTargetGroup handles the ELBv2 CreateTargetGroup API call.
func CreateTargetGroup(input *elbv2.CreateTargetGroupInput, natsConn *nats.Conn, accountID string) (elbv2.CreateTargetGroupOutput, error) {
	var output elbv2.CreateTargetGroupOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.Name == nil || *input.Name == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_elbv2.NewNATSELBv2Service(natsConn)
	result, err := svc.CreateTargetGroup(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
