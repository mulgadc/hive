package gateway_ec2_natgw

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_natgw "github.com/mulgadc/spinifex/spinifex/handlers/ec2/natgw"
	"github.com/nats-io/nats.go"
)

func CreateNatGateway(input *ec2.CreateNatGatewayInput, natsConn *nats.Conn, accountID string) (ec2.CreateNatGatewayOutput, error) {
	var output ec2.CreateNatGatewayOutput
	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	svc := handlers_ec2_natgw.NewNATSNatGatewayService(natsConn)
	result, err := svc.CreateNatGateway(input, accountID)
	if err != nil {
		return output, err
	}
	return *result, nil
}
