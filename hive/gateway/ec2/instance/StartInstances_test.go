package gateway_ec2_instance

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartInstances_Success(t *testing.T) {
	_, nc := startTestNATSServer(t)

	instanceID := "i-0123456789abcdef0"

	// Mock subscriber for the instance command topic
	nc.Subscribe("ec2.cmd."+instanceID, func(msg *nats.Msg) {
		var cmd qmp.Command
		err := json.Unmarshal(msg.Data, &cmd)
		require.NoError(t, err)

		assert.Equal(t, instanceID, cmd.ID)
		assert.Equal(t, "cont", cmd.QMPCommand.Execute)
		assert.True(t, cmd.Attributes.StartInstance)
		assert.False(t, cmd.Attributes.StopInstance)
		assert.False(t, cmd.Attributes.TerminateInstance)

		msg.Respond([]byte(`{"return":{}}`))
	})

	input := &ec2.StartInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	}

	output, err := StartInstances(input, nc)

	require.NoError(t, err)
	require.NotNil(t, output)
	require.Len(t, output.StartingInstances, 1)

	sc := output.StartingInstances[0]
	assert.Equal(t, instanceID, *sc.InstanceId)
	assert.Equal(t, int64(0), *sc.CurrentState.Code)
	assert.Equal(t, "pending", *sc.CurrentState.Name)
	assert.Equal(t, int64(80), *sc.PreviousState.Code)
	assert.Equal(t, "stopped", *sc.PreviousState.Name)
}

func TestStartInstances_MultipleInstances(t *testing.T) {
	_, nc := startTestNATSServer(t)

	ids := []string{"i-001", "i-002", "i-003"}

	for _, id := range ids {
		id := id
		nc.Subscribe("ec2.cmd."+id, func(msg *nats.Msg) {
			msg.Respond([]byte(`{"return":{}}`))
		})
	}

	input := &ec2.StartInstancesInput{
		InstanceIds: []*string{aws.String(ids[0]), aws.String(ids[1]), aws.String(ids[2])},
	}

	output, err := StartInstances(input, nc)

	require.NoError(t, err)
	require.NotNil(t, output)
	assert.Len(t, output.StartingInstances, 3)

	for i, sc := range output.StartingInstances {
		assert.Equal(t, ids[i], *sc.InstanceId)
		assert.Equal(t, "pending", *sc.CurrentState.Name)
	}
}

func TestStartInstances_EmptyInstanceIds(t *testing.T) {
	_, nc := startTestNATSServer(t)

	input := &ec2.StartInstancesInput{
		InstanceIds: []*string{},
	}

	_, err := StartInstances(input, nc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no instance IDs provided")
}

func TestStartInstances_NilInstanceIdSkipped(t *testing.T) {
	_, nc := startTestNATSServer(t)

	instanceID := "i-valid"
	nc.Subscribe("ec2.cmd."+instanceID, func(msg *nats.Msg) {
		msg.Respond([]byte(`{"return":{}}`))
	})

	input := &ec2.StartInstancesInput{
		InstanceIds: []*string{nil, aws.String(instanceID), nil},
	}

	output, err := StartInstances(input, nc)

	require.NoError(t, err)
	require.NotNil(t, output)
	// Only the valid ID should produce a state change
	assert.Len(t, output.StartingInstances, 1)
	assert.Equal(t, instanceID, *output.StartingInstances[0].InstanceId)
}

func TestStartInstances_NATSRequestFails(t *testing.T) {
	_, nc := startTestNATSServer(t)

	// No subscriber for this instance topic, so NATS request will fail
	instanceID := "i-nosubscriber"

	input := &ec2.StartInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	}

	output, err := StartInstances(input, nc)

	require.NoError(t, err) // Function itself doesn't error, it records state change
	require.NotNil(t, output)
	require.Len(t, output.StartingInstances, 1)

	// On NATS failure, state should reflect "still stopped"
	sc := output.StartingInstances[0]
	assert.Equal(t, instanceID, *sc.InstanceId)
	assert.Equal(t, int64(80), *sc.CurrentState.Code)
	assert.Equal(t, "stopped", *sc.CurrentState.Name)
	assert.Equal(t, int64(80), *sc.PreviousState.Code)
	assert.Equal(t, "stopped", *sc.PreviousState.Name)
}

func TestStartInstances_MixedSuccessAndFailure(t *testing.T) {
	_, nc := startTestNATSServer(t)

	goodID := "i-good"
	badID := "i-bad" // no subscriber

	nc.Subscribe("ec2.cmd."+goodID, func(msg *nats.Msg) {
		msg.Respond([]byte(`{"return":{}}`))
	})

	input := &ec2.StartInstancesInput{
		InstanceIds: []*string{aws.String(goodID), aws.String(badID)},
	}

	output, err := StartInstances(input, nc)

	require.NoError(t, err)
	require.Len(t, output.StartingInstances, 2)

	// First instance succeeded: state → pending
	assert.Equal(t, goodID, *output.StartingInstances[0].InstanceId)
	assert.Equal(t, "pending", *output.StartingInstances[0].CurrentState.Name)

	// Second instance failed: state → stopped (unchanged)
	assert.Equal(t, badID, *output.StartingInstances[1].InstanceId)
	assert.Equal(t, "stopped", *output.StartingInstances[1].CurrentState.Name)
}
