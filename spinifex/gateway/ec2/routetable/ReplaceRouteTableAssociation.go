package gateway_ec2_routetable

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_routetable "github.com/mulgadc/spinifex/spinifex/handlers/ec2/routetable"
	"github.com/nats-io/nats.go"
)

func ReplaceRouteTableAssociation(input *ec2.ReplaceRouteTableAssociationInput, natsConn *nats.Conn, accountID string) (ec2.ReplaceRouteTableAssociationOutput, error) {
	var output ec2.ReplaceRouteTableAssociationOutput
	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	svc := handlers_ec2_routetable.NewNATSRouteTableService(natsConn)
	result, err := svc.ReplaceRouteTableAssociation(input, accountID)
	if err != nil {
		return output, err
	}
	return *result, nil
}
