package gateway_ec2_volume

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestDescribeVolumeStatus_InputValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.DescribeVolumeStatusInput
		wantErr bool
		errMsg  string
	}{
		{
			name: "InvalidVolumeId_NoPrefix",
			input: &ec2.DescribeVolumeStatusInput{
				VolumeIds: []*string{aws.String("invalid-id")},
			},
			wantErr: true,
			errMsg:  "InvalidVolumeID.Malformed",
		},
		{
			name: "InvalidVolumeId_WrongPrefix",
			input: &ec2.DescribeVolumeStatusInput{
				VolumeIds: []*string{aws.String("ami-0123456789abcdef0")},
			},
			wantErr: true,
			errMsg:  "InvalidVolumeID.Malformed",
		},
		{
			name: "InvalidVolumeId_EmptyString",
			input: &ec2.DescribeVolumeStatusInput{
				VolumeIds: []*string{aws.String("")},
			},
			wantErr: true,
			errMsg:  "InvalidVolumeID.Malformed",
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
			errMsg:  "InvalidVolumeID.Malformed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DescribeVolumeStatus(tt.input, nil)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
