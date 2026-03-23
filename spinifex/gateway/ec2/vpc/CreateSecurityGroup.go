package gateway_ec2_vpc

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

func CreateSecurityGroup(input *ec2.CreateSecurityGroupInput, natsConn *nats.Conn, accountID string) (ec2.CreateSecurityGroupOutput, error) {
	var output ec2.CreateSecurityGroupOutput
	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.GroupName == nil || *input.GroupName == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.CreateSecurityGroup(input, accountID)
	if err != nil {
		return output, err
	}
	return *result, nil
}

func DeleteSecurityGroup(input *ec2.DeleteSecurityGroupInput, natsConn *nats.Conn, accountID string) (ec2.DeleteSecurityGroupOutput, error) {
	var output ec2.DeleteSecurityGroupOutput
	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.GroupId == nil || *input.GroupId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.DeleteSecurityGroup(input, accountID)
	if err != nil {
		return output, err
	}
	return *result, nil
}

func DescribeSecurityGroups(input *ec2.DescribeSecurityGroupsInput, natsConn *nats.Conn, accountID string) (ec2.DescribeSecurityGroupsOutput, error) {
	var output ec2.DescribeSecurityGroupsOutput
	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.DescribeSecurityGroups(input, accountID)
	if err != nil {
		return output, err
	}
	return *result, nil
}

func AuthorizeSecurityGroupIngress(input *ec2.AuthorizeSecurityGroupIngressInput, natsConn *nats.Conn, accountID string) (ec2.AuthorizeSecurityGroupIngressOutput, error) {
	var output ec2.AuthorizeSecurityGroupIngressOutput
	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.GroupId == nil || *input.GroupId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.AuthorizeSecurityGroupIngress(input, accountID)
	if err != nil {
		return output, err
	}
	return *result, nil
}

func AuthorizeSecurityGroupEgress(input *ec2.AuthorizeSecurityGroupEgressInput, natsConn *nats.Conn, accountID string) (ec2.AuthorizeSecurityGroupEgressOutput, error) {
	var output ec2.AuthorizeSecurityGroupEgressOutput
	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.GroupId == nil || *input.GroupId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.AuthorizeSecurityGroupEgress(input, accountID)
	if err != nil {
		return output, err
	}
	return *result, nil
}

func RevokeSecurityGroupIngress(input *ec2.RevokeSecurityGroupIngressInput, natsConn *nats.Conn, accountID string) (ec2.RevokeSecurityGroupIngressOutput, error) {
	var output ec2.RevokeSecurityGroupIngressOutput
	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.GroupId == nil || *input.GroupId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.RevokeSecurityGroupIngress(input, accountID)
	if err != nil {
		return output, err
	}
	return *result, nil
}

func RevokeSecurityGroupEgress(input *ec2.RevokeSecurityGroupEgressInput, natsConn *nats.Conn, accountID string) (ec2.RevokeSecurityGroupEgressOutput, error) {
	var output ec2.RevokeSecurityGroupEgressOutput
	if input == nil {
		return output, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.GroupId == nil || *input.GroupId == "" {
		return output, errors.New(awserrors.ErrorMissingParameter)
	}
	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.RevokeSecurityGroupEgress(input, accountID)
	if err != nil {
		return output, err
	}
	return *result, nil
}
