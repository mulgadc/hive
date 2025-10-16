package gateway_ec2_instance

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/handlers/ec2/instance"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTestNATSServer starts an embedded NATS server for testing
func startTestNATSServer(t *testing.T) (*server.Server, string) {
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1, // Auto-allocate available port
		JetStream: false,
		NoLog:     true,
		NoSigs:    true,
	}

	ns, err := server.NewServer(opts)
	require.NoError(t, err, "Failed to create NATS server")

	// Start server in goroutine
	go ns.Start()

	// Wait for server to be ready
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server failed to start")
	}

	url := ns.ClientURL()
	t.Logf("Test NATS server started at: %s", url)

	return ns, url
}

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
			// Skip the valid test as it requires full daemon infrastructure
			// These tests are covered by the integration tests in service_nats_test.go
			if test.want == nil {
				t.Skip("Skipping valid test - requires full daemon infrastructure")
			}

			// For validation tests, we can pass nil conn since validation happens before NATS call
			response, err := RunInstances(test.input, nil)

			// Use assert to check if the error is as expected
			assert.Equal(t, test.want, err)

			if err != nil {
				assert.Len(t, response.Instances, 0)
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
			wantErrCode:       "MissingParameter",
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

			handler := handlers_ec2_instance.NewRunInstancesHandler(handlers_ec2_instance.NewMockInstanceService())
			jsonResp := handler.Process(jsonData)

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
