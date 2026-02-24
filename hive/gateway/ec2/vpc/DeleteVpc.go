package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/hive/hive/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// DeleteVpc handles the EC2 DeleteVpc API call
func DeleteVpc(input *ec2.DeleteVpcInput, natsConn *nats.Conn) (ec2.DeleteVpcOutput, error) {
	var output ec2.DeleteVpcOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.VpcId == nil || *input.VpcId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.DeleteVpc(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
