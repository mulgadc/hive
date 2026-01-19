package gateway_ec2_instance

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/nats-io/nats.go"
)

// TerminateInstances sends terminate commands to specified instances via NATS
// Uses system_powerdown with stop_instance attribute to prevent restart
func TerminateInstances(input *ec2.TerminateInstancesInput, natsConn *nats.Conn) (*ec2.TerminateInstancesOutput, error) {
	if len(input.InstanceIds) == 0 {
		return nil, fmt.Errorf("no instance IDs provided")
	}

	slog.Info("TerminateInstances: Processing request", "instance_count", len(input.InstanceIds))

	var stateChanges []*ec2.InstanceStateChange

	// Process each instance
	for _, instanceIDPtr := range input.InstanceIds {
		if instanceIDPtr == nil {
			continue
		}
		instanceID := *instanceIDPtr

		// Build the QMP command to terminate the instance
		// Note: stop_instance=true prevents restart on daemon/node restart
		command := qmp.Command{
			ID: instanceID,
			QMPCommand: qmp.QMPCommand{
				Execute:   "system_powerdown",
				Arguments: map[string]any{},
			},
			Attributes: qmp.Attributes{
				StopInstance:      true, // Prevent restart on daemon/node restart
				TerminateInstance: true,
			},
		}

		// Marshal the command
		jsonData, err := json.Marshal(command)
		if err != nil {
			slog.Error("TerminateInstances: Failed to marshal command", "instance_id", instanceID, "err", err)
			continue
		}

		// Send NATS request to the specific instance topic
		subject := fmt.Sprintf("ec2.cmd.%s", instanceID)
		msg, err := natsConn.Request(subject, jsonData, 5*time.Second)
		if err != nil {
			slog.Error("TerminateInstances: Failed to send command", "instance_id", instanceID, "err", err)
			// Add failed state change
			stateChange := &ec2.InstanceStateChange{
				InstanceId: &instanceID,
				CurrentState: &ec2.InstanceState{
					Code: new(int64),
					Name: new(string),
				},
				PreviousState: &ec2.InstanceState{
					Code: new(int64),
					Name: new(string),
				},
			}
			*stateChange.CurrentState.Code = 16 // running
			*stateChange.CurrentState.Name = "running"
			*stateChange.PreviousState.Code = 16
			*stateChange.PreviousState.Name = "running"
			stateChanges = append(stateChanges, stateChange)
			continue
		}

		slog.Info("TerminateInstances: Command sent successfully", "instance_id", instanceID, "response", string(msg.Data))

		// Build state change response (running -> shutting-down)
		stateChange := &ec2.InstanceStateChange{
			InstanceId: &instanceID,
			CurrentState: &ec2.InstanceState{
				Code: new(int64),
				Name: new(string),
			},
			PreviousState: &ec2.InstanceState{
				Code: new(int64),
				Name: new(string),
			},
		}
		*stateChange.CurrentState.Code = 32 // shutting-down
		*stateChange.CurrentState.Name = "shutting-down"
		*stateChange.PreviousState.Code = 16 // running
		*stateChange.PreviousState.Name = "running"
		stateChanges = append(stateChanges, stateChange)
	}

	output := &ec2.TerminateInstancesOutput{
		TerminatingInstances: stateChanges,
	}

	slog.Info("TerminateInstances: Completed", "total_instances", len(stateChanges))
	return output, nil
}
