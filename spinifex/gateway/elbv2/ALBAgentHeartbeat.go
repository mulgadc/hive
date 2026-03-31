package gateway_elbv2

import (
	"errors"

	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_elbv2 "github.com/mulgadc/spinifex/spinifex/handlers/elbv2"
	"github.com/nats-io/nats.go"
)

// ALBAgentHeartbeat handles the ELBv2 ALBAgentHeartbeat API call.
func ALBAgentHeartbeat(input *handlers_elbv2.ALBAgentHeartbeatInput, natsConn *nats.Conn, accountID string) (handlers_elbv2.ALBAgentHeartbeatOutput, error) {
	var output handlers_elbv2.ALBAgentHeartbeatOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.LBID == nil || *input.LBID == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_elbv2.NewNATSELBv2Service(natsConn)
	result, err := svc.ALBAgentHeartbeat(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
