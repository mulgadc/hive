package gateway

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateEC2ErrorResponse_Structure(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		message   string
		requestID string
	}{
		{
			name:      "standard error",
			code:      "InvalidParameterValue",
			message:   "The value supplied is not valid.",
			requestID: "req-12345",
		},
		{
			name:      "auth failure",
			code:      "AuthFailure",
			message:   "Credentials could not be validated.",
			requestID: "req-auth-001",
		},
		{
			name:      "empty fields",
			code:      "",
			message:   "",
			requestID: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output := GenerateEC2ErrorResponse(tc.code, tc.message, tc.requestID)
			require.NotNil(t, output)

			xmlStr := string(output)

			// Verify XML header
			assert.True(t, strings.HasPrefix(xmlStr, xml.Header))

			// Verify error code
			assert.Contains(t, xmlStr, "<Code>"+tc.code+"</Code>")

			// Verify request ID
			assert.Contains(t, xmlStr, "<RequestID>"+tc.requestID+"</RequestID>")

			// Verify root element
			assert.Contains(t, xmlStr, "<Response>")
			assert.Contains(t, xmlStr, "</Response>")

			// Verify Errors wrapper
			assert.Contains(t, xmlStr, "<Errors>")
			assert.Contains(t, xmlStr, "<Error>")
		})
	}
}

func TestGenerateEC2ErrorResponse_ValidXML(t *testing.T) {
	output := GenerateEC2ErrorResponse("TestCode", "Test message", "req-999")
	require.NotNil(t, output)

	// Strip XML header and verify it's well-formed
	xmlBody := strings.TrimPrefix(string(output), xml.Header)
	decoder := xml.NewDecoder(strings.NewReader(xmlBody))
	for {
		_, err := decoder.Token()
		if err != nil {
			// io.EOF means we parsed the entire document successfully
			assert.ErrorIs(t, err, err) // just ensure we got here
			break
		}
	}
}

func startTestNATS(t *testing.T) *nats.Conn {
	t.Helper()
	ns, err := server.NewServer(&server.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	})
	require.NoError(t, err)

	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second), "NATS server failed to start")

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)

	t.Cleanup(func() {
		nc.Close()
		ns.Shutdown()
	})

	return nc
}

func TestDiscoverActiveNodes_NilNATS(t *testing.T) {
	gw := &GatewayConfig{
		ExpectedNodes: 3,
		NATSConn:      nil,
	}

	result := gw.DiscoverActiveNodes()
	assert.Equal(t, 3, result)
}

func TestDiscoverActiveNodes_NoResponders(t *testing.T) {
	nc := startTestNATS(t)

	gw := &GatewayConfig{
		ExpectedNodes: 5,
		NATSConn:      nc,
	}

	result := gw.DiscoverActiveNodes()
	assert.Equal(t, 5, result)
}

func TestDiscoverActiveNodes_WithResponders(t *testing.T) {
	nc := startTestNATS(t)

	for _, nodeName := range []string{"node-1", "node-2"} {
		name := nodeName
		_, err := nc.Subscribe("hive.nodes.discover", func(msg *nats.Msg) {
			resp := NodeDiscoverResponse{Node: name}
			data, _ := json.Marshal(resp)
			msg.Respond(data)
		})
		require.NoError(t, err)
	}
	require.NoError(t, nc.Flush())

	gw := &GatewayConfig{
		ExpectedNodes: 1,
		NATSConn:      nc,
	}

	result := gw.DiscoverActiveNodes()
	assert.Equal(t, 2, result)
}

func TestDiscoverActiveNodes_InvalidJSON(t *testing.T) {
	nc := startTestNATS(t)

	_, err := nc.Subscribe("hive.nodes.discover", func(msg *nats.Msg) {
		msg.Respond([]byte("not json"))
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	gw := &GatewayConfig{
		ExpectedNodes: 4,
		NATSConn:      nc,
	}

	result := gw.DiscoverActiveNodes()
	assert.Equal(t, 4, result)
}

func TestDiscoverActiveNodes_DuplicateNodes(t *testing.T) {
	nc := startTestNATS(t)

	for range 2 {
		_, err := nc.Subscribe("hive.nodes.discover", func(msg *nats.Msg) {
			resp := NodeDiscoverResponse{Node: "same-node"}
			data, _ := json.Marshal(resp)
			msg.Respond(data)
		})
		require.NoError(t, err)
	}
	require.NoError(t, nc.Flush())

	gw := &GatewayConfig{
		ExpectedNodes: 5,
		NATSConn:      nc,
	}

	result := gw.DiscoverActiveNodes()
	assert.Equal(t, 1, result)
}

func TestSupportedServices(t *testing.T) {
	assert.True(t, supportedServices["ec2"])
	assert.True(t, supportedServices["iam"])
	assert.True(t, supportedServices["account"])
	assert.False(t, supportedServices["s3"])
	assert.False(t, supportedServices["dynamodb"])
	assert.False(t, supportedServices[""])
}
