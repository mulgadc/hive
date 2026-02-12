package gateway_ec2_instance

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// startStoppedInstanceRequest is the payload sent to the ec2.start topic
type startStoppedInstanceRequest struct {
	InstanceID string `json:"instance_id"`
}

// StartInstances sends start requests to the ec2.start queue group topic.
// Any available daemon can pick up the request and launch the stopped instance.
func StartInstances(input *ec2.StartInstancesInput, natsConn *nats.Conn) (*ec2.StartInstancesOutput, error) {
	if len(input.InstanceIds) == 0 {
		return nil, fmt.Errorf("no instance IDs provided")
	}

	slog.Info("StartInstances: Processing request", "instance_count", len(input.InstanceIds))

	var stateChanges []*ec2.InstanceStateChange

	for _, instanceIDPtr := range input.InstanceIds {
		if instanceIDPtr == nil {
			continue
		}
		instanceID := *instanceIDPtr

		req := startStoppedInstanceRequest{InstanceID: instanceID}
		jsonData, err := json.Marshal(req)
		if err != nil {
			slog.Error("StartInstances: Failed to marshal request", "instance_id", instanceID, "err", err)
			continue
		}

		slog.Info("StartInstances: Sending NATS request", "subject", "ec2.start", "instance_id", instanceID)

		msg, err := natsConn.Request("ec2.start", jsonData, 30*time.Second)
		if err != nil {
			slog.Error("StartInstances: Failed to send start request", "instance_id", instanceID, "err", err)
			stateChanges = append(stateChanges, newStateChange(instanceID, 80, "stopped", 80, "stopped"))
			continue
		}

		// Check if the daemon returned an error response
		if responseError, parseErr := utils.ValidateErrorPayload(msg.Data); parseErr != nil {
			slog.Error("StartInstances: Daemon returned error", "instance_id", instanceID, "code", responseError.Code)
			stateChanges = append(stateChanges, newStateChange(instanceID, 80, "stopped", 80, "stopped"))
			continue
		}

		slog.Info("StartInstances: Command sent successfully", "instance_id", instanceID, "response", string(msg.Data))
		stateChanges = append(stateChanges, newStateChange(instanceID, 0, "pending", 80, "stopped"))
	}

	output := &ec2.StartInstancesOutput{
		StartingInstances: stateChanges,
	}

	slog.Info("StartInstances: Completed", "total_instances", len(stateChanges))
	return output, nil
}
