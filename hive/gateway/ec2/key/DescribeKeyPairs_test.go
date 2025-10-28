package gateway_ec2_key

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestValidateDescribeKeyPairsInput(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.DescribeKeyPairsInput
		want  error
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  nil,
		},
		{
			name:  "EmptyInput",
			input: &ec2.DescribeKeyPairsInput{},
			want:  nil,
		},
		{
			name: "ValidInputWithKeyNames",
			input: &ec2.DescribeKeyPairsInput{
				KeyNames: []*string{aws.String("test-key")},
			},
			want: nil,
		},
		{
			name: "ValidInputWithKeyPairIds",
			input: &ec2.DescribeKeyPairsInput{
				KeyPairIds: []*string{aws.String("key-0123456789abcdef0")},
			},
			want: nil,
		},
		{
			name: "ValidInputWithMultipleKeys",
			input: &ec2.DescribeKeyPairsInput{
				KeyNames: []*string{
					aws.String("test-key-1"),
					aws.String("test-key-2"),
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test only the validation function (DescribeKeyPairs requires NATS infrastructure)
			err := ValidateDescribeKeyPairsInput(tt.input)
			assert.Equal(t, tt.want, err)
		})
	}
}
