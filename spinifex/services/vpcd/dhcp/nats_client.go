package dhcp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

// NATSTimeout bounds the request/reply round-trip to vpcd's DHCPManager.
// Picked to comfortably exceed the DORA handshake (15 s default + server
// jitter + ~10 s of slack for NAK + fresh DISCOVER fallbacks). A var rather
// than a const so tests can drive the timeout error path without waiting
// 20 s per table entry.
var NATSTimeout = 30 * time.Second

// AcquireMaxAttempts caps retry of vpc.dhcp.acquire. 60 attempts × 1 s sleep
// covers a cold-boot vpcd start window where NATS is up but the dhcp.Manager
// hasn't yet subscribed (typical: 5–30 s, pathological: 60 s during full
// reconcile). Tests override to keep timeout-path coverage fast.
var AcquireMaxAttempts = 60

// AcquireRetryDelay is the wait between retries. Var for the same reason as
// AcquireMaxAttempts.
var AcquireRetryDelay = 1 * time.Second

// LeaseResult is the data callers need from a successful acquire. Mirrors
// AcquireReplyMsg but exposed as a typed result so callers don't depend on
// the wire struct.
type LeaseResult struct {
	IP          string
	SubnetMask  string
	Routers     []string
	DNS         []string
	ServerID    string
	HWAddr      string
	ExpiresUnix int64
}

// RequestAcquire asks vpcd's DHCPManager (over NATS) to acquire a DHCP lease
// on the given bridge, identifying the caller with option 61 (client-id),
// option 12 (hostname) and option 60 (vendor class). Blocks until vpcd
// replies or NATSTimeout expires. The Manager-side handler is idempotent on
// clientID: a second call with the same clientID while a live lease exists
// returns the same lease without a fresh DORA, so CAS retry loops on the
// caller are safe.
func RequestAcquire(nc *nats.Conn, bridge, clientID, hostname, vendorClass, poolName, hwAddr string) (LeaseResult, error) {
	if nc == nil {
		return LeaseResult{}, fmt.Errorf("DHCP lease: NATS connection is required")
	}
	if bridge == "" {
		return LeaseResult{}, fmt.Errorf("DHCP lease: bridge name is required")
	}
	if clientID == "" {
		return LeaseResult{}, fmt.Errorf("DHCP lease: client ID is required")
	}

	req := AcquireRequestMsg{
		Bridge:      bridge,
		ClientID:    clientID,
		Hostname:    hostname,
		VendorClass: vendorClass,
		PoolName:    poolName,
		HWAddr:      hwAddr,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return LeaseResult{}, fmt.Errorf("marshal dhcp acquire request: %w", err)
	}

	var reply AcquireReplyMsg
	maxAttempts := AcquireMaxAttempts

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		msg, err := nc.Request(TopicAcquire, data, NATSTimeout)
		if err != nil {
			if attempt == maxAttempts {
				return LeaseResult{}, fmt.Errorf(
					"dhcp acquire NATS request failed after %d attempts (client %s): %w",
					maxAttempts, clientID, err,
				)
			}
			slog.Warn("dhcp acquire NATS request failed, retrying",
				"clientID", clientID,
				"attempt", attempt,
				"maxAttempts", maxAttempts,
				"err", err,
			)
			time.Sleep(AcquireRetryDelay)
			continue
		}

		if err := json.Unmarshal(msg.Data, &reply); err != nil {
			return LeaseResult{}, fmt.Errorf("unmarshal dhcp acquire reply: %w", err)
		}
		if reply.Error != "" {
			return LeaseResult{}, fmt.Errorf("dhcp acquire client %s: %s", clientID, reply.Error)
		}
		break
	}

	slog.Info("DHCP lease obtained",
		"bridge", bridge,
		"client_id", clientID,
		"ip", reply.IP,
		"server_id", reply.ServerID,
		"expires_unix", reply.ExpiresUnix,
	)

	return LeaseResult{
		IP:          reply.IP,
		SubnetMask:  reply.SubnetMask,
		Routers:     reply.Routers,
		DNS:         reply.DNS,
		ServerID:    reply.ServerID,
		HWAddr:      reply.HWAddr,
		ExpiresUnix: reply.ExpiresUnix,
	}, nil
}

// RequestRelease asks vpcd to release the lease identified by clientID.
// Returns nil silently when nc or clientID is empty so callers in clean-up
// paths can invoke it unconditionally.
func RequestRelease(nc *nats.Conn, clientID string) error {
	if nc == nil || clientID == "" {
		return nil
	}

	data, err := json.Marshal(ReleaseRequestMsg{ClientID: clientID})
	if err != nil {
		return fmt.Errorf("marshal dhcp release request: %w", err)
	}

	msg, err := nc.Request(TopicRelease, data, NATSTimeout)
	if err != nil {
		return fmt.Errorf("dhcp release NATS request (client %s): %w", clientID, err)
	}

	var reply ReleaseReplyMsg
	if err := json.Unmarshal(msg.Data, &reply); err != nil {
		return fmt.Errorf("unmarshal dhcp release reply: %w", err)
	}
	if reply.Error != "" {
		return fmt.Errorf("dhcp release (client %s): %s", clientID, reply.Error)
	}

	slog.Info("DHCP lease released", "client_id", clientID)
	return nil
}
