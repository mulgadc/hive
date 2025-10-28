package gateway_ec2_key

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/handlers/ec2/key"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/stretchr/testify/assert"
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

func TestEC2ProcessCreateKeyPair(t *testing.T) {
	tests := []struct {
		name              string
		payload           interface{}
		rawJSON           []byte
		wantValidationErr bool
		wantErrCode       string
		assertFn          func(t *testing.T, output ec2.CreateKeyPairOutput)
	}{
		{
			name:              "InvalidPayload",
			payload:           &ec2.DeleteKeyPairInput{},
			wantValidationErr: true,
			wantErrCode:       "ValidationError",
		},
		{
			name: "MissingRequiredFields",
			payload: &ec2.CreateKeyPairInput{
				KeyName: aws.String(""),
			},
			wantValidationErr: true,
			wantErrCode:       "MissingParameter",
		},
		{
			name: "ValidCreateKeyPair",
			payload: &ec2.CreateKeyPairInput{
				KeyName: aws.String("test-key"),
			},
			assertFn: func(t *testing.T, output ec2.CreateKeyPairOutput) {
				assert.NotNil(t, output.KeyName)
				assert.Equal(t, "test-key", *output.KeyName)
				assert.NotEmpty(t, output.KeyFingerprint)
				assert.NotEmpty(t, output.KeyMaterial)
				assert.NotEmpty(t, output.KeyPairId)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var jsonData []byte
			var err error

			if tt.rawJSON != nil {
				jsonData = tt.rawJSON
			} else {
				jsonData, err = json.Marshal(tt.payload)
				assert.NoError(t, err)
			}

			handler := handlers_ec2_key.NewCreateKeyPairHandler(handlers_ec2_key.NewMockKeyService())
			jsonResp := handler.Process(jsonData)
			responseError, err := utils.ValidateErrorPayload(jsonResp)

			if tt.wantValidationErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrCode, aws.StringValue(responseError.Code))
				return
			}

			assert.NoError(t, err)

			var output ec2.CreateKeyPairOutput
			assert.NoError(t, json.Unmarshal(jsonResp, &output))

			if tt.assertFn != nil {
				tt.assertFn(t, output)
			}
		})
	}
}
