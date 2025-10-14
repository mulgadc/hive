package gateway_ec2_key

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
)

func TestValidateImportKeyPairInput(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.ImportKeyPairInput
		want  error
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  errors.New("MissingParameter"),
		},
		{
			name: "MissingKeyName",
			input: &ec2.ImportKeyPairInput{
				PublicKeyMaterial: []byte("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ..."),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "MissingPublicKeyMaterial",
			input: &ec2.ImportKeyPairInput{
				KeyName: aws.String("test-key"),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "EmptyPublicKeyMaterial",
			input: &ec2.ImportKeyPairInput{
				KeyName:           aws.String("test-key"),
				PublicKeyMaterial: []byte{},
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "ValidInput",
			input: &ec2.ImportKeyPairInput{
				KeyName:           aws.String("test-key"),
				PublicKeyMaterial: []byte("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ..."),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ImportKeyPair(tt.input)
			assert.Equal(t, tt.want, err)
			if err != nil {
				assert.Empty(t, result.KeyName)
			} else {
				assert.NotEmpty(t, result.KeyName)
				assert.NotEmpty(t, result.KeyFingerprint)
				assert.NotEmpty(t, result.KeyPairId)
			}
		})
	}
}
