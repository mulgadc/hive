package gateway_ec2_vpc

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/stretchr/testify/assert"
)

const testAccountID = "123456789012"

func TestCreateVpc_NilInput(t *testing.T) {
	_, err := CreateVpc(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateVpc_NilCidrBlock(t *testing.T) {
	_, err := CreateVpc(&ec2.CreateVpcInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateVpc_EmptyCidrBlock(t *testing.T) {
	_, err := CreateVpc(&ec2.CreateVpcInput{CidrBlock: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteVpc_NilInput(t *testing.T) {
	_, err := DeleteVpc(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteVpc_NilVpcId(t *testing.T) {
	_, err := DeleteVpc(&ec2.DeleteVpcInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteVpc_EmptyVpcId(t *testing.T) {
	_, err := DeleteVpc(&ec2.DeleteVpcInput{VpcId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateSubnet_NilInput(t *testing.T) {
	_, err := CreateSubnet(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateSubnet_NilVpcId(t *testing.T) {
	_, err := CreateSubnet(&ec2.CreateSubnetInput{CidrBlock: aws.String("10.0.0.0/24")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateSubnet_EmptyVpcId(t *testing.T) {
	_, err := CreateSubnet(&ec2.CreateSubnetInput{VpcId: aws.String(""), CidrBlock: aws.String("10.0.0.0/24")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateSubnet_NilCidrBlock(t *testing.T) {
	_, err := CreateSubnet(&ec2.CreateSubnetInput{VpcId: aws.String("vpc-123")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateSubnet_EmptyCidrBlock(t *testing.T) {
	_, err := CreateSubnet(&ec2.CreateSubnetInput{VpcId: aws.String("vpc-123"), CidrBlock: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteSubnet_NilInput(t *testing.T) {
	_, err := DeleteSubnet(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteSubnet_NilSubnetId(t *testing.T) {
	_, err := DeleteSubnet(&ec2.DeleteSubnetInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteSubnet_EmptySubnetId(t *testing.T) {
	_, err := DeleteSubnet(&ec2.DeleteSubnetInput{SubnetId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateNetworkInterface_NilInput(t *testing.T) {
	_, err := CreateNetworkInterface(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateNetworkInterface_NilSubnetId(t *testing.T) {
	_, err := CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateNetworkInterface_EmptySubnetId(t *testing.T) {
	_, err := CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{SubnetId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteNetworkInterface_NilInput(t *testing.T) {
	_, err := DeleteNetworkInterface(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteNetworkInterface_NilNetworkInterfaceId(t *testing.T) {
	_, err := DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteNetworkInterface_EmptyNetworkInterfaceId(t *testing.T) {
	_, err := DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

// Handler tests with valid input + nil NATS — covers NATS error paths

func TestCreateVpc_NilNATS(t *testing.T) {
	_, err := CreateVpc(&ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")}, nil, testAccountID)
	assert.Error(t, err)
}

func TestDeleteVpc_NilNATS(t *testing.T) {
	_, err := DeleteVpc(&ec2.DeleteVpcInput{VpcId: aws.String("vpc-123")}, nil, testAccountID)
	assert.Error(t, err)
}

func TestCreateSubnet_NilNATS(t *testing.T) {
	_, err := CreateSubnet(&ec2.CreateSubnetInput{VpcId: aws.String("vpc-123"), CidrBlock: aws.String("10.0.1.0/24")}, nil, testAccountID)
	assert.Error(t, err)
}

func TestDeleteSubnet_NilNATS(t *testing.T) {
	_, err := DeleteSubnet(&ec2.DeleteSubnetInput{SubnetId: aws.String("subnet-123")}, nil, testAccountID)
	assert.Error(t, err)
}

func TestCreateNetworkInterface_NilNATS(t *testing.T) {
	_, err := CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{SubnetId: aws.String("subnet-123")}, nil, testAccountID)
	assert.Error(t, err)
}

func TestDeleteNetworkInterface_NilNATS(t *testing.T) {
	_, err := DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: aws.String("eni-123")}, nil, testAccountID)
	assert.Error(t, err)
}

func TestDescribeVpcs_NilNATS(t *testing.T) {
	_, err := DescribeVpcs(nil, nil, testAccountID)
	assert.Error(t, err)

	_, err = DescribeVpcs(&ec2.DescribeVpcsInput{}, nil, testAccountID)
	assert.Error(t, err)
}

func TestDescribeSubnets_NilNATS(t *testing.T) {
	_, err := DescribeSubnets(nil, nil, testAccountID)
	assert.Error(t, err)

	_, err = DescribeSubnets(&ec2.DescribeSubnetsInput{}, nil, testAccountID)
	assert.Error(t, err)
}

func TestDescribeNetworkInterfaces_NilNATS(t *testing.T) {
	_, err := DescribeNetworkInterfaces(nil, nil, testAccountID)
	assert.Error(t, err)

	_, err = DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// ModifyVpcAttribute tests

func TestModifyVpcAttribute_NilInput(t *testing.T) {
	_, err := ModifyVpcAttribute(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestModifyVpcAttribute_NilVpcId(t *testing.T) {
	_, err := ModifyVpcAttribute(&ec2.ModifyVpcAttributeInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestModifyVpcAttribute_EmptyVpcId(t *testing.T) {
	_, err := ModifyVpcAttribute(&ec2.ModifyVpcAttributeInput{VpcId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestModifyVpcAttribute_NilNATS(t *testing.T) {
	_, err := ModifyVpcAttribute(&ec2.ModifyVpcAttributeInput{VpcId: aws.String("vpc-123")}, nil, testAccountID)
	assert.Error(t, err)
}

// DescribeVpcAttribute tests

func TestDescribeVpcAttribute_NilInput(t *testing.T) {
	_, err := DescribeVpcAttribute(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDescribeVpcAttribute_NilVpcId(t *testing.T) {
	_, err := DescribeVpcAttribute(&ec2.DescribeVpcAttributeInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDescribeVpcAttribute_EmptyVpcId(t *testing.T) {
	_, err := DescribeVpcAttribute(&ec2.DescribeVpcAttributeInput{VpcId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDescribeVpcAttribute_MissingAttribute(t *testing.T) {
	_, err := DescribeVpcAttribute(&ec2.DescribeVpcAttributeInput{VpcId: aws.String("vpc-123")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDescribeVpcAttribute_EmptyAttribute(t *testing.T) {
	_, err := DescribeVpcAttribute(&ec2.DescribeVpcAttributeInput{VpcId: aws.String("vpc-123"), Attribute: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDescribeVpcAttribute_NilNATS(t *testing.T) {
	_, err := DescribeVpcAttribute(&ec2.DescribeVpcAttributeInput{VpcId: aws.String("vpc-123"), Attribute: aws.String(ec2.VpcAttributeNameEnableDnsSupport)}, nil, testAccountID)
	assert.Error(t, err)
}

// ModifySubnetAttribute tests

func TestModifySubnetAttribute_NilInput(t *testing.T) {
	_, err := ModifySubnetAttribute(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestModifySubnetAttribute_NilSubnetId(t *testing.T) {
	_, err := ModifySubnetAttribute(&ec2.ModifySubnetAttributeInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestModifySubnetAttribute_EmptySubnetId(t *testing.T) {
	_, err := ModifySubnetAttribute(&ec2.ModifySubnetAttributeInput{SubnetId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestModifySubnetAttribute_NilNATS(t *testing.T) {
	_, err := ModifySubnetAttribute(&ec2.ModifySubnetAttributeInput{SubnetId: aws.String("subnet-123")}, nil, testAccountID)
	assert.Error(t, err)
}

// CreateSecurityGroup tests

func TestCreateSecurityGroup_NilInput(t *testing.T) {
	_, err := CreateSecurityGroup(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestCreateSecurityGroup_NilGroupName(t *testing.T) {
	_, err := CreateSecurityGroup(&ec2.CreateSecurityGroupInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateSecurityGroup_EmptyGroupName(t *testing.T) {
	_, err := CreateSecurityGroup(&ec2.CreateSecurityGroupInput{GroupName: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestCreateSecurityGroup_NilNATS(t *testing.T) {
	_, err := CreateSecurityGroup(&ec2.CreateSecurityGroupInput{GroupName: aws.String("my-sg")}, nil, testAccountID)
	assert.Error(t, err)
}

// DeleteSecurityGroup tests

func TestDeleteSecurityGroup_NilInput(t *testing.T) {
	_, err := DeleteSecurityGroup(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteSecurityGroup_NilGroupId(t *testing.T) {
	_, err := DeleteSecurityGroup(&ec2.DeleteSecurityGroupInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteSecurityGroup_EmptyGroupId(t *testing.T) {
	_, err := DeleteSecurityGroup(&ec2.DeleteSecurityGroupInput{GroupId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDeleteSecurityGroup_NilNATS(t *testing.T) {
	_, err := DeleteSecurityGroup(&ec2.DeleteSecurityGroupInput{GroupId: aws.String("sg-123")}, nil, testAccountID)
	assert.Error(t, err)
}

// DescribeSecurityGroups tests

func TestDescribeSecurityGroups_NilInput(t *testing.T) {
	_, err := DescribeSecurityGroups(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDescribeSecurityGroups_NilNATS(t *testing.T) {
	_, err := DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{}, nil, testAccountID)
	assert.Error(t, err)
}

// AuthorizeSecurityGroupIngress tests

func TestAuthorizeSecurityGroupIngress_NilInput(t *testing.T) {
	_, err := AuthorizeSecurityGroupIngress(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestAuthorizeSecurityGroupIngress_NilGroupId(t *testing.T) {
	_, err := AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestAuthorizeSecurityGroupIngress_EmptyGroupId(t *testing.T) {
	_, err := AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{GroupId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestAuthorizeSecurityGroupIngress_NilNATS(t *testing.T) {
	_, err := AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{GroupId: aws.String("sg-123")}, nil, testAccountID)
	assert.Error(t, err)
}

// AuthorizeSecurityGroupEgress tests

func TestAuthorizeSecurityGroupEgress_NilInput(t *testing.T) {
	_, err := AuthorizeSecurityGroupEgress(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestAuthorizeSecurityGroupEgress_NilGroupId(t *testing.T) {
	_, err := AuthorizeSecurityGroupEgress(&ec2.AuthorizeSecurityGroupEgressInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestAuthorizeSecurityGroupEgress_EmptyGroupId(t *testing.T) {
	_, err := AuthorizeSecurityGroupEgress(&ec2.AuthorizeSecurityGroupEgressInput{GroupId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestAuthorizeSecurityGroupEgress_NilNATS(t *testing.T) {
	_, err := AuthorizeSecurityGroupEgress(&ec2.AuthorizeSecurityGroupEgressInput{GroupId: aws.String("sg-123")}, nil, testAccountID)
	assert.Error(t, err)
}

// RevokeSecurityGroupIngress tests

func TestRevokeSecurityGroupIngress_NilInput(t *testing.T) {
	_, err := RevokeSecurityGroupIngress(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestRevokeSecurityGroupIngress_NilGroupId(t *testing.T) {
	_, err := RevokeSecurityGroupIngress(&ec2.RevokeSecurityGroupIngressInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestRevokeSecurityGroupIngress_EmptyGroupId(t *testing.T) {
	_, err := RevokeSecurityGroupIngress(&ec2.RevokeSecurityGroupIngressInput{GroupId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestRevokeSecurityGroupIngress_NilNATS(t *testing.T) {
	_, err := RevokeSecurityGroupIngress(&ec2.RevokeSecurityGroupIngressInput{GroupId: aws.String("sg-123")}, nil, testAccountID)
	assert.Error(t, err)
}

// RevokeSecurityGroupEgress tests

func TestRevokeSecurityGroupEgress_NilInput(t *testing.T) {
	_, err := RevokeSecurityGroupEgress(nil, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestRevokeSecurityGroupEgress_NilGroupId(t *testing.T) {
	_, err := RevokeSecurityGroupEgress(&ec2.RevokeSecurityGroupEgressInput{}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestRevokeSecurityGroupEgress_EmptyGroupId(t *testing.T) {
	_, err := RevokeSecurityGroupEgress(&ec2.RevokeSecurityGroupEgressInput{GroupId: aws.String("")}, nil, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestRevokeSecurityGroupEgress_NilNATS(t *testing.T) {
	_, err := RevokeSecurityGroupEgress(&ec2.RevokeSecurityGroupEgressInput{GroupId: aws.String("sg-123")}, nil, testAccountID)
	assert.Error(t, err)
}
