package dhcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mulgadc/spinifex/spinifex/testutil"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestAcquire_RequestTimeout(t *testing.T) {
	_, nc := testutil.StartTestNATS(t)
	sub, err := nc.Subscribe(TopicAcquire, func(*nats.Msg) {})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	prev := NATSTimeout
	NATSTimeout = 150 * time.Millisecond
	t.Cleanup(func() { NATSTimeout = prev })

	prevMax := AcquireMaxAttempts
	prevDelay := AcquireRetryDelay
	AcquireMaxAttempts = 2
	AcquireRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() {
		AcquireMaxAttempts = prevMax
		AcquireRetryDelay = prevDelay
	})

	_, err = RequestAcquire(nc, "br-wan", "eni-timeout", "eni-timeout", "mulga-spinifex", "wan", "")
	assert.ErrorContains(t, err, "dhcp acquire NATS request")
}

func TestRequestRelease_RequestTimeout(t *testing.T) {
	_, nc := testutil.StartTestNATS(t)
	sub, err := nc.Subscribe(TopicRelease, func(*nats.Msg) {})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	prev := NATSTimeout
	NATSTimeout = 150 * time.Millisecond
	t.Cleanup(func() { NATSTimeout = prev })

	err = RequestRelease(nc, "eni-timeout")
	assert.ErrorContains(t, err, "dhcp release NATS request")
}

func TestRequestAcquire_MalformedReply(t *testing.T) {
	_, nc := testutil.StartTestNATS(t)
	sub, err := nc.Subscribe(TopicAcquire, func(msg *nats.Msg) {
		_ = msg.Respond([]byte("not json"))
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	_, err = RequestAcquire(nc, "br-wan", "eni-malformed", "eni-malformed", "mulga-spinifex", "wan", "")
	assert.ErrorContains(t, err, "unmarshal dhcp acquire reply")
}

func TestRequestRelease_MalformedReply(t *testing.T) {
	_, nc := testutil.StartTestNATS(t)
	sub, err := nc.Subscribe(TopicRelease, func(msg *nats.Msg) {
		_ = msg.Respond([]byte("not json"))
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	err = RequestRelease(nc, "eni-malformed")
	assert.ErrorContains(t, err, "unmarshal dhcp release reply")
}

func TestRequestRelease_ReplyError(t *testing.T) {
	_, nc := testutil.StartTestNATS(t)
	sub, err := nc.Subscribe(TopicRelease, func(msg *nats.Msg) {
		data, _ := json.Marshal(ReleaseReplyMsg{Error: "vpcd rejected release"})
		_ = msg.Respond(data)
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	err = RequestRelease(nc, "eni-rejected")
	assert.ErrorContains(t, err, "vpcd rejected release")
}

func TestRequestAcquire_Guards(t *testing.T) {
	_, err := RequestAcquire(nil, "br-wan", "eni-1", "eni-1", "mulga-spinifex", "wan", "")
	assert.ErrorContains(t, err, "NATS connection is required")

	_, nc := testutil.StartTestNATS(t)
	_, err = RequestAcquire(nc, "", "eni-1", "eni-1", "mulga-spinifex", "wan", "")
	assert.ErrorContains(t, err, "bridge name is required")

	_, err = RequestAcquire(nc, "br-wan", "", "eni-1", "mulga-spinifex", "wan", "")
	assert.ErrorContains(t, err, "client ID is required")
}

func TestRequestRelease_NoopWhenNilOrEmpty(t *testing.T) {
	assert.NoError(t, RequestRelease(nil, "eni-1"))
	_, nc := testutil.StartTestNATS(t)
	assert.NoError(t, RequestRelease(nc, ""))
}
