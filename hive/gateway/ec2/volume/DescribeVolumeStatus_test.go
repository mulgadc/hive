package gateway_ec2_volume

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestValidateDescribeVolumeStatusInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.DescribeVolumeStatusInput
		wantErr bool
		errMsg  string
	}{
		{
			name:    "NilInput",
			input:   nil,
			wantErr: false,
		},
		{
			name:    "EmptyInput",
			input:   &ec2.DescribeVolumeStatusInput{},
			wantErr: false,
		},
		{
			name: "ValidSingleVolumeId",
			input: &ec2.DescribeVolumeStatusInput{
				VolumeIds: []*string{aws.String("vol-0123456789abcdef0")},
			},
			wantErr: false,
		},
		{
			name: "ValidMultipleVolumeIds",
			input: &ec2.DescribeVolumeStatusInput{
				VolumeIds: []*string{
					aws.String("vol-abc123"),
					aws.String("vol-def456"),
					aws.String("vol-ghi789"),
				},
			},
			wantErr: false,
		},
		{
			name: "InvalidVolumeId_NoPrefix",
			input: &ec2.DescribeVolumeStatusInput{
				VolumeIds: []*string{aws.String("invalid-id")},
			},
			wantErr: true,
			errMsg:  "InvalidVolume.Malformed",
		},
		{
			name: "InvalidVolumeId_WrongPrefix",
			input: &ec2.DescribeVolumeStatusInput{
				VolumeIds: []*string{aws.String("ami-0123456789abcdef0")},
			},
			wantErr: true,
			errMsg:  "InvalidVolume.Malformed",
		},
		{
			name: "InvalidVolumeId_EmptyString",
			input: &ec2.DescribeVolumeStatusInput{
				VolumeIds: []*string{aws.String("")},
			},
			wantErr: true,
			errMsg:  "InvalidVolume.Malformed",
		},
		{
			name: "MixedValidAndInvalid",
			input: &ec2.DescribeVolumeStatusInput{
				VolumeIds: []*string{
					aws.String("vol-valid123"),
					aws.String("invalid-id"),
				},
			},
			wantErr: true,
			errMsg:  "InvalidVolume.Malformed",
		},
		{
			name: "NilVolumeIdInList",
			input: &ec2.DescribeVolumeStatusInput{
				VolumeIds: []*string{nil, aws.String("vol-abc123")},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDescribeVolumeStatusInput(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
