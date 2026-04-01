package utils

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mulgadc/spinifex/spinifex/testutil"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startTestNATSServer(t *testing.T) *server.Server {
	t.Helper()
	ns, _ := testutil.StartTestNATS(t)
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

	result, err := NATSRequest[Resp](nc, "test.greet", Req{Name: "world"}, 2*time.Second, "")
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
	_, err = NATSRequest[Resp](nc, "test.fail", struct{}{}, 2*time.Second, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestNATSRequest_NoResponders(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct{}
	_, err = NATSRequest[Resp](nc, "test.nobody", struct{}{}, 500*time.Millisecond, "")
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
	_, err = NATSRequest[Resp](nc, "test.slow", struct{}{}, 100*time.Millisecond, "")
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
	_, err = NATSRequest[Resp](nc, "test.badjson", struct{}{}, 2*time.Second, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

// --- NATSRequest with account ID tests ---

func TestNATSRequest_AccountIDHeader(t *testing.T) {
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

	result, err := NATSRequest[Resp](nc, "test.account", Req{Name: "world"}, 2*time.Second, "111122223333")
	require.NoError(t, err)
	assert.Equal(t, "hello world", result.Greeting)
	assert.Equal(t, "111122223333", result.AccountID)
}

func TestNATSRequest_MarshalError(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct{}
	// Channels cannot be marshaled to JSON
	_, err = NATSRequest[Resp](nc, "test.marshalfail", make(chan int), 2*time.Second, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal input")
}

// --- AccountIDFromMsg tests ---

// --- NATSScatterGather tests ---

func TestNATSScatterGather_SuccessAmongErrors(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct {
		Value string `json:"value"`
	}

	// Simulate 3 nodes: 2 return errors, 1 returns success
	_, err = nc.Subscribe("test.scatter", func(msg *nats.Msg) {
		// Simulate varying response times
		resp := Resp{Value: "found"}
		data, _ := json.Marshal(resp)
		msg.Respond(data)
	})
	require.NoError(t, err)

	// Two error responders (faster)
	for range 2 {
		_, err = nc.Subscribe("test.scatter", func(msg *nats.Msg) {
			errPayload := GenerateErrorPayload("InvalidInstanceID.NotFound")
			msg.Respond(errPayload)
		})
		require.NoError(t, err)
	}

	result, err := NATSScatterGather[Resp](nc, "test.scatter", struct{}{}, 3*time.Second, 3, "")
	require.NoError(t, err)
	assert.Equal(t, "found", result.Value)
}

func TestNATSScatterGather_AllErrors(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct{}

	// All 3 nodes return errors
	for range 3 {
		_, err = nc.Subscribe("test.scatter.allerr", func(msg *nats.Msg) {
			errPayload := GenerateErrorPayload("InvalidInstanceID.NotFound")
			msg.Respond(errPayload)
		})
		require.NoError(t, err)
	}

	_, err = NATSScatterGather[Resp](nc, "test.scatter.allerr", struct{}{}, 2*time.Second, 3, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidInstanceID.NotFound")
}

func TestNATSScatterGather_Timeout(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct{}

	// No subscribers — should return a timeout error
	_, err = NATSScatterGather[Resp](nc, "test.scatter.timeout", struct{}{}, 200*time.Millisecond, 0, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no responses received")
}

func TestNATSScatterGather_SingleSuccess(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct {
		ID string `json:"id"`
	}

	_, err = nc.Subscribe("test.scatter.single", func(msg *nats.Msg) {
		resp := Resp{ID: "ami-123"}
		data, _ := json.Marshal(resp)
		msg.Respond(data)
	})
	require.NoError(t, err)

	result, err := NATSScatterGather[Resp](nc, "test.scatter.single", struct{}{}, 2*time.Second, 1, "acct-123")
	require.NoError(t, err)
	assert.Equal(t, "ami-123", result.ID)
}

func TestNATSScatterGather_MarshalError(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct{}
	_, err = NATSScatterGather[Resp](nc, "test.scatter.marshal", make(chan int), 2*time.Second, 0, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal input")
}

func TestNATSScatterGather_EarlyExitOnSuccess(t *testing.T) {
	ns := startTestNATSServer(t)

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	defer nc.Close()

	type Resp struct {
		Value string `json:"value"`
	}

	// One fast success responder — should return immediately without waiting
	_, err = nc.Subscribe("test.scatter.early", func(msg *nats.Msg) {
		resp := Resp{Value: "quick"}
		data, _ := json.Marshal(resp)
		msg.Respond(data)
	})
	require.NoError(t, err)

	start := time.Now()
	result, err := NATSScatterGather[Resp](nc, "test.scatter.early", struct{}{}, 5*time.Second, 0, "")
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "quick", result.Value)
	// Should return well before the 5s timeout
	assert.Less(t, elapsed, 2*time.Second)
}

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
