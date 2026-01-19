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

// DescribeInstanceTypes queries all hive nodes for their instance types via NATS
func DescribeInstanceTypes(input *ec2.DescribeInstanceTypesInput, natsConn *nats.Conn, expectedNodes int) (*ec2.DescribeInstanceTypesOutput, error) {
	// Marshal input to JSON
	jsonData, err := json.Marshal(input)
	if err != nil {
		slog.Error("DescribeInstanceTypes: Failed to marshal input", "err", err)
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Create an inbox for collecting responses from all nodes
	inbox := nats.NewInbox()
	sub, err := natsConn.SubscribeSync(inbox)
	if err != nil {
		slog.Error("DescribeInstanceTypes: Failed to create inbox subscription", "err", err)
		return nil, fmt.Errorf("failed to create inbox: %w", err)
	}
	defer sub.Unsubscribe()

	// Publish request to all nodes (no queue group, so all daemons receive it)
	err = natsConn.PublishRequest("ec2.DescribeInstanceTypes", inbox, jsonData)
	if err != nil {
		slog.Error("DescribeInstanceTypes: Failed to publish request", "err", err)
		return nil, fmt.Errorf("failed to publish request: %w", err)
	}

	// Collect responses from all nodes
	// Timeout serves as a safety mechanism in case some nodes don't respond
	timeout := 3 * time.Second
	deadline := time.Now().Add(timeout)

	var allInstanceTypes []*ec2.InstanceTypeInfo
	responsesReceived := 0

	if expectedNodes <= 0 {
		expectedNodes = -1 // Disable early exit
		slog.Warn("DescribeInstanceTypes: ExpectedNodes not configured, using timeout-only collection")
	}

	for time.Now().Before(deadline) {
		// Check if we've received responses from all expected nodes
		if expectedNodes > 0 && responsesReceived >= expectedNodes {
			slog.Info("DescribeInstanceTypes: Received responses from all expected nodes", "expected", expectedNodes, "received", responsesReceived)
			break
		}

		// Calculate remaining timeout
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		// Wait for next message with remaining timeout
		msg, err := sub.NextMsg(remaining)
		if err != nil {
			if err == nats.ErrTimeout {
				// Timeout reached, no more messages
				break
			}
			slog.Error("DescribeInstanceTypes: Error receiving message", "err", err)
			break
		}

		// Increment response counter (even for errors, as we heard from the node)
		responsesReceived++

		// Check if response is an error
		responseError, err := utils.ValidateErrorPayload(msg.Data)
		if err != nil {
			// Response is an error payload - log but continue collecting from other nodes
			slog.Warn("DescribeInstanceTypes: Received error from node", "code", responseError.Code, "responses_received", responsesReceived)
			continue
		}

		// Parse the DescribeInstanceTypesOutput from this node
		var nodeOutput ec2.DescribeInstanceTypesOutput
		err = json.Unmarshal(msg.Data, &nodeOutput)
		if err != nil {
			slog.Error("DescribeInstanceTypes: Failed to unmarshal node response", "err", err)
			continue
		}

		// Aggregate instance types from this node
		if nodeOutput.InstanceTypes != nil {
			allInstanceTypes = append(allInstanceTypes, nodeOutput.InstanceTypes...)
			slog.Info("DescribeInstanceTypes: Collected instance types from node", "count", len(nodeOutput.InstanceTypes), "responses_received", responsesReceived)
		}
	}

	// By default, deduplicate instance types.
	// If the "capacity" filter is set to "true", show all available slots (duplicates).
	showCapacity := false
	for _, f := range input.Filters {
		if f.Name != nil && *f.Name == "capacity" {
			for _, v := range f.Values {
				if v != nil && *v == "true" {
					showCapacity = true
					break
				}
			}
		}
	}

	var finalInstanceTypes []*ec2.InstanceTypeInfo
	if showCapacity {
		finalInstanceTypes = allInstanceTypes
	} else {
		seen := make(map[string]bool)
		for _, it := range allInstanceTypes {
			if it != nil && it.InstanceType != nil {
				if !seen[*it.InstanceType] {
					seen[*it.InstanceType] = true
					finalInstanceTypes = append(finalInstanceTypes, it)
				}
			}
		}
	}

	// Build final aggregated response
	output := &ec2.DescribeInstanceTypesOutput{
		InstanceTypes: finalInstanceTypes,
	}

	slog.Info("DescribeInstanceTypes: Aggregated response", "total_instance_types", len(finalInstanceTypes), "show_capacity", showCapacity)
	return output, nil
}
