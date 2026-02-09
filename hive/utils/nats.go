package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// NATSRequest performs a NATS request-response with JSON marshaling.
// It marshals the input, sends to the given subject, validates the response
// for error payloads, and unmarshals the successful response into Out.
func NATSRequest[Out any](conn *nats.Conn, subject string, input any, timeout time.Duration) (*Out, error) {
	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	msg, err := conn.Request(subject, jsonData, timeout)
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	responseError, err := ValidateErrorPayload(msg.Data)
	if err != nil {
		return nil, errors.New(*responseError.Code)
	}

	var output Out
	if err := json.Unmarshal(msg.Data, &output); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}
