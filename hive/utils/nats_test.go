package utils

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startTestNATSServer(t *testing.T) *server.Server {
	t.Helper()

	opts := &server.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	}

	ns, err := server.NewServer(opts)
	require.NoError(t, err)

	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second), "NATS server failed to start")

	t.Cleanup(func() { ns.Shutdown() })
	return ns
}

func TestConnectNATS_Success(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := ConnectNATS(ns.ClientURL(), "")
	require.NoError(t, err)
	defer nc.Close()

	assert.True(t, nc.IsConnected())
}

func TestConnectNATS_WithToken(t *testing.T) {
	opts := &server.Options{
		Host:          "127.0.0.1",
		Port:          -1,
		NoLog:         true,
		NoSigs:        true,
		Authorization: "test-token-123",
	}

	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	// With correct token — should succeed
	nc, err := ConnectNATS(ns.ClientURL(), "test-token-123")
	require.NoError(t, err)
	defer nc.Close()
	assert.True(t, nc.IsConnected())
}

func TestConnectNATS_WrongToken(t *testing.T) {
	opts := &server.Options{
		Host:          "127.0.0.1",
		Port:          -1,
		NoLog:         true,
		NoSigs:        true,
		Authorization: "correct-token",
	}

	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	_, err = ConnectNATS(ns.ClientURL(), "wrong-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NATS connect failed")
}

func TestConnectNATS_BadAddress(t *testing.T) {
	_, err := ConnectNATS("nats://127.0.0.1:1", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NATS connect failed")
}

func TestNATSRequest_Success(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Req struct {
		Name string `json:"name"`
	}
	type Resp struct {
		Greeting string `json:"greeting"`
	}

	// Mock responder
	_, err = nc.Subscribe("test.greet", func(msg *nats.Msg) {
		var req Req
		json.Unmarshal(msg.Data, &req)
		resp := Resp{Greeting: "hello " + req.Name}
		data, _ := json.Marshal(resp)
		msg.Respond(data)
	})
	require.NoError(t, err)

	result, err := NATSRequest[Resp](nc, "test.greet", Req{Name: "world"}, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "hello world", result.Greeting)
}

func TestNATSRequest_ErrorResponse(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	// Responder returns an error payload
	_, err = nc.Subscribe("test.fail", func(msg *nats.Msg) {
		errPayload := GenerateErrorPayload("InvalidParameterValue")
		msg.Respond(errPayload)
	})
	require.NoError(t, err)

	type Resp struct{}
	_, err = NATSRequest[Resp](nc, "test.fail", struct{}{}, 2*time.Second)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestNATSRequest_NoResponders(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct{}
	_, err = NATSRequest[Resp](nc, "test.nobody", struct{}{}, 500*time.Millisecond)
	assert.Error(t, err)
}

func TestNATSRequest_Timeout(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	// Responder that never responds
	_, err = nc.QueueSubscribe("test.slow", "q", func(msg *nats.Msg) {
		time.Sleep(5 * time.Second)
	})
	require.NoError(t, err)

	type Resp struct{}
	_, err = NATSRequest[Resp](nc, "test.slow", struct{}{}, 100*time.Millisecond)
	assert.Error(t, err)
}

func TestNATSRequest_InvalidUnmarshal(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	// Responder returns invalid JSON for the expected type
	_, err = nc.Subscribe("test.badjson", func(msg *nats.Msg) {
		msg.Respond([]byte(`not-json`))
	})
	require.NoError(t, err)

	type Resp struct {
		Value int `json:"value"`
	}
	_, err = NATSRequest[Resp](nc, "test.badjson", struct{}{}, 2*time.Second)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

// --- NATSRequestWithAccount tests ---

func TestNATSRequestWithAccount_Success(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Req struct {
		Name string `json:"name"`
	}
	type Resp struct {
		Greeting  string `json:"greeting"`
		AccountID string `json:"account_id"`
	}

	// Responder echoes back the account ID from the header
	_, err = nc.Subscribe("test.account", func(msg *nats.Msg) {
		var req Req
		json.Unmarshal(msg.Data, &req)
		acct := AccountIDFromMsg(msg)
		resp := Resp{Greeting: "hello " + req.Name, AccountID: acct}
		data, _ := json.Marshal(resp)
		msg.Respond(data)
	})
	require.NoError(t, err)

	result, err := NATSRequestWithAccount[Resp](nc, "test.account", Req{Name: "world"}, 2*time.Second, "111122223333")
	require.NoError(t, err)
	assert.Equal(t, "hello world", result.Greeting)
	assert.Equal(t, "111122223333", result.AccountID)
}

func TestNATSRequestWithAccount_ErrorResponse(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	_, err = nc.Subscribe("test.account.fail", func(msg *nats.Msg) {
		msg.Respond(GenerateErrorPayload("InvalidParameterValue"))
	})
	require.NoError(t, err)

	type Resp struct{}
	_, err = NATSRequestWithAccount[Resp](nc, "test.account.fail", struct{}{}, 2*time.Second, "111122223333")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestNATSRequestWithAccount_NoResponders(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct{}
	_, err = NATSRequestWithAccount[Resp](nc, "test.account.nobody", struct{}{}, 500*time.Millisecond, "111122223333")
	assert.Error(t, err)
}

func TestNATSRequestWithAccount_Timeout(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	// Responder that never responds — triggers non-NoResponders NATS error
	_, err = nc.QueueSubscribe("test.account.slow", "q", func(msg *nats.Msg) {
		time.Sleep(5 * time.Second)
	})
	require.NoError(t, err)

	type Resp struct{}
	_, err = NATSRequestWithAccount[Resp](nc, "test.account.slow", struct{}{}, 100*time.Millisecond, "111122223333")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NATS request failed")
}

func TestNATSRequestWithAccount_MarshalError(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct{}
	// Channels cannot be marshaled to JSON
	_, err = NATSRequestWithAccount[Resp](nc, "test.account.marshalfail", make(chan int), 2*time.Second, "111122223333")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal input")
}

func TestNATSRequestWithAccount_InvalidUnmarshal(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	// Responder returns invalid JSON for the expected type
	_, err = nc.Subscribe("test.account.badjson", func(msg *nats.Msg) {
		msg.Respond([]byte(`not-json`))
	})
	require.NoError(t, err)

	type Resp struct {
		Value int `json:"value"`
	}
	_, err = NATSRequestWithAccount[Resp](nc, "test.account.badjson", struct{}{}, 2*time.Second, "111122223333")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

// --- AccountIDFromMsg tests ---

func TestAccountIDFromMsg(t *testing.T) {
	msg := nats.NewMsg("test")
	msg.Header.Set(AccountIDHeader, "444455556666")

	assert.Equal(t, "444455556666", AccountIDFromMsg(msg))
}

func TestAccountIDFromMsg_Missing(t *testing.T) {
	msg := nats.NewMsg("test")
	assert.Equal(t, "", AccountIDFromMsg(msg))
}

func TestAccountIDFromMsg_NilMsg(t *testing.T) {
	assert.Equal(t, "", AccountIDFromMsg(nil))
}

func TestAccountIDFromMsg_NilHeader(t *testing.T) {
	msg := &nats.Msg{Subject: "test"}
	assert.Equal(t, "", AccountIDFromMsg(msg))
}
