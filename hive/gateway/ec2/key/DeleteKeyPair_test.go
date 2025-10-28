package gateway_ec2_key

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

func TestValidateDeleteKeyPairInput(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.DeleteKeyPairInput
		want  error
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  errors.New("MissingParameter"),
		},
		{
			name: "MissingBothKeyNameAndKeyPairId",
			input: &ec2.DeleteKeyPairInput{
				KeyName:   aws.String(""),
				KeyPairId: aws.String(""),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "ValidInputWithKeyName",
			input: &ec2.DeleteKeyPairInput{
				KeyName: aws.String("test-key"),
			},
			want: nil,
		},
		{
			name: "ValidInputWithKeyPairId",
			input: &ec2.DeleteKeyPairInput{
				KeyPairId: aws.String("key-0123456789abcdef0"),
			},
			want: nil,
		},
		{
			name: "ValidInputWithBoth",
			input: &ec2.DeleteKeyPairInput{
				KeyName:   aws.String("test-key"),
				KeyPairId: aws.String("key-0123456789abcdef0"),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip this test as DeleteKeyPair now requires NATS connection
			// This is covered by integration tests
			t.Skip("Skipping - DeleteKeyPair now requires NATS connection. See handler tests for validation.")
		})
	}
}
