package gateway_ec2_natgw

import (
	"testing"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/stretchr/testify/assert"
)

const testAccountID = "123456789012"

// CreateNatGateway tests

func TestCreateNatGateway_NilInput(t *testing.T) {
	_, err := CreateNatGateway(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateNatGateway_NilNATS(t *testing.T) {
	_, err := CreateNatGateway(&ec2.CreateNatGatewayInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// DeleteNatGateway tests

func TestDeleteNatGateway_NilInput(t *testing.T) {
	_, err := DeleteNatGateway(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteNatGateway_NilNATS(t *testing.T) {
	_, err := DeleteNatGateway(&ec2.DeleteNatGatewayInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// DescribeNatGateways tests

func TestDescribeNatGateways_NilInput(t *testing.T) {
	_, err := DescribeNatGateways(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDescribeNatGateways_NilNATS(t *testing.T) {
	_, err := DescribeNatGateways(&ec2.DescribeNatGatewaysInput{}, nil, testAccountID)
	assert.Error(t, err)
}
