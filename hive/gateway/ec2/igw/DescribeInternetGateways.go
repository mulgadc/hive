package gateway_ec2_igw

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_igw "github.com/mulgadc/hive/hive/handlers/ec2/igw"
	"github.com/nats-io/nats.go"
)

// DescribeInternetGateways handles the EC2 DescribeInternetGateways API call
func DescribeInternetGateways(input *ec2.DescribeInternetGatewaysInput, natsConn *nats.Conn) (ec2.DescribeInternetGatewaysOutput, error) {
	var output ec2.DescribeInternetGatewaysOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	svc := handlers_ec2_igw.NewNATSIGWService(natsConn)
	result, err := svc.DescribeInternetGateways(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
