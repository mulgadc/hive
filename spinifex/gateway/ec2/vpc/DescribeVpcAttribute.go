package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// DescribeVpcAttribute handles the EC2 DescribeVpcAttribute API call
func DescribeVpcAttribute(input *ec2.DescribeVpcAttributeInput, natsConn *nats.Conn, accountID string) (ec2.DescribeVpcAttributeOutput, error) {
	var output ec2.DescribeVpcAttributeOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.VpcId == nil || *input.VpcId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.Attribute == nil || *input.Attribute == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.DescribeVpcAttribute(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
