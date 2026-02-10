package gateway_ec2_eigw

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/stretchr/testify/assert"
)

func TestValidateCreateEgressOnlyInternetGatewayInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.CreateEgressOnlyInternetGatewayInput
		wantErr string
	}{
		{
			name:    "nil input",
			input:   nil,
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name:    "missing VpcId",
			input:   &ec2.CreateEgressOnlyInternetGatewayInput{},
			wantErr: awserrors.ErrorMissingParameter,
		},
		{
			name:    "empty VpcId",
			input:   &ec2.CreateEgressOnlyInternetGatewayInput{VpcId: aws.String("")},
			wantErr: awserrors.ErrorMissingParameter,
		},
		{
			name:    "valid input",
			input:   &ec2.CreateEgressOnlyInternetGatewayInput{VpcId: aws.String("vpc-123")},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCreateEgressOnlyInternetGatewayInput(tt.input)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
		})
	}
}

func TestValidateDeleteEgressOnlyInternetGatewayInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.DeleteEgressOnlyInternetGatewayInput
		wantErr string
	}{
		{
			name:    "nil input",
			input:   nil,
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name:    "missing ID",
			input:   &ec2.DeleteEgressOnlyInternetGatewayInput{},
			wantErr: awserrors.ErrorMissingParameter,
		},
		{
			name:    "empty ID",
			input:   &ec2.DeleteEgressOnlyInternetGatewayInput{EgressOnlyInternetGatewayId: aws.String("")},
			wantErr: awserrors.ErrorMissingParameter,
		},
		{
			name:    "valid input",
			input:   &ec2.DeleteEgressOnlyInternetGatewayInput{EgressOnlyInternetGatewayId: aws.String("eigw-abc123")},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDeleteEgressOnlyInternetGatewayInput(tt.input)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
		})
	}
}

func TestValidateDescribeEgressOnlyInternetGatewaysInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.DescribeEgressOnlyInternetGatewaysInput
		wantErr string
	}{
		{
			name:    "nil input",
			input:   nil,
			wantErr: awserrors.ErrorInvalidParameterValue,
		},
		{
			name:    "empty input",
			input:   &ec2.DescribeEgressOnlyInternetGatewaysInput{},
			wantErr: "",
		},
		{
			name: "with filter IDs",
			input: &ec2.DescribeEgressOnlyInternetGatewaysInput{
				EgressOnlyInternetGatewayIds: []*string{aws.String("eigw-abc")},
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDescribeEgressOnlyInternetGatewaysInput(tt.input)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
		})
	}
}
