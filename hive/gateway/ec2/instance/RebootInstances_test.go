package gateway_ec2_instance

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/types"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRebootInstances_Success(t *testing.T) {
	_, nc := startTestNATSServer(t)

	instanceID := "i-0123456789abcdef0"

	nc.Subscribe("ec2.cmd."+instanceID, func(msg *nats.Msg) {
		var cmd types.EC2InstanceCommand
		err := json.Unmarshal(msg.Data, &cmd)
		require.NoError(t, err)

		assert.Equal(t, instanceID, cmd.ID)
		assert.True(t, cmd.Attributes.RebootInstance)

		msg.Respond([]byte(`{}`))
	})

	input := &ec2.RebootInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	}

	output, err := RebootInstances(input, nc, "123456789012")

	require.NoError(t, err)
	require.NotNil(t, output)
}

func TestRebootInstances_MultipleInstances(t *testing.T) {
	_, nc := startTestNATSServer(t)

	ids := []string{"i-001", "i-002"}

	for _, id := range ids {
		nc.Subscribe("ec2.cmd."+id, func(msg *nats.Msg) {
			msg.Respond([]byte(`{}`))
		})
	}

	input := &ec2.RebootInstancesInput{
		InstanceIds: []*string{aws.String(ids[0]), aws.String(ids[1])},
	}

	output, err := RebootInstances(input, nc, "123456789012")

	require.NoError(t, err)
	require.NotNil(t, output)
}

func TestRebootInstances_EmptyInstanceIds(t *testing.T) {
	_, nc := startTestNATSServer(t)

	input := &ec2.RebootInstancesInput{
		InstanceIds: []*string{},
	}

	_, err := RebootInstances(input, nc, "123456789012")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no instance IDs provided")
}

func TestRebootInstances_NATSTimeout(t *testing.T) {
	_, nc := startTestNATSServer(t)

	instanceID := "i-nosubscriber"

	input := &ec2.RebootInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	}

	_, err := RebootInstances(input, nc, "123456789012")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidInstanceIDNotFound, err.Error())
}

func TestRebootInstances_DaemonError(t *testing.T) {
	_, nc := startTestNATSServer(t)

	instanceID := "i-error"

	nc.Subscribe("ec2.cmd."+instanceID, func(msg *nats.Msg) {
		msg.Respond(utils.GenerateErrorPayload(awserrors.ErrorIncorrectInstanceState))
	})

	input := &ec2.RebootInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	}

	_, err := RebootInstances(input, nc, "123456789012")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorIncorrectInstanceState, err.Error())
}
