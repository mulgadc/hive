package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/hive/hive/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// DeleteSubnet handles the EC2 DeleteSubnet API call
func DeleteSubnet(input *ec2.DeleteSubnetInput, natsConn *nats.Conn) (ec2.DeleteSubnetOutput, error) {
	var output ec2.DeleteSubnetOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.SubnetId == nil || *input.SubnetId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.DeleteSubnet(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
