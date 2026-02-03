package gateway_ec2_volume

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/stretchr/testify/assert"
)

func TestValidateDetachVolumeInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.DetachVolumeInput
		wantErr bool
		errMsg  string
	}{
		{
			name:    "NilInput",
			input:   nil,
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidParameterValue,
		},
		{
			name:    "EmptyInput",
			input:   &ec2.DetachVolumeInput{},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "MissingVolumeId",
			input: &ec2.DetachVolumeInput{
				InstanceId: aws.String("i-1234567890abcdef0"),
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "EmptyVolumeId",
			input: &ec2.DetachVolumeInput{
				VolumeId: aws.String(""),
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "ValidInput_VolumeOnly",
			input: &ec2.DetachVolumeInput{
				VolumeId: aws.String("vol-1234567890abcdef0"),
			},
			wantErr: false,
		},
		{
			name: "ValidInput_WithInstance",
			input: &ec2.DetachVolumeInput{
				VolumeId:   aws.String("vol-1234567890abcdef0"),
				InstanceId: aws.String("i-1234567890abcdef0"),
			},
			wantErr: false,
		},
		{
			name: "ValidInput_WithForce",
			input: &ec2.DetachVolumeInput{
				VolumeId: aws.String("vol-1234567890abcdef0"),
				Force:    aws.Bool(true),
			},
			wantErr: false,
		},
		{
			name: "ValidInput_AllFields",
			input: &ec2.DetachVolumeInput{
				VolumeId:   aws.String("vol-1234567890abcdef0"),
				InstanceId: aws.String("i-1234567890abcdef0"),
				Device:     aws.String("/dev/sdf"),
				Force:      aws.Bool(true),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDetachVolumeInput(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
