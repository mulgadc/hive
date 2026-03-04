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

// terminateStoppedInstanceRequest is the payload sent to the ec2.terminate topic
type terminateStoppedInstanceRequest struct {
	InstanceID string `json:"instance_id"`
}

// TerminateInstances sends terminate commands to specified instances via NATS
// Uses system_powerdown with stop_instance attribute to prevent restart
func TerminateInstances(input *ec2.TerminateInstancesInput, natsConn *nats.Conn, accountID string) (*ec2.TerminateInstancesOutput, error) {
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

		// Send NATS request to the specific instance topic with account ID header
		subject := fmt.Sprintf("ec2.cmd.%s", instanceID)
		reqMsg := nats.NewMsg(subject)
		reqMsg.Data = jsonData
		reqMsg.Header.Set(utils.AccountIDHeader, accountID)
		msg, err := natsConn.RequestMsg(reqMsg, 5*time.Second)
		if err != nil {
			// If no daemon owns this instance, try the ec2.terminate topic for stopped instances
			if errors.Is(err, nats.ErrNoResponders) {
				slog.Info("TerminateInstances: No responder on per-instance topic, trying ec2.terminate", "instance_id", instanceID)

				terminateReq, _ := json.Marshal(terminateStoppedInstanceRequest{InstanceID: instanceID})
				terminateReqMsg := nats.NewMsg("ec2.terminate")
				terminateReqMsg.Data = terminateReq
				terminateReqMsg.Header.Set(utils.AccountIDHeader, accountID)
				terminateMsg, terminateErr := natsConn.RequestMsg(terminateReqMsg, 30*time.Second)
				if terminateErr == nil {
					if _, parseErr := utils.ValidateErrorPayload(terminateMsg.Data); parseErr == nil {
						slog.Info("TerminateInstances: Stopped instance terminated via ec2.terminate", "instance_id", instanceID)
						stateChanges = append(stateChanges, newStateChange(instanceID, 32, "shutting-down", 80, "stopped"))
						continue
					}
				}
			}

			slog.Error("TerminateInstances: Failed to send command", "instance_id", instanceID, "err", err)
			stateChanges = append(stateChanges, newStateChange(instanceID, 16, "running", 16, "running"))
			continue
		}

		// Check if the daemon returned an error response (e.g. ownership check failure)
		if responseError, parseErr := utils.ValidateErrorPayload(msg.Data); parseErr != nil {
			slog.Error("TerminateInstances: Daemon returned error", "instance_id", instanceID, "code", *responseError.Code)
			return nil, errors.New(*responseError.Code)
		}

		slog.Info("TerminateInstances: Command sent successfully", "instance_id", instanceID, "response", string(msg.Data))

		stateChanges = append(stateChanges, newStateChange(instanceID, 32, "shutting-down", 16, "running"))
	}

	output := &ec2.TerminateInstancesOutput{
		TerminatingInstances: stateChanges,
	}

	slog.Info("TerminateInstances: Completed", "total_instances", len(stateChanges))
	return output, nil
}
