package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/hive/hive/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// DeleteNetworkInterface handles the EC2 DeleteNetworkInterface API call
func DeleteNetworkInterface(input *ec2.DeleteNetworkInterfaceInput, natsConn *nats.Conn) (ec2.DeleteNetworkInterfaceOutput, error) {
	var output ec2.DeleteNetworkInterfaceOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.NetworkInterfaceId == nil || *input.NetworkInterfaceId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.DeleteNetworkInterface(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
