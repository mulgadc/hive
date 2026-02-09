package gateway_ec2_snapshot

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/stretchr/testify/assert"
)

func TestValidateCreateSnapshotInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.CreateSnapshotInput
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
			input:   &ec2.CreateSnapshotInput{},
			wantErr: true,
			errMsg:  awserrors.ErrorMissingParameter,
		},
		{
			name: "InvalidVolumeIdFormat",
			input: &ec2.CreateSnapshotInput{
				VolumeId: aws.String("invalid-id"),
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidVolumeIDMalformed,
		},
		{
			name: "ValidInput",
			input: &ec2.CreateSnapshotInput{
				VolumeId: aws.String("vol-1234567890abcdef0"),
			},
			wantErr: false,
		},
		{
			name: "ValidInputWithDescription",
			input: &ec2.CreateSnapshotInput{
				VolumeId:    aws.String("vol-1234567890abcdef0"),
				Description: aws.String("My snapshot"),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCreateSnapshotInput(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateDescribeSnapshotsInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.DescribeSnapshotsInput
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
			input:   &ec2.DescribeSnapshotsInput{},
			wantErr: false,
		},
		{
			name: "ValidSnapshotId",
			input: &ec2.DescribeSnapshotsInput{
				SnapshotIds: []*string{aws.String("snap-1234567890abcdef0")},
			},
			wantErr: false,
		},
		{
			name: "InvalidSnapshotIdFormat",
			input: &ec2.DescribeSnapshotsInput{
				SnapshotIds: []*string{aws.String("invalid-id")},
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidSnapshotIDMalformed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDescribeSnapshotsInput(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateDeleteSnapshotInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.DeleteSnapshotInput
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
			input:   &ec2.DeleteSnapshotInput{},
			wantErr: true,
			errMsg:  awserrors.ErrorMissingParameter,
		},
		{
			name: "InvalidSnapshotIdFormat",
			input: &ec2.DeleteSnapshotInput{
				SnapshotId: aws.String("invalid-id"),
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidSnapshotIDMalformed,
		},
		{
			name: "ValidInput",
			input: &ec2.DeleteSnapshotInput{
				SnapshotId: aws.String("snap-1234567890abcdef0"),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDeleteSnapshotInput(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCopySnapshotInput(t *testing.T) {
	tests := []struct {
		name    string
		input   *ec2.CopySnapshotInput
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
			input:   &ec2.CopySnapshotInput{},
			wantErr: true,
			errMsg:  awserrors.ErrorMissingParameter,
		},
		{
			name: "MissingSourceRegion",
			input: &ec2.CopySnapshotInput{
				SourceSnapshotId: aws.String("snap-1234567890abcdef0"),
			},
			wantErr: true,
			errMsg:  awserrors.ErrorMissingParameter,
		},
		{
			name: "InvalidSnapshotIdFormat",
			input: &ec2.CopySnapshotInput{
				SourceSnapshotId: aws.String("invalid-id"),
				SourceRegion:     aws.String("ap-southeast-2"),
			},
			wantErr: true,
			errMsg:  awserrors.ErrorInvalidSnapshotIDMalformed,
		},
		{
			name: "ValidInput",
			input: &ec2.CopySnapshotInput{
				SourceSnapshotId: aws.String("snap-1234567890abcdef0"),
				SourceRegion:     aws.String("ap-southeast-2"),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCopySnapshotInput(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
