package gateway_ec2_image

import (
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestValidateRegisterImageInput(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.RegisterImageInput
		want  error
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  errors.New("MissingParameter"),
		},
		{
			name: "MissingName",
			input: &ec2.RegisterImageInput{
				ImageLocation: aws.String("s3://bucket/image"),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "ValidInput",
			input: &ec2.RegisterImageInput{
				Name:          aws.String("test-image"),
				ImageLocation: aws.String("s3://bucket/image"),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RegisterImage(tt.input)
			assert.Equal(t, tt.want, err)
			if err == nil {
				assert.NotEmpty(t, result.ImageId)
				assert.True(t, strings.HasPrefix(*result.ImageId, "ami-"))
			}
		})
	}
}
