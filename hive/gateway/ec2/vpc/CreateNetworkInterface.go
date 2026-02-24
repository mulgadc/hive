package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/hive/hive/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// CreateNetworkInterface handles the EC2 CreateNetworkInterface API call
func CreateNetworkInterface(input *ec2.CreateNetworkInterfaceInput, natsConn *nats.Conn) (ec2.CreateNetworkInterfaceOutput, error) {
	var output ec2.CreateNetworkInterfaceOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.SubnetId == nil || *input.SubnetId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.CreateNetworkInterface(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
