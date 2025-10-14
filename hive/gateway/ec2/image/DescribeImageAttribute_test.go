package gateway_ec2_image

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestValidateDescribeImageAttributeInput(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.DescribeImageAttributeInput
		want  error
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  errors.New("MissingParameter"),
		},
		{
			name: "MissingAttribute",
			input: &ec2.DescribeImageAttributeInput{
				ImageId: aws.String("ami-0123456789abcdef0"),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "MissingImageId",
			input: &ec2.DescribeImageAttributeInput{
				Attribute: aws.String("description"),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "InvalidImageId",
			input: &ec2.DescribeImageAttributeInput{
				ImageId:   aws.String("invalid-id"),
				Attribute: aws.String("description"),
			},
			want: errors.New("InvalidAMIID.Malformed"),
		},
		{
			name: "ValidInput",
			input: &ec2.DescribeImageAttributeInput{
				ImageId:   aws.String("ami-0123456789abcdef0"),
				Attribute: aws.String("description"),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := DescribeImageAttribute(tt.input)
			assert.Equal(t, tt.want, err)
			if err == nil {
				assert.NotEmpty(t, result.ImageId)
			}
		})
	}
}
