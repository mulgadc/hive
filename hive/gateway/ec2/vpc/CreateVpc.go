package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/hive/hive/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// CreateVpc handles the EC2 CreateVpc API call
func CreateVpc(input *ec2.CreateVpcInput, natsConn *nats.Conn) (ec2.CreateVpcOutput, error) {
	var output ec2.CreateVpcOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.CidrBlock == nil || *input.CidrBlock == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.CreateVpc(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
