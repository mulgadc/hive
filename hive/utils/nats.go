package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

// ConnectNATS establishes a connection to a NATS server with standard reconnect
// handling and logging. If token is non-empty, token authentication is used.
func ConnectNATS(host, token string) (*nats.Conn, error) {
	opts := []nats.Option{
		nats.ReconnectWait(time.Second),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			slog.Debug("NATS disconnected", "err", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			slog.Debug("NATS reconnected", "url", nc.ConnectedUrl())
		}),
	}

	if token != "" {
		opts = append(opts, nats.Token(token))
	}

	nc, err := nats.Connect(host, opts...)
	if err != nil {
		return nil, fmt.Errorf("NATS connect failed: %w", err)
	}

	slog.Debug("Connected to NATS server", "host", host)
	return nc, nil
}

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
		if errors.Is(err, nats.ErrNoResponders) {
			return nil, fmt.Errorf("NATS request to %s: %w", subject, nats.ErrNoResponders)
		}
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
