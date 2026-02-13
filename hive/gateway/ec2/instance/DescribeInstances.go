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

// DescribeInstances queries all hive nodes for their instances via NATS
// and aggregates the results into a single response
func DescribeInstances(input *ec2.DescribeInstancesInput, natsConn *nats.Conn, expectedNodes int) (*ec2.DescribeInstancesOutput, error) {
	// Marshal input to JSON
	jsonData, err := json.Marshal(input)
	if err != nil {
		slog.Error("DescribeInstances: Failed to marshal input", "err", err)
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Create an inbox for collecting responses from all nodes
	inbox := nats.NewInbox()
	sub, err := natsConn.SubscribeSync(inbox)
	if err != nil {
		slog.Error("DescribeInstances: Failed to create inbox subscription", "err", err)
		return nil, fmt.Errorf("failed to create inbox: %w", err)
	}
	defer sub.Unsubscribe()

	// Publish request to all nodes (no queue group, so all daemons receive it)
	err = natsConn.PublishRequest("ec2.DescribeInstances", inbox, jsonData)
	if err != nil {
		slog.Error("DescribeInstances: Failed to publish request", "err", err)
		return nil, fmt.Errorf("failed to publish request: %w", err)
	}

	// Collect responses from all nodes
	// Timeout serves as a safety mechanism in case some nodes don't respond
	timeout := 3 * time.Second
	deadline := time.Now().Add(timeout)

	var allReservations []*ec2.Reservation
	responsesReceived := 0

	// If expectedNodes is not configured (0), fall back to timeout-based collection
	if expectedNodes <= 0 {
		expectedNodes = -1 // Disable early exit
		slog.Warn("DescribeInstances: ExpectedNodes not configured, using timeout-only collection")
	}

	for time.Now().Before(deadline) {
		// Check if we've received responses from all expected nodes
		if expectedNodes > 0 && responsesReceived >= expectedNodes {
			slog.Info("DescribeInstances: Received responses from all expected nodes", "expected", expectedNodes, "received", responsesReceived)
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
			slog.Error("DescribeInstances: Error receiving message", "err", err)
			break
		}

		// Increment response counter (even for errors, as we heard from the node)
		responsesReceived++

		// Check if response is an error
		responseError, err := utils.ValidateErrorPayload(msg.Data)
		if err != nil {
			// Response is an error payload - log but continue collecting from other nodes
			slog.Warn("DescribeInstances: Received error from node", "code", responseError.Code, "responses_received", responsesReceived)
			continue
		}

		// Parse the DescribeInstancesOutput from this node
		var nodeOutput ec2.DescribeInstancesOutput
		err = json.Unmarshal(msg.Data, &nodeOutput)
		if err != nil {
			slog.Error("DescribeInstances: Failed to unmarshal node response", "err", err)
			continue
		}

		// Aggregate reservations from this node
		if nodeOutput.Reservations != nil {
			allReservations = append(allReservations, nodeOutput.Reservations...)
			slog.Info("DescribeInstances: Collected reservations from node", "count", len(nodeOutput.Reservations), "responses_received", responsesReceived)
		}
	}

	// Query stopped instances from shared KV (via queue group — only one daemon handles it)
	stoppedMsg, err := natsConn.Request("ec2.DescribeStoppedInstances", jsonData, 3*time.Second)
	if err != nil {
		slog.Warn("DescribeInstances: Failed to query stopped instances (may not be available)", "err", err)
	} else {
		responseError, parseErr := utils.ValidateErrorPayload(stoppedMsg.Data)
		if parseErr != nil {
			slog.Warn("DescribeInstances: Stopped instances query returned error", "code", responseError.Code)
		} else {
			var stoppedOutput ec2.DescribeInstancesOutput
			if err := json.Unmarshal(stoppedMsg.Data, &stoppedOutput); err != nil {
				slog.Error("DescribeInstances: Failed to unmarshal stopped instances response", "err", err)
			} else if stoppedOutput.Reservations != nil {
				allReservations = append(allReservations, stoppedOutput.Reservations...)
				slog.Info("DescribeInstances: Collected stopped instance reservations", "count", len(stoppedOutput.Reservations))
			}
		}
	}

	// Build final aggregated response
	output := &ec2.DescribeInstancesOutput{
		Reservations: allReservations,
	}

	slog.Info("DescribeInstances: Aggregated response", "total_reservations", len(allReservations))
	return output, nil
}
