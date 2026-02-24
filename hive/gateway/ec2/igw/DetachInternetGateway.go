package gateway_ec2_igw

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_igw "github.com/mulgadc/hive/hive/handlers/ec2/igw"
	"github.com/nats-io/nats.go"
)

// DetachInternetGateway handles the EC2 DetachInternetGateway API call
func DetachInternetGateway(input *ec2.DetachInternetGatewayInput, natsConn *nats.Conn) (ec2.DetachInternetGatewayOutput, error) {
	var output ec2.DetachInternetGatewayOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.InternetGatewayId == nil || *input.InternetGatewayId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.VpcId == nil || *input.VpcId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_igw.NewNATSIGWService(natsConn)
	result, err := svc.DetachInternetGateway(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
