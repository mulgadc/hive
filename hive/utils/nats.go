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

// AccountIDHeader is the NATS message header key used to pass the caller's
// AWS account ID from the gateway to daemon handlers.
const AccountIDHeader = "X-Account-ID"

// NATSRequest performs a NATS request-response with JSON marshaling.
// It marshals the input, sends to the given subject with the X-Account-ID
// header, validates the response for error payloads, and unmarshals the
// successful response into Out. Handlers can ignore the account ID if the
// operation is unscoped (e.g. DescribeInstanceTypes).
func NATSRequest[Out any](conn *nats.Conn, subject string, input any, timeout time.Duration, accountID string) (*Out, error) {
	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	reqMsg := nats.NewMsg(subject)
	reqMsg.Data = jsonData
	reqMsg.Header.Set(AccountIDHeader, accountID)

	msg, err := conn.RequestMsg(reqMsg, timeout)
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

// NATSScatterGather publishes a fan-out request and collects responses from
// multiple nodes. It returns the first successful (non-error) response. Error
// payloads from individual nodes are skipped. If all responses are errors, the
// last error is returned. If no responses arrive before the deadline, a timeout
// error is returned. When expectedNodes > 0, collection exits early once that
// many responses have been received.
func NATSScatterGather[Out any](conn *nats.Conn, subject string, input any, timeout time.Duration, expectedNodes int, accountID string) (*Out, error) {
	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	inbox := nats.NewInbox()
	sub, err := conn.SubscribeSync(inbox)
	if err != nil {
		return nil, fmt.Errorf("failed to create inbox: %w", err)
	}
	defer sub.Unsubscribe()

	pubMsg := nats.NewMsg(subject)
	pubMsg.Reply = inbox
	pubMsg.Data = jsonData
	pubMsg.Header.Set(AccountIDHeader, accountID)
	if err := conn.PublishMsg(pubMsg); err != nil {
		return nil, fmt.Errorf("failed to publish request: %w", err)
	}

	deadline := time.Now().Add(timeout)
	responsesReceived := 0
	var lastErr error

	for time.Now().Before(deadline) {
		if expectedNodes > 0 && responsesReceived >= expectedNodes {
			break
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		msg, err := sub.NextMsg(remaining)
		if err != nil {
			if err == nats.ErrTimeout || err == nats.ErrNoResponders {
				break
			}
			return nil, fmt.Errorf("scatter-gather receive error: %w", err)
		}

		responsesReceived++

		responseError, err := ValidateErrorPayload(msg.Data)
		if err != nil {
			slog.Debug("ScatterGather: skipping error response", "code", *responseError.Code, "subject", subject)
			lastErr = errors.New(*responseError.Code)
			continue
		}

		var output Out
		if err := json.Unmarshal(msg.Data, &output); err != nil {
			slog.Debug("ScatterGather: skipping malformed response", "subject", subject, "err", err)
			lastErr = fmt.Errorf("failed to unmarshal response: %w", err)
			continue
		}

		return &output, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("scatter-gather timeout: no responses received for %s", subject)
}

// AccountIDFromMsg extracts the caller's account ID from a NATS message header.
// Returns the account ID, or empty string if the header is not set.
func AccountIDFromMsg(msg *nats.Msg) string {
	if msg == nil || msg.Header == nil {
		return ""
	}
	return msg.Header.Get(AccountIDHeader)
}
