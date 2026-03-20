package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// ModifySubnetAttribute handles the EC2 ModifySubnetAttribute API call
func ModifySubnetAttribute(input *ec2.ModifySubnetAttributeInput, natsConn *nats.Conn, accountID string) (ec2.ModifySubnetAttributeOutput, error) {
	var output ec2.ModifySubnetAttributeOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.SubnetId == nil || *input.SubnetId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.ModifySubnetAttribute(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
