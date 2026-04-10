package gateway_elbv2

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_elbv2 "github.com/mulgadc/spinifex/spinifex/handlers/elbv2"
	"github.com/nats-io/nats.go"
)

// ModifyLoadBalancerAttributes handles the ELBv2 ModifyLoadBalancerAttributes API call.
func ModifyLoadBalancerAttributes(input *elbv2.ModifyLoadBalancerAttributesInput, natsConn *nats.Conn, accountID string) (elbv2.ModifyLoadBalancerAttributesOutput, error) {
	var output elbv2.ModifyLoadBalancerAttributesOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.LoadBalancerArn == nil || *input.LoadBalancerArn == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	if len(input.Attributes) == 0 {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_elbv2.NewNATSELBv2Service(natsConn)
	result, err := svc.ModifyLoadBalancerAttributes(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
