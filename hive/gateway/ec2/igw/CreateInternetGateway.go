package gateway_ec2_igw

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_igw "github.com/mulgadc/hive/hive/handlers/ec2/igw"
	"github.com/nats-io/nats.go"
)

// CreateInternetGateway handles the EC2 CreateInternetGateway API call
func CreateInternetGateway(input *ec2.CreateInternetGatewayInput, natsConn *nats.Conn) (ec2.CreateInternetGatewayOutput, error) {
	var output ec2.CreateInternetGatewayOutput

	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	svc := handlers_ec2_igw.NewNATSIGWService(natsConn)
	result, err := svc.CreateInternetGateway(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
