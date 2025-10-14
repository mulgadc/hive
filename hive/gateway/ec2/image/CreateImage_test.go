package gateway_ec2_image

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/stretchr/testify/assert"
)

func TestValidateCreateImageInput(t *testing.T) {
	tests := []struct {
		name  string
		input *ec2.CreateImageInput
		want  error
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  errors.New("MissingParameter"),
		},
		{
			name: "MissingInstanceId",
			input: &ec2.CreateImageInput{
				Name: aws.String("test-image"),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "MissingName",
			input: &ec2.CreateImageInput{
				InstanceId: aws.String("i-0123456789abcdef0"),
			},
			want: errors.New("MissingParameter"),
		},
		{
			name: "InvalidInstanceId",
			input: &ec2.CreateImageInput{
				InstanceId: aws.String("invalid-id"),
				Name:       aws.String("test-image"),
			},
			want: errors.New("InvalidInstanceID.Malformed"),
		},
		{
			name: "ValidInput",
			input: &ec2.CreateImageInput{
				InstanceId: aws.String("i-0123456789abcdef0"),
				Name:       aws.String("test-image"),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CreateImage(tt.input)
			assert.Equal(t, tt.want, err)
			if err != nil {
				assert.Empty(t, result.ImageId)
			} else {
				assert.NotEmpty(t, result.ImageId)
				assert.True(t, strings.HasPrefix(*result.ImageId, "ami-"))
			}
		})
	}
}

func TestEC2ProcessCreateImage(t *testing.T) {
	tests := []struct {
		name              string
		payload           interface{}
		rawJSON           []byte
		wantValidationErr bool
		wantErrCode       string
		assertFn          func(t *testing.T, output ec2.CreateImageOutput)
	}{
		{
			name:              "InvalidPayload",
			payload:           &ec2.DescribeImagesInput{},
			wantValidationErr: true,
			wantErrCode:       "ValidationError",
		},
		{
			name: "MissingRequiredFields",
			payload: &ec2.CreateImageInput{
				Name: aws.String("test-image"),
			},
			wantValidationErr: true,
			wantErrCode:       "ValidationError",
		},
		{
			name: "ValidCreateImage",
			payload: &ec2.CreateImageInput{
				InstanceId: aws.String("i-0123456789abcdef0"),
				Name:       aws.String("test-image"),
			},
			assertFn: func(t *testing.T, output ec2.CreateImageOutput) {
				assert.NotNil(t, output.ImageId)
				assert.True(t, strings.HasPrefix(*output.ImageId, "ami-"))
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

			jsonResp := EC2_Process_CreateImage(jsonData)
			responseError, err := utils.ValidateErrorPayload(jsonResp)

			if tt.wantValidationErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrCode, aws.StringValue(responseError.Code))
				return
			}

			assert.NoError(t, err)

			var output ec2.CreateImageOutput
			assert.NoError(t, json.Unmarshal(jsonResp, &output))

			if tt.assertFn != nil {
				tt.assertFn(t, output)
			}
		})
	}
}
