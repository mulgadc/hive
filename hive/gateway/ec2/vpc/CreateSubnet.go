package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/hive/hive/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// CreateSubnet handles the EC2 CreateSubnet API call
func CreateSubnet(input *ec2.CreateSubnetInput, natsConn *nats.Conn) (ec2.CreateSubnetOutput, error) {
	var output ec2.CreateSubnetOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.VpcId == nil || *input.VpcId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.CidrBlock == nil || *input.CidrBlock == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.CreateSubnet(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
