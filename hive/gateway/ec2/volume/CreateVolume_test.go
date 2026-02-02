package gateway_ec2_volume

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestValidateCreateVolumeInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.CreateVolumeInput
		wantErr bool
		errMsg  string
	}{
		{
			name:    "NilInput",
			input:   nil,
			wantErr: true,
			errMsg:  "InvalidParameterValue",
		},
		{
			name:    "EmptyInput",
			input:   &ec2.CreateVolumeInput{},
			wantErr: true,
			errMsg:  "InvalidParameterValue",
		},
		{
			name: "ValidInput",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
			wantErr: false,
		},
		{
			name: "ValidInput_WithGP3",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String("ap-southeast-2a"),
				VolumeType:       aws.String("gp3"),
			},
			wantErr: false,
		},
		{
			name: "InvalidSize_Zero",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(0),
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
			wantErr: true,
			errMsg:  "InvalidParameterValue",
		},
		{
			name: "InvalidSize_Negative",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(-10),
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
			wantErr: true,
			errMsg:  "InvalidParameterValue",
		},
		{
			name: "InvalidSize_Nil",
			input: &ec2.CreateVolumeInput{
				AvailabilityZone: aws.String("ap-southeast-2a"),
			},
			wantErr: true,
			errMsg:  "InvalidParameterValue",
		},
		{
			name: "InvalidAZ_Empty",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String(""),
			},
			wantErr: true,
			errMsg:  "InvalidParameterValue",
		},
		{
			name: "InvalidAZ_Nil",
			input: &ec2.CreateVolumeInput{
				Size: aws.Int64(80),
			},
			wantErr: true,
			errMsg:  "InvalidParameterValue",
		},
		{
			name: "InvalidVolumeType_IO1",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String("ap-southeast-2a"),
				VolumeType:       aws.String("io1"),
			},
			wantErr: true,
			errMsg:  "InvalidParameterValue",
		},
		{
			name: "InvalidVolumeType_GP2",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String("ap-southeast-2a"),
				VolumeType:       aws.String("gp2"),
			},
			wantErr: true,
			errMsg:  "InvalidParameterValue",
		},
		{
			name: "ValidInput_EmptyVolumeType",
			input: &ec2.CreateVolumeInput{
				Size:             aws.Int64(80),
				AvailabilityZone: aws.String("ap-southeast-2a"),
				VolumeType:       aws.String(""),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCreateVolumeInput(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
