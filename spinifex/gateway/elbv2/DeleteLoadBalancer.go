package gateway_elbv2

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_elbv2 "github.com/mulgadc/spinifex/spinifex/handlers/elbv2"
	"github.com/nats-io/nats.go"
)

// DeleteLoadBalancer handles the ELBv2 DeleteLoadBalancer API call.
func DeleteLoadBalancer(input *elbv2.DeleteLoadBalancerInput, natsConn *nats.Conn, accountID string) (elbv2.DeleteLoadBalancerOutput, error) {
	var output elbv2.DeleteLoadBalancerOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.LoadBalancerArn == nil || *input.LoadBalancerArn == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_elbv2.NewNATSELBv2Service(natsConn)
	result, err := svc.DeleteLoadBalancer(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
