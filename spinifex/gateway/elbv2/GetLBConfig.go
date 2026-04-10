package gateway_elbv2

import (
	"errors"

	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_elbv2 "github.com/mulgadc/spinifex/spinifex/handlers/elbv2"
	"github.com/nats-io/nats.go"
)

// GetLBConfig handles the ELBv2 GetLBConfig API call.
func GetLBConfig(input *handlers_elbv2.GetLBConfigInput, natsConn *nats.Conn, accountID string) (handlers_elbv2.GetLBConfigOutput, error) {
	var output handlers_elbv2.GetLBConfigOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.LBID == nil || *input.LBID == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_elbv2.NewNATSELBv2Service(natsConn)
	result, err := svc.GetLBConfig(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
