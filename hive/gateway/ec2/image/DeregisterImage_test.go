package gateway_ec2_image

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestValidateDeregisterImageInput(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.DeregisterImageInput
		want  error
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  errors.New("MissingParameter"),
		},
		{
			name: "MissingImageId",
			input: &ec2.DeregisterImageInput{
				ImageId: aws.String(""),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "InvalidImageId",
			input: &ec2.DeregisterImageInput{
				ImageId: aws.String("invalid-id"),
			},
			want: errors.New("InvalidAMIID.Malformed"),
		},
		{
			name: "ValidInput",
			input: &ec2.DeregisterImageInput{
				ImageId: aws.String("ami-0123456789abcdef0"),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DeregisterImage(tt.input)
			assert.Equal(t, tt.want, err)
		})
	}
}
