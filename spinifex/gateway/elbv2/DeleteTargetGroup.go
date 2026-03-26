package gateway_elbv2

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_elbv2 "github.com/mulgadc/spinifex/spinifex/handlers/elbv2"
	"github.com/nats-io/nats.go"
)

// DeleteTargetGroup handles the ELBv2 DeleteTargetGroup API call.
func DeleteTargetGroup(input *elbv2.DeleteTargetGroupInput, natsConn *nats.Conn, accountID string) (elbv2.DeleteTargetGroupOutput, error) {
	var output elbv2.DeleteTargetGroupOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.TargetGroupArn == nil || *input.TargetGroupArn == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_elbv2.NewNATSELBv2Service(natsConn)
	result, err := svc.DeleteTargetGroup(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
