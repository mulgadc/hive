package gateway_ec2_volume

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/stretchr/testify/assert"
)

func TestValidateDeleteVolumeInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.DeleteVolumeInput
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
			name:    "EmptyInput_NoVolumeId",
			input:   &ec2.DeleteVolumeInput{},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidVolumeIDMalformed,
		},
		{
			name: "ValidVolumeId",
			input: &ec2.DeleteVolumeInput{
				VolumeId: aws.String("vol-0123456789abcdef0"),
			},
			wantErr: false,
		},
		{
			name: "ValidVolumeId_Short",
			input: &ec2.DeleteVolumeInput{
				VolumeId: aws.String("vol-abc123"),
			},
			wantErr: false,
		},
		{
			name: "InvalidVolumeId_NoPrefix",
			input: &ec2.DeleteVolumeInput{
				VolumeId: aws.String("invalid-id"),
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidVolumeIDMalformed,
		},
		{
			name: "InvalidVolumeId_WrongPrefix",
			input: &ec2.DeleteVolumeInput{
				VolumeId: aws.String("ami-0123456789abcdef0"),
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidVolumeIDMalformed,
		},
		{
			name: "InvalidVolumeId_BarePrefix",
			input: &ec2.DeleteVolumeInput{
				VolumeId: aws.String("vol-"),
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidVolumeIDMalformed,
		},
		{
			name: "InvalidVolumeId_EmptyString",
			input: &ec2.DeleteVolumeInput{
				VolumeId: aws.String(""),
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidVolumeIDMalformed,
		},
		{
			name: "InvalidVolumeId_Nil",
			input: &ec2.DeleteVolumeInput{
				VolumeId: nil,
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidVolumeIDMalformed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDeleteVolumeInput(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
