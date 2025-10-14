package gateway_ec2_image

import (
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestValidateCopyImageInput(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.CopyImageInput
		want  error
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  errors.New("MissingParameter"),
		},
		{
			name: "MissingName",
			input: &ec2.CopyImageInput{
				SourceImageId: aws.String("ami-0123456789abcdef0"),
				SourceRegion:  aws.String("us-east-1"),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "MissingSourceImageId",
			input: &ec2.CopyImageInput{
				Name:         aws.String("test-copy"),
				SourceRegion: aws.String("us-east-1"),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "MissingSourceRegion",
			input: &ec2.CopyImageInput{
				Name:          aws.String("test-copy"),
				SourceImageId: aws.String("ami-0123456789abcdef0"),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "InvalidSourceImageId",
			input: &ec2.CopyImageInput{
				Name:          aws.String("test-copy"),
				SourceImageId: aws.String("invalid-id"),
				SourceRegion:  aws.String("us-east-1"),
			},
			want: errors.New("InvalidAMIID.Malformed"),
		},
		{
			name: "ValidInput",
			input: &ec2.CopyImageInput{
				Name:          aws.String("test-copy"),
				SourceImageId: aws.String("ami-0123456789abcdef0"),
				SourceRegion:  aws.String("us-east-1"),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CopyImage(tt.input)
			assert.Equal(t, tt.want, err)
			if err == nil {
				assert.NotEmpty(t, result.ImageId)
				assert.True(t, strings.HasPrefix(*result.ImageId, "ami-"))
			}
		})
	}
}
