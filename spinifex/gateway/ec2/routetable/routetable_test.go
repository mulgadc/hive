package gateway_ec2_routetable

import (
	"testing"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/stretchr/testify/assert"
)

const testAccountID = "123456789012"

// CreateRouteTable tests

func TestCreateRouteTable_NilInput(t *testing.T) {
	_, err := CreateRouteTable(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateRouteTable_NilNATS(t *testing.T) {
	_, err := CreateRouteTable(&ec2.CreateRouteTableInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// DeleteRouteTable tests

func TestDeleteRouteTable_NilInput(t *testing.T) {
	_, err := DeleteRouteTable(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteRouteTable_NilNATS(t *testing.T) {
	_, err := DeleteRouteTable(&ec2.DeleteRouteTableInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// DescribeRouteTables tests

func TestDescribeRouteTables_NilInput(t *testing.T) {
	_, err := DescribeRouteTables(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDescribeRouteTables_NilNATS(t *testing.T) {
	_, err := DescribeRouteTables(&ec2.DescribeRouteTablesInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// CreateRoute tests

func TestCreateRoute_NilInput(t *testing.T) {
	_, err := CreateRoute(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateRoute_NilNATS(t *testing.T) {
	_, err := CreateRoute(&ec2.CreateRouteInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// DeleteRoute tests

func TestDeleteRoute_NilInput(t *testing.T) {
	_, err := DeleteRoute(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteRoute_NilNATS(t *testing.T) {
	_, err := DeleteRoute(&ec2.DeleteRouteInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// ReplaceRoute tests

func TestReplaceRoute_NilInput(t *testing.T) {
	_, err := ReplaceRoute(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestReplaceRoute_NilNATS(t *testing.T) {
	_, err := ReplaceRoute(&ec2.ReplaceRouteInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// AssociateRouteTable tests

func TestAssociateRouteTable_NilInput(t *testing.T) {
	_, err := AssociateRouteTable(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestAssociateRouteTable_NilNATS(t *testing.T) {
	_, err := AssociateRouteTable(&ec2.AssociateRouteTableInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// DisassociateRouteTable tests

func TestDisassociateRouteTable_NilInput(t *testing.T) {
	_, err := DisassociateRouteTable(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDisassociateRouteTable_NilNATS(t *testing.T) {
	_, err := DisassociateRouteTable(&ec2.DisassociateRouteTableInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// ReplaceRouteTableAssociation tests

func TestReplaceRouteTableAssociation_NilInput(t *testing.T) {
	_, err := ReplaceRouteTableAssociation(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestReplaceRouteTableAssociation_NilNATS(t *testing.T) {
	_, err := ReplaceRouteTableAssociation(&ec2.ReplaceRouteTableAssociationInput{}, nil, testAccountID)
	assert.Error(t, err)
}
