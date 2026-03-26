package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// ModifyVpcAttribute handles the EC2 ModifyVpcAttribute API call
func ModifyVpcAttribute(input *ec2.ModifyVpcAttributeInput, natsConn *nats.Conn, accountID string) (ec2.ModifyVpcAttributeOutput, error) {
	var output ec2.ModifyVpcAttributeOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.VpcId == nil || *input.VpcId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.ModifyVpcAttribute(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
