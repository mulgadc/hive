package gateway_ec2_vpc

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/stretchr/testify/assert"
)

func TestCreateVpc_NilInput(t *testing.T) {
	_, err := CreateVpc(nil, nil)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateVpc_NilCidrBlock(t *testing.T) {
	_, err := CreateVpc(&ec2.CreateVpcInput{}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateVpc_EmptyCidrBlock(t *testing.T) {
	_, err := CreateVpc(&ec2.CreateVpcInput{CidrBlock: aws.String("")}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteVpc_NilInput(t *testing.T) {
	_, err := DeleteVpc(nil, nil)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteVpc_NilVpcId(t *testing.T) {
	_, err := DeleteVpc(&ec2.DeleteVpcInput{}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteVpc_EmptyVpcId(t *testing.T) {
	_, err := DeleteVpc(&ec2.DeleteVpcInput{VpcId: aws.String("")}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateSubnet_NilInput(t *testing.T) {
	_, err := CreateSubnet(nil, nil)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateSubnet_NilVpcId(t *testing.T) {
	_, err := CreateSubnet(&ec2.CreateSubnetInput{CidrBlock: aws.String("10.0.0.0/24")}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateSubnet_EmptyVpcId(t *testing.T) {
	_, err := CreateSubnet(&ec2.CreateSubnetInput{VpcId: aws.String(""), CidrBlock: aws.String("10.0.0.0/24")}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateSubnet_NilCidrBlock(t *testing.T) {
	_, err := CreateSubnet(&ec2.CreateSubnetInput{VpcId: aws.String("vpc-123")}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateSubnet_EmptyCidrBlock(t *testing.T) {
	_, err := CreateSubnet(&ec2.CreateSubnetInput{VpcId: aws.String("vpc-123"), CidrBlock: aws.String("")}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteSubnet_NilInput(t *testing.T) {
	_, err := DeleteSubnet(nil, nil)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteSubnet_NilSubnetId(t *testing.T) {
	_, err := DeleteSubnet(&ec2.DeleteSubnetInput{}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteSubnet_EmptySubnetId(t *testing.T) {
	_, err := DeleteSubnet(&ec2.DeleteSubnetInput{SubnetId: aws.String("")}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateNetworkInterface_NilInput(t *testing.T) {
	_, err := CreateNetworkInterface(nil, nil)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateNetworkInterface_NilSubnetId(t *testing.T) {
	_, err := CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateNetworkInterface_EmptySubnetId(t *testing.T) {
	_, err := CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{SubnetId: aws.String("")}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteNetworkInterface_NilInput(t *testing.T) {
	_, err := DeleteNetworkInterface(nil, nil)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteNetworkInterface_NilNetworkInterfaceId(t *testing.T) {
	_, err := DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteNetworkInterface_EmptyNetworkInterfaceId(t *testing.T) {
	_, err := DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: aws.String("")}, nil)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}
