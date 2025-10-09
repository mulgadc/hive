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

	var input ec2.DescribeInstancesInput

	input.DryRun = aws.Bool(true)

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)

	// Expect no marshal error, even for invalid payload
	assert.NoError(t, err)

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_RunInstances(jsonData)

	responseError, err := utils.ValidateErrorPayload(jsonResp)

	// Should successfully parse the generated error payload
	assert.Error(t, err)

	// Expect correct error code
	assert.Equal(t, "ValidationError", aws.StringValue(responseError.Code))

	// Confirm the correct input struct, but default values incorrect.
	emptyRunInstance := ec2.RunInstancesInput{ImageId: aws.String("ami-0abcdef1234567890")}

	jsonData, err = json.Marshal(emptyRunInstance)

	// Expect no marshal error
	assert.NoError(t, err)

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp = EC2_Process_RunInstances(jsonData)

	_, err = utils.ValidateErrorPayload(jsonResp)

	// Expect correct error code
	assert.Equal(t, "ValidationError", aws.StringValue(responseError.Code))

	// Should successfully parse the generated error payload
	assert.Error(t, err)

	// Run expected "good" input
	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err = json.Marshal(defaults)

	// Expect no marshal error
	assert.NoError(t, err)

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp = EC2_Process_RunInstances(jsonData)

	var reservation ec2.Reservation

	_, err = utils.ValidateErrorPayload(jsonResp)

	// Should successfully parse the generated error payload
	assert.NoError(t, err)

	// Unmarshal
	err = json.Unmarshal(jsonResp, &reservation)

	assert.NoError(t, err)

	// Sanity check expected output matches
	assert.Len(t, reservation.Instances, 1)

	if len(reservation.Instances) > 0 {
		assert.Equal(t, defaults.InstanceType, reservation.Instances[0].InstanceType)
	}

}
