package gateway_ec2_instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// StopInstances sends stop commands to specified instances via NATS
// Uses system_powerdown with stop_instance attribute to prevent auto-restart on daemon boot
func StopInstances(input *ec2.StopInstancesInput, natsConn *nats.Conn, accountID string) (*ec2.StopInstancesOutput, error) {
	if len(input.InstanceIds) == 0 {
		return nil, fmt.Errorf("no instance IDs provided")
	}

	slog.Info("StopInstances: Processing request", "instance_count", len(input.InstanceIds))

	var stateChanges []*ec2.InstanceStateChange

	// Process each instance
	for _, instanceIDPtr := range input.InstanceIds {
		if instanceIDPtr == nil {
			continue
		}
		instanceID := *instanceIDPtr

		// Build the QMP command to stop the instance
		// Note: system_powerdown with stop_instance=true prevents auto-restart on daemon boot
		command := qmp.Command{
			ID: instanceID,
			QMPCommand: qmp.QMPCommand{
				Execute:   "system_powerdown",
				Arguments: map[string]any{},
			},
			Attributes: qmp.Attributes{
				StopInstance:      true, // Don't auto-restart on daemon boot
				TerminateInstance: false,
			},
		}

		// Marshal the command
		jsonData, err := json.Marshal(command)
		if err != nil {
			slog.Error("StopInstances: Failed to marshal command", "instance_id", instanceID, "err", err)
			continue
		}

		// Send NATS request to the specific instance topic with account ID header
		subject := fmt.Sprintf("ec2.cmd.%s", instanceID)
		reqMsg := nats.NewMsg(subject)
		reqMsg.Data = jsonData
		reqMsg.Header.Set(utils.AccountIDHeader, accountID)
		msg, err := natsConn.RequestMsg(reqMsg, 5*time.Second)
		if err != nil {
			slog.Error("StopInstances: Failed to send command", "instance_id", instanceID, "err", err)
			stateChanges = append(stateChanges, newStateChange(instanceID, 16, "running", 16, "running"))
			continue
		}

		// Check if the daemon returned an error response (e.g. ownership check failure)
		if responseError, parseErr := utils.ValidateErrorPayload(msg.Data); parseErr != nil {
			slog.Error("StopInstances: Daemon returned error", "instance_id", instanceID, "code", *responseError.Code)
			return nil, errors.New(*responseError.Code)
		}

		slog.Info("StopInstances: Command sent successfully", "instance_id", instanceID, "response", string(msg.Data))

		stateChanges = append(stateChanges, newStateChange(instanceID, 64, "stopping", 16, "running"))
	}

	output := &ec2.StopInstancesOutput{
		StoppingInstances: stateChanges,
	}

	slog.Info("StopInstances: Completed", "total_instances", len(stateChanges))
	return output, nil
}
