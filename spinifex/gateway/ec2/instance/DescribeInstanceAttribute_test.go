package gateway_ec2_instance

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Validation tests ---

func TestValidateDescribeInstanceAttributeInput_NilInput(t *testing.T) {
	err := ValidateDescribeInstanceAttributeInput(nil)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestValidateDescribeInstanceAttributeInput_MissingInstanceId(t *testing.T) {
	err := ValidateDescribeInstanceAttributeInput(&ec2.DescribeInstanceAttributeInput{
		Attribute: aws.String("instanceType"),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidInstanceIDMalformed, err.Error())
}

func TestValidateDescribeInstanceAttributeInput_EmptyInstanceId(t *testing.T) {
	err := ValidateDescribeInstanceAttributeInput(&ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(""),
		Attribute:  aws.String("instanceType"),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidInstanceIDMalformed, err.Error())
}

func TestValidateDescribeInstanceAttributeInput_BadPrefix(t *testing.T) {
	err := ValidateDescribeInstanceAttributeInput(&ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String("x-12345"),
		Attribute:  aws.String("instanceType"),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidInstanceIDMalformed, err.Error())
}

func TestValidateDescribeInstanceAttributeInput_MissingAttribute(t *testing.T) {
	err := ValidateDescribeInstanceAttributeInput(&ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String("i-abc123"),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorMissingParameter, err.Error())
}

func TestValidateDescribeInstanceAttributeInput_EmptyAttribute(t *testing.T) {
	err := ValidateDescribeInstanceAttributeInput(&ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String("i-abc123"),
		Attribute:  aws.String(""),
	})
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorMissingParameter, err.Error())
}

func TestValidateDescribeInstanceAttributeInput_Valid(t *testing.T) {
	err := ValidateDescribeInstanceAttributeInput(&ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String("i-abc123"),
		Attribute:  aws.String("instanceType"),
	})
	assert.NoError(t, err)
}

// --- Gateway function tests ---

func TestDescribeInstanceAttribute_Success(t *testing.T) {
	_, nc := startTestNATSServer(t)

	nc.QueueSubscribe("ec2.DescribeInstanceAttribute", "spinifex-workers", func(msg *nats.Msg) {
		var input ec2.DescribeInstanceAttributeInput
		err := json.Unmarshal(msg.Data, &input)
		require.NoError(t, err)
		assert.Equal(t, "i-test123", *input.InstanceId)
		assert.Equal(t, "instanceType", *input.Attribute)

		output := &ec2.DescribeInstanceAttributeOutput{
			InstanceId:   aws.String("i-test123"),
			InstanceType: &ec2.AttributeValue{Value: aws.String("t3.micro")},
		}
		resp, _ := json.Marshal(output)
		msg.Respond(resp)
	})

	input := &ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String("i-test123"),
		Attribute:  aws.String("instanceType"),
	}

	output, err := DescribeInstanceAttribute(input, nc, "123456789012")
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.Equal(t, "i-test123", *output.InstanceId)
	assert.Equal(t, "t3.micro", *output.InstanceType.Value)
}

func TestDescribeInstanceAttribute_DaemonError(t *testing.T) {
	_, nc := startTestNATSServer(t)

	nc.QueueSubscribe("ec2.DescribeInstanceAttribute", "spinifex-workers", func(msg *nats.Msg) {
		msg.Respond([]byte(`{"Code":"InvalidInstanceID.NotFound"}`))
	})

	input := &ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String("i-notfound"),
		Attribute:  aws.String("instanceType"),
	}

	_, err := DescribeInstanceAttribute(input, nc, "123456789012")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidInstanceIDNotFound, err.Error())
}

func TestDescribeInstanceAttribute_ValidationFailure(t *testing.T) {
	_, nc := startTestNATSServer(t)

	_, err := DescribeInstanceAttribute(nil, nc, "123456789012")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}
