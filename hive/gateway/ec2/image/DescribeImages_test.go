package gateway_ec2_image

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestValidateDescribeImagesInput(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.DescribeImagesInput
		want  error
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  nil,
		},
		{
			name:  "EmptyInput",
			input: &ec2.DescribeImagesInput{},
			want:  nil,
		},
		{
			name: "InvalidImageId",
			input: &ec2.DescribeImagesInput{
				ImageIds: []*string{aws.String("invalid-id")},
			},
			want: errors.New("InvalidAMIID.Malformed"),
		},
		{
			name: "ValidInput",
			input: &ec2.DescribeImagesInput{
				ImageIds: []*string{aws.String("ami-0123456789abcdef0")},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := DescribeImages(tt.input)
			assert.Equal(t, tt.want, err)
			if err == nil {
				assert.NotNil(t, result.Images)
			}
		})
	}
}
