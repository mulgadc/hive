package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// ModifyNetworkInterfaceAttribute handles the EC2 ModifyNetworkInterfaceAttribute API call
func ModifyNetworkInterfaceAttribute(input *ec2.ModifyNetworkInterfaceAttributeInput, natsConn *nats.Conn, accountID string) (ec2.ModifyNetworkInterfaceAttributeOutput, error) {
	var output ec2.ModifyNetworkInterfaceAttributeOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.NetworkInterfaceId == nil || *input.NetworkInterfaceId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.ModifyNetworkInterfaceAttribute(input, accountID)
	if err != nil {
		return output, err
	}

	return *result, nil
}
