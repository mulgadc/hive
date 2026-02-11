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

func TestTerminateInstances_Success(t *testing.T) {
	_, nc := startTestNATSServer(t)

	instanceID := "i-0123456789abcdef0"

	nc.Subscribe("ec2.cmd."+instanceID, func(msg *nats.Msg) {
		var cmd qmp.Command
		err := json.Unmarshal(msg.Data, &cmd)
		require.NoError(t, err)

		assert.Equal(t, instanceID, cmd.ID)
		assert.Equal(t, "system_powerdown", cmd.QMPCommand.Execute)
		assert.True(t, cmd.Attributes.StopInstance)
		assert.True(t, cmd.Attributes.TerminateInstance)

		msg.Respond([]byte(`{"return":{}}`))
	})

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	}

	output, err := TerminateInstances(input, nc)

	require.NoError(t, err)
	require.NotNil(t, output)
	require.Len(t, output.TerminatingInstances, 1)

	sc := output.TerminatingInstances[0]
	assert.Equal(t, instanceID, *sc.InstanceId)
	assert.Equal(t, int64(32), *sc.CurrentState.Code)
	assert.Equal(t, "shutting-down", *sc.CurrentState.Name)
	assert.Equal(t, int64(16), *sc.PreviousState.Code)
	assert.Equal(t, "running", *sc.PreviousState.Name)
}

func TestTerminateInstances_MultipleInstances(t *testing.T) {
	_, nc := startTestNATSServer(t)

	ids := []string{"i-001", "i-002", "i-003"}

	for _, id := range ids {
		id := id
		nc.Subscribe("ec2.cmd."+id, func(msg *nats.Msg) {
			msg.Respond([]byte(`{"return":{}}`))
		})
	}

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(ids[0]), aws.String(ids[1]), aws.String(ids[2])},
	}

	output, err := TerminateInstances(input, nc)

	require.NoError(t, err)
	require.Len(t, output.TerminatingInstances, 3)

	for i, sc := range output.TerminatingInstances {
		assert.Equal(t, ids[i], *sc.InstanceId)
		assert.Equal(t, "shutting-down", *sc.CurrentState.Name)
		assert.Equal(t, "running", *sc.PreviousState.Name)
	}
}

func TestTerminateInstances_EmptyInstanceIds(t *testing.T) {
	_, nc := startTestNATSServer(t)

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{},
	}

	_, err := TerminateInstances(input, nc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no instance IDs provided")
}

func TestTerminateInstances_NilInstanceIdSkipped(t *testing.T) {
	_, nc := startTestNATSServer(t)

	instanceID := "i-valid"
	nc.Subscribe("ec2.cmd."+instanceID, func(msg *nats.Msg) {
		msg.Respond([]byte(`{"return":{}}`))
	})

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{nil, aws.String(instanceID), nil},
	}

	output, err := TerminateInstances(input, nc)

	require.NoError(t, err)
	assert.Len(t, output.TerminatingInstances, 1)
	assert.Equal(t, instanceID, *output.TerminatingInstances[0].InstanceId)
}

func TestTerminateInstances_NATSRequestFails(t *testing.T) {
	_, nc := startTestNATSServer(t)

	instanceID := "i-nosubscriber"

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	}

	output, err := TerminateInstances(input, nc)

	require.NoError(t, err)
	require.Len(t, output.TerminatingInstances, 1)

	// On NATS failure, state should reflect "still running"
	sc := output.TerminatingInstances[0]
	assert.Equal(t, instanceID, *sc.InstanceId)
	assert.Equal(t, int64(16), *sc.CurrentState.Code)
	assert.Equal(t, "running", *sc.CurrentState.Name)
	assert.Equal(t, int64(16), *sc.PreviousState.Code)
	assert.Equal(t, "running", *sc.PreviousState.Name)
}

func TestTerminateInstances_MixedSuccessAndFailure(t *testing.T) {
	_, nc := startTestNATSServer(t)

	goodID := "i-good"
	badID := "i-bad"

	nc.Subscribe("ec2.cmd."+goodID, func(msg *nats.Msg) {
		msg.Respond([]byte(`{"return":{}}`))
	})

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(goodID), aws.String(badID)},
	}

	output, err := TerminateInstances(input, nc)

	require.NoError(t, err)
	require.Len(t, output.TerminatingInstances, 2)

	// First: success → shutting-down
	assert.Equal(t, goodID, *output.TerminatingInstances[0].InstanceId)
	assert.Equal(t, "shutting-down", *output.TerminatingInstances[0].CurrentState.Name)

	// Second: failure → still running
	assert.Equal(t, badID, *output.TerminatingInstances[1].InstanceId)
	assert.Equal(t, "running", *output.TerminatingInstances[1].CurrentState.Name)
}

func TestTerminateInstances_VerifiesQMPAttributes(t *testing.T) {
	_, nc := startTestNATSServer(t)

	instanceID := "i-verify"
	var receivedCmd qmp.Command

	nc.Subscribe("ec2.cmd."+instanceID, func(msg *nats.Msg) {
		json.Unmarshal(msg.Data, &receivedCmd)
		msg.Respond([]byte(`{"return":{}}`))
	})

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	}

	_, err := TerminateInstances(input, nc)
	require.NoError(t, err)

	// Terminate should set both stop and terminate flags
	assert.True(t, receivedCmd.Attributes.StopInstance, "StopInstance should be true for terminate")
	assert.True(t, receivedCmd.Attributes.TerminateInstance, "TerminateInstance should be true")
	assert.False(t, receivedCmd.Attributes.StartInstance, "StartInstance should be false")
}
