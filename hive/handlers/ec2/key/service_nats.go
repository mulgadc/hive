package handlers_ec2_key

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSKeyService handles key operations via NATS messaging
type NATSKeyService struct {
	natsConn *nats.Conn
}

// NewNATSKeyService creates a new NATS-based key service
func NewNATSKeyService(conn *nats.Conn) KeyService {
	return &NATSKeyService{natsConn: conn}
}

// CreateKeyPair sends a CreateKeyPair request to the daemon via NATS
func (s *NATSKeyService) CreateKeyPair(input *ec2.CreateKeyPairInput) (*ec2.CreateKeyPairOutput, error) {
	// Marshal input to JSON
	jsonData, err := json.Marshal(input)
	if err != nil {
		slog.Error("NATSKeyService: Failed to marshal CreateKeyPairInput", "err", err)
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Send request to daemon via NATS with 30 second timeout
	msg, err := s.natsConn.Request("ec2.CreateKeyPair", jsonData, 30*time.Second)
	if err != nil {
		slog.Error("NATSKeyService: Failed to send NATS request", "err", err)
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	// Check if the response is an error
	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		// Response is an error payload
		slog.Error("NATSKeyService: Received error response from daemon", "code", responseError.Code)
		return nil, fmt.Errorf("daemon returned error: %s", *responseError.Code)
	}

	// Unmarshal successful response
	var output ec2.CreateKeyPairOutput
	err = json.Unmarshal(msg.Data, &output)
	if err != nil {
		slog.Error("NATSKeyService: Failed to unmarshal output", "err", err)
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}

func (s *NATSKeyService) DeleteKeyPair(input *ec2.DeleteKeyPairInput) (*ec2.DeleteKeyPairOutput, error) {
	// Marshal input to JSON
	jsonData, err := json.Marshal(input)
	if err != nil {
		slog.Error("NATSKeyService: Failed to marshal DeleteKeyPairInput", "err", err)
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Send request to daemon via NATS with 30 second timeout
	msg, err := s.natsConn.Request("ec2.DeleteKeyPair", jsonData, 30*time.Second)
	if err != nil {
		slog.Error("NATSKeyService: Failed to send NATS request", "err", err)
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	// Check if the response is an error
	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		// Response is an error payload
		slog.Error("NATSKeyService: Received error response from daemon", "code", responseError.Code)
		return nil, fmt.Errorf("daemon returned error: %s", *responseError.Code)
	}

	// Unmarshal successful response
	var output ec2.DeleteKeyPairOutput
	err = json.Unmarshal(msg.Data, &output)
	if err != nil {
		slog.Error("NATSKeyService: Failed to unmarshal output", "err", err)
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}

func (s *NATSKeyService) DescribeKeyPairs(input *ec2.DescribeKeyPairsInput) (*ec2.DescribeKeyPairsOutput, error) {
	// Marshal input to JSON
	jsonData, err := json.Marshal(input)
	if err != nil {
		slog.Error("NATSKeyService: Failed to marshal DescribeKeyPairsInput", "err", err)
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Send request to daemon via NATS with 30 second timeout
	msg, err := s.natsConn.Request("ec2.DescribeKeyPairs", jsonData, 30*time.Second)
	if err != nil {
		slog.Error("NATSKeyService: Failed to send NATS request", "err", err)
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	// Check if the response is an error
	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		// Response is an error payload
		slog.Error("NATSKeyService: Received error response from daemon", "code", responseError.Code)
		return nil, fmt.Errorf("daemon returned error: %s", *responseError.Code)
	}

	// Unmarshal successful response
	var output ec2.DescribeKeyPairsOutput
	err = json.Unmarshal(msg.Data, &output)
	if err != nil {
		slog.Error("NATSKeyService: Failed to unmarshal output", "err", err)
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}

func (s *NATSKeyService) ImportKeyPair(input *ec2.ImportKeyPairInput) (*ec2.ImportKeyPairOutput, error) {
	// Marshal input to JSON
	jsonData, err := json.Marshal(input)
	if err != nil {
		slog.Error("NATSKeyService: Failed to marshal ImportKeyPairInput", "err", err)
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Send request to daemon via NATS with 30 second timeout
	msg, err := s.natsConn.Request("ec2.ImportKeyPair", jsonData, 30*time.Second)
	if err != nil {
		slog.Error("NATSKeyService: Failed to send NATS request", "err", err)
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	// Check if the response is an error
	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		// Response is an error payload
		slog.Error("NATSKeyService: Received error response from daemon", "code", responseError.Code)
		return nil, fmt.Errorf("daemon returned error: %s", *responseError.Code)
	}

	// Unmarshal successful response
	var output ec2.ImportKeyPairOutput
	err = json.Unmarshal(msg.Data, &output)
	if err != nil {
		slog.Error("NATSKeyService: Failed to unmarshal output", "err", err)
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}
