package gateway_elbv2

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_elbv2 "github.com/mulgadc/spinifex/spinifex/handlers/elbv2"
	"github.com/nats-io/nats.go"
)

// DeleteListener handles the ELBv2 DeleteListener API call.
func DeleteListener(input *elbv2.DeleteListenerInput, natsConn *nats.Conn, accountID string) (elbv2.DeleteListenerOutput, error) {
	var output elbv2.DeleteListenerOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.ListenerArn == nil || *input.ListenerArn == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_elbv2.NewNATSELBv2Service(natsConn)
	result, err := svc.DeleteListener(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
