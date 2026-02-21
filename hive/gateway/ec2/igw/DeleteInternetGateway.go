package gateway_ec2_igw

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_igw "github.com/mulgadc/hive/hive/handlers/ec2/igw"
	"github.com/nats-io/nats.go"
)

// DeleteInternetGateway handles the EC2 DeleteInternetGateway API call
func DeleteInternetGateway(input *ec2.DeleteInternetGatewayInput, natsConn *nats.Conn) (ec2.DeleteInternetGatewayOutput, error) {
	var output ec2.DeleteInternetGatewayOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.InternetGatewayId == nil || *input.InternetGatewayId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}

	svc := handlers_ec2_igw.NewNATSIGWService(natsConn)
	result, err := svc.DeleteInternetGateway(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
