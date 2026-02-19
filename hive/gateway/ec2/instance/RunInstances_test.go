package gateway_ec2_instance

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		name     string
		input    *ec2.RunInstancesInput
		wantCode string
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
			wantCode: awserrors.ErrorInvalidParameterValue,
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
			wantCode: awserrors.ErrorInvalidParameterValue,
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
			wantCode: awserrors.ErrorMissingParameter,
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
			wantCode: awserrors.ErrorMissingParameter,
		},

		{
			name: "InvalidAMIIDMalformed",
			input: &ec2.RunInstancesInput{
				ImageId:          aws.String("invalid-name-here"),
				InstanceType:     defaults.InstanceType,
				MinCount:         aws.Int64(1),
				MaxCount:         aws.Int64(1),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			wantCode: awserrors.ErrorInvalidAMIIDMalformed,
		},

		{
			name:     "NilInput",
			input:    nil,
			wantCode: awserrors.ErrorMissingParameter,
		},

		{
			name: "NilMinCount",
			input: &ec2.RunInstancesInput{
				ImageId:          defaults.ImageId,
				InstanceType:     defaults.InstanceType,
				MinCount:         nil,
				MaxCount:         aws.Int64(1),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			wantCode: awserrors.ErrorMissingParameter,
		},

		{
			name: "MinCountGreaterThanMaxCount",
			input: &ec2.RunInstancesInput{
				ImageId:          defaults.ImageId,
				InstanceType:     defaults.InstanceType,
				MinCount:         aws.Int64(5),
				MaxCount:         aws.Int64(2),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			wantCode: awserrors.ErrorInvalidParameterValue,
		},

		{
			name: "NilImageId",
			input: &ec2.RunInstancesInput{
				ImageId:          nil,
				InstanceType:     defaults.InstanceType,
				MinCount:         aws.Int64(1),
				MaxCount:         aws.Int64(1),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			wantCode: awserrors.ErrorMissingParameter,
		},

		{
			name: "NilInstanceType",
			input: &ec2.RunInstancesInput{
				ImageId:          defaults.ImageId,
				InstanceType:     nil,
				MinCount:         aws.Int64(1),
				MaxCount:         aws.Int64(1),
				KeyName:          defaults.KeyName,
				SecurityGroupIds: defaults.SecurityGroupIds,
				SubnetId:         defaults.SubnetId,
			},
			wantCode: awserrors.ErrorMissingParameter,
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
			wantCode: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Skip the valid test as it requires full daemon infrastructure
			if test.wantCode == "" {
				t.Skip("Skipping valid test - requires full daemon infrastructure")
			}

			// For validation tests, we can pass nil conn since validation happens before NATS call
			response, err := RunInstances(test.input, nil)

			require.Error(t, err)
			assert.Equal(t, test.wantCode, err.Error())
			assert.Len(t, response.Instances, 0)
		})
	}
}

func TestRunInstances_FieldSpecificMessages(t *testing.T) {
	tests := []struct {
		name        string
		input       *ec2.RunInstancesInput
		wantCode    string
		wantContain string
	}{
		{
			name: "MinCountZero_SpecificMessage",
			input: &ec2.RunInstancesInput{
				ImageId:      defaults.ImageId,
				InstanceType: defaults.InstanceType,
				MinCount:     aws.Int64(0),
				MaxCount:     aws.Int64(1),
			},
			wantCode:    awserrors.ErrorInvalidParameterValue,
			wantContain: "minCount",
		},
		{
			name: "MaxCountZero_SpecificMessage",
			input: &ec2.RunInstancesInput{
				ImageId:      defaults.ImageId,
				InstanceType: defaults.InstanceType,
				MinCount:     aws.Int64(1),
				MaxCount:     aws.Int64(0),
			},
			wantCode:    awserrors.ErrorInvalidParameterValue,
			wantContain: "maxCount",
		},
		{
			name: "MinExceedsMax_SpecificMessage",
			input: &ec2.RunInstancesInput{
				ImageId:      defaults.ImageId,
				InstanceType: defaults.InstanceType,
				MinCount:     aws.Int64(5),
				MaxCount:     aws.Int64(2),
			},
			wantCode:    awserrors.ErrorInvalidParameterValue,
			wantContain: "minCount may not exceed maxCount",
		},
		{
			name: "InvalidAMIID_SpecificMessage",
			input: &ec2.RunInstancesInput{
				ImageId:      aws.String("bad-id"),
				InstanceType: defaults.InstanceType,
				MinCount:     aws.Int64(1),
				MaxCount:     aws.Int64(1),
			},
			wantCode:    awserrors.ErrorInvalidAMIIDMalformed,
			wantContain: "bad-id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := RunInstances(test.input, nil)

			require.Error(t, err)
			assert.Equal(t, test.wantCode, err.Error())

			var awsErr *awserrors.AWSError
			require.True(t, errors.As(err, &awsErr), "expected AWSError type")
			assert.Contains(t, awsErr.Detail, test.wantContain)
		})
	}
}
