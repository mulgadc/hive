package gateway_ec2_instance

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

var defaults = ec2.RunInstancesInput{
	ImageId:      aws.String("ami-0abcdef1234567890"),
	InstanceType: aws.String("t2.micro"),
	MinCount:     aws.Int64(1),
	MaxCount:     aws.Int64(1),
	KeyName:      aws.String("my-key-pair"),
	SecurityGroupIds: []*string{
		aws.String("sg-0123456789abcdef0"),
	},
	SubnetId: aws.String("subnet-6e7f829e"),
}

func TestParseRunInstances(t *testing.T) {

	// Group multiple tests
	tests := []struct {
		name  string
		input *ec2.RunInstancesInput
		want  error
	}{

		{
			name: "InvalidMinCount",
			input: &ec2.RunInstancesInput{
				ImageId:          defaults.ImageId,
				InstanceType:     defaults.InstanceType,
				MinCount:         aws.Int64(0),
				MaxCount:         aws.Int64(1),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			want: errors.New("InvalidParameterValue"),
		},

		{
			name: "InvalidMaxCount",
			input: &ec2.RunInstancesInput{
				ImageId:          defaults.ImageId,
				InstanceType:     defaults.InstanceType,
				MinCount:         aws.Int64(1),
				MaxCount:         aws.Int64(0),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			want: errors.New("InvalidParameterValue"),
		},

		{
			name: "InvalidMinCount",
			input: &ec2.RunInstancesInput{
				ImageId:          defaults.ImageId,
				InstanceType:     defaults.InstanceType,
				MinCount:         aws.Int64(0),
				MaxCount:         aws.Int64(1),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			want: errors.New("InvalidParameterValue"),
		},

		{
			name: "InvalidNoImageId",
			input: &ec2.RunInstancesInput{
				ImageId:          aws.String(""),
				InstanceType:     defaults.InstanceType,
				MinCount:         aws.Int64(1),
				MaxCount:         aws.Int64(1),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			want: errors.New("MissingParameter"),
		},

		{
			name: "InvalidNoInstanceType",
			input: &ec2.RunInstancesInput{
				ImageId:          defaults.ImageId,
				InstanceType:     aws.String(""),
				MinCount:         aws.Int64(1),
				MaxCount:         aws.Int64(1),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			want: errors.New("MissingParameter"),
		},

		{
			name: "InvalidNoInstanceType",
			input: &ec2.RunInstancesInput{
				ImageId:          aws.String("invalid-name-here"),
				InstanceType:     defaults.InstanceType,
				MinCount:         aws.Int64(1),
				MaxCount:         aws.Int64(1),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			want: errors.New("InvalidAMIID.Malformed"),
		},

		// Successful test
		{
			name: "ValidTest",
			input: &ec2.RunInstancesInput{
				ImageId:          defaults.ImageId,
				InstanceType:     defaults.InstanceType,
				MinCount:         aws.Int64(1),
				MaxCount:         aws.Int64(1),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := RunInstances(test.input)

			// Use assert to check if the error is as expected
			assert.Equal(t, test.want, err)

			if err != nil {
				assert.Len(t, response.Instances, 0)
				//assert.Nil(t, response)
			} else {

				// Check expected output
				assert.Len(t, response.Instances, 1)

				// ImageID returned
				if len(response.Instances) > 0 {

					// ImageId matches our AMI
					assert.Equal(t, defaults.ImageId, response.Instances[0].ImageId)

					// InstanceID starts with i-
					assert.True(t, true, strings.HasPrefix(*response.Instances[0].ImageId, "i-"))

					// InstanceType matches
					assert.Equal(t, defaults.InstanceType, response.Instances[0].InstanceType)

					// KeyName matches
					assert.Equal(t, defaults.KeyName, response.Instances[0].KeyName)

					// State should be 16, booting.
					assert.Equal(t, aws.Int64(16), response.Instances[0].State.Code)

					assert.Equal(t, aws.String("running"), response.Instances[0].State.Name)

					// Subnet should match
					assert.Equal(t, defaults.SubnetId, response.Instances[0].SubnetId)

				}

			}

		})
	}

	// Additional test

}

func TestEC2ProcessRunInstances(t *testing.T) {
	makeValidInput := func() *ec2.RunInstancesInput {
		return &ec2.RunInstancesInput{
			ImageId:          aws.String(*defaults.ImageId),
			InstanceType:     aws.String(*defaults.InstanceType),
			MinCount:         aws.Int64(1),
			MaxCount:         aws.Int64(1),
			KeyName:          aws.String(*defaults.KeyName),
			SecurityGroupIds: []*string{aws.String(*defaults.SecurityGroupIds[0])},
			SubnetId:         aws.String(*defaults.SubnetId),
		}
	}

	tests := []struct {
		name              string
		payload           interface{}
		rawJSON           []byte
		wantValidationErr bool
		wantErrCode       string
		assertFn          func(t *testing.T, reservation ec2.Reservation)
	}{
		{
			name:              "MismatchedShape",
			payload:           &ec2.DescribeInstancesInput{DryRun: aws.Bool(true)},
			wantValidationErr: true,
			wantErrCode:       "ValidationError",
		},
		{
			name: "MissingRequiredFields",
			payload: &ec2.RunInstancesInput{
				ImageId:  aws.String("ami-0abcdef1234567890"),
				MaxCount: aws.Int64(1),
			},
			wantValidationErr: true,
			wantErrCode:       "ValidationError",
		},
		{
			name: "UnknownFieldInPayload",
			rawJSON: []byte(`{
				"ImageId":"ami-0abcdef1234567890",
				"InstanceType":"t2.micro",
				"MinCount":1,
				"MaxCount":1,
				"Unexpected":true
			}`),
			wantValidationErr: true,
			wantErrCode:       "ValidationError",
		},
		{
			name:    "ValidRunInstances",
			payload: makeValidInput(),
			assertFn: func(t *testing.T, reservation ec2.Reservation) {
				assert.Len(t, reservation.Instances, 1)
				if len(reservation.Instances) == 0 {
					return
				}
				instance := reservation.Instances[0]
				assert.Equal(t, defaults.ImageId, instance.ImageId)
				assert.Equal(t, defaults.InstanceType, instance.InstanceType)
				assert.Equal(t, defaults.KeyName, instance.KeyName)
				assert.Equal(t, aws.Int64(16), instance.State.Code)
				assert.Equal(t, aws.String("running"), instance.State.Name)
				assert.Equal(t, defaults.SubnetId, instance.SubnetId)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				jsonData []byte
				err      error
			)

			if tt.rawJSON != nil {
				jsonData = tt.rawJSON
			} else {
				jsonData, err = json.Marshal(tt.payload)
				assert.NoError(t, err)
			}

			jsonResp := EC2_Process_RunInstances(jsonData)

			responseError, err := utils.ValidateErrorPayload(jsonResp)

			if tt.wantValidationErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrCode, aws.StringValue(responseError.Code))
				return
			}

			assert.NoError(t, err)

			var reservation ec2.Reservation
			assert.NoError(t, json.Unmarshal(jsonResp, &reservation))

			if tt.assertFn != nil {
				tt.assertFn(t, reservation)
			}
		})
	}
}
