package gateway_ec2_eigw

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_eigw "github.com/mulgadc/hive/hive/handlers/ec2/eigw"
	"github.com/nats-io/nats.go"
)

// ValidateDescribeEgressOnlyInternetGatewaysInput validates the input parameters
func ValidateDescribeEgressOnlyInternetGatewaysInput(input *ec2.DescribeEgressOnlyInternetGatewaysInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	return nil
}

// DescribeEgressOnlyInternetGateways handles the EC2 DescribeEgressOnlyInternetGateways API call
func DescribeEgressOnlyInternetGateways(input *ec2.DescribeEgressOnlyInternetGatewaysInput, natsConn *nats.Conn) (ec2.DescribeEgressOnlyInternetGatewaysOutput, error) {
	var output ec2.DescribeEgressOnlyInternetGatewaysOutput

	if err := ValidateDescribeEgressOnlyInternetGatewaysInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_eigw.NewNATSEgressOnlyIGWService(natsConn)
	result, err := svc.DescribeEgressOnlyInternetGateways(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
