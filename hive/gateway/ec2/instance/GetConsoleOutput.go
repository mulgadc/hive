package gateway_ec2_instance

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/nats-io/nats.go"
)

// GetConsoleOutput retrieves console output for a specific instance via NATS.
// Routes directly to the node running the instance via ec2.{instanceID}.GetConsoleOutput.
func GetConsoleOutput(input *ec2.GetConsoleOutputInput, natsConn *nats.Conn) (*ec2.GetConsoleOutputOutput, error) {
	if input.InstanceId == nil || *input.InstanceId == "" {
		return nil, fmt.Errorf("InstanceId is required")
	}

	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	topic := fmt.Sprintf("ec2.%s.GetConsoleOutput", *input.InstanceId)
	msg, err := natsConn.Request(topic, jsonData, 5*time.Second)
	if err != nil {
		if err == nats.ErrNoResponders || err == nats.ErrTimeout {
			return nil, fmt.Errorf("instance %s not found or not running", *input.InstanceId)
		}
		return nil, fmt.Errorf("failed to get console output: %w", err)
	}

	var output ec2.GetConsoleOutputOutput
	if err := json.Unmarshal(msg.Data, &output); err != nil {
		slog.Error("GetConsoleOutput: Failed to unmarshal response", "err", err)
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}
