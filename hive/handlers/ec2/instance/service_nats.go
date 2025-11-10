package handlers_ec2_instance

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSInstanceService handles instance operations via NATS messaging
type NATSInstanceService struct {
	natsConn *nats.Conn
}

// NewNATSInstanceService creates a new NATS-based instance service
func NewNATSInstanceService(conn *nats.Conn) InstanceService {
	return &NATSInstanceService{natsConn: conn}
}

// RunInstances sends a RunInstances request to the daemon via NATS
func (s *NATSInstanceService) RunInstances(input *ec2.RunInstancesInput) (*ec2.Reservation, error) {
	// Marshal input to JSON
	jsonData, err := json.Marshal(input)
	if err != nil {
		slog.Error("NATSInstanceService: Failed to marshal RunInstancesInput", "err", err)
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Send request to daemon via NATS with 5 minute timeout
	msg, err := s.natsConn.Request("ec2.RunInstances", jsonData, 5*time.Minute)
	if err != nil {
		slog.Error("NATSInstanceService: Failed to send NATS request", "err", err)
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	// Check if the response is an error
	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		// Response is an error payload
		slog.Error("NATSInstanceService: Received error response from daemon", "code", responseError.Code)
		return nil, fmt.Errorf("daemon returned error: %s", *responseError.Code)
	}

	// Unmarshal successful response
	var reservation ec2.Reservation
	err = json.Unmarshal(msg.Data, &reservation)
	if err != nil {
		slog.Error("NATSInstanceService: Failed to unmarshal reservation", "err", err)
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &reservation, nil
}
