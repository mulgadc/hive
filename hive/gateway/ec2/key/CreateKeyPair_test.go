package gateway_ec2_key

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

func TestValidateCreateKeyPairInput(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.CreateKeyPairInput
		want  error
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  errors.New("MissingParameter"),
		},
		{
			name: "MissingKeyName",
			input: &ec2.CreateKeyPairInput{
				KeyName: aws.String(""),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "ValidInput",
			input: &ec2.CreateKeyPairInput{
				KeyName: aws.String("test-key"),
			},
			want: nil,
		},
		{
			name: "ValidInputWithKeyType",
			input: &ec2.CreateKeyPairInput{
				KeyName: aws.String("test-key"),
				KeyType: aws.String("rsa"),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip this test as CreateKeyPair now requires NATS connection
			// This is covered by integration tests
			t.Skip("Skipping - CreateKeyPair now requires NATS connection. See TestEC2ProcessCreateKeyPair for handler tests.")
		})
	}
}
