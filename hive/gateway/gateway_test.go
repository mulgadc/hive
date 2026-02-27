package gateway

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/mulgadc/hive/hive/awserrors"
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
			assert.ErrorIs(t, err, io.EOF)
			break
		}
	}
}

func TestGenerateIAMErrorResponse_Structure(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		message   string
		requestID string
	}{
		{
			name:      "entity not found",
			code:      "NoSuchEntity",
			message:   "The request was rejected because it referenced a resource entity that does not exist.",
			requestID: "req-iam-001",
		},
		{
			name:      "entity already exists",
			code:      "EntityAlreadyExists",
			message:   "The request was rejected because it attempted to create a resource that already exists.",
			requestID: "req-iam-002",
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
			output := GenerateIAMErrorResponse(tc.code, tc.message, tc.requestID)
			require.NotNil(t, output)

			xmlStr := string(output)

			// Verify XML header
			assert.True(t, strings.HasPrefix(xmlStr, xml.Header))

			// IAM uses <ErrorResponse> root, not <Response>
			assert.Contains(t, xmlStr, "<ErrorResponse>")
			assert.Contains(t, xmlStr, "</ErrorResponse>")
			assert.NotContains(t, xmlStr, "<Response>")

			// Verify IAM-specific structure
			assert.Contains(t, xmlStr, "<Type>Sender</Type>")
			assert.Contains(t, xmlStr, "<Code>"+tc.code+"</Code>")
			assert.Contains(t, xmlStr, "<RequestId>"+tc.requestID+"</RequestId>")
		})
	}
}

func TestGenerateIAMErrorResponse_ValidXML(t *testing.T) {
	output := GenerateIAMErrorResponse("NoSuchEntity", "Entity not found", "req-iam-999")
	require.NotNil(t, output)

	xmlBody := strings.TrimPrefix(string(output), xml.Header)
	decoder := xml.NewDecoder(strings.NewReader(xmlBody))
	for {
		_, err := decoder.Token()
		if err != nil {
			assert.ErrorIs(t, err, io.EOF)
			break
		}
	}
}

func TestErrorHandler_IAMService(t *testing.T) {
	gw := &GatewayConfig{DisableLogging: true}
	app := fiber.New(fiber.Config{
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			return gw.ErrorHandler(ctx, err)
		},
	})
	app.Get("/", func(c *fiber.Ctx) error {
		c.Locals("sigv4.service", "iam")
		return errors.New(awserrors.ErrorIAMNoSuchEntity)
	})

	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	xmlStr := string(body)
	// IAM format uses <ErrorResponse> not <Response>
	assert.Contains(t, xmlStr, "<ErrorResponse>")
	assert.Contains(t, xmlStr, "<Type>Sender</Type>")
	assert.Contains(t, xmlStr, "<Code>NoSuchEntity</Code>")
	assert.NotContains(t, xmlStr, "<Errors>")
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

func TestParseAWSQueryArgs(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected map[string]string
	}{
		{
			name:  "simple action and version",
			query: "Action=DescribeInstances&Version=2016-11-15",
			expected: map[string]string{
				"Action":  "DescribeInstances",
				"Version": "2016-11-15",
			},
		},
		{
			name:  "URL-encoded values",
			query: "Name=%2Fdev%2Fsda&Value=hello%20world",
			expected: map[string]string{
				"Name":  "/dev/sda",
				"Value": "hello world",
			},
		},
		{
			name:  "key without value",
			query: "DryRun",
			expected: map[string]string{
				"DryRun": "",
			},
		},
		{
			name:     "empty string",
			query:    "",
			expected: map[string]string{"": ""},
		},
		{
			name:  "multiple parameters",
			query: "Action=RunInstances&ImageId=ami-123&MinCount=1&MaxCount=5&InstanceType=t2.micro",
			expected: map[string]string{
				"Action":       "RunInstances",
				"ImageId":      "ami-123",
				"MinCount":     "1",
				"MaxCount":     "5",
				"InstanceType": "t2.micro",
			},
		},
		{
			name:  "value containing equals sign",
			query: "Filter.1.Name=tag:Env&Filter.1.Value=prod=staging",
			expected: map[string]string{
				"Filter.1.Name":  "tag:Env",
				"Filter.1.Value": "prod=staging",
			},
		},
		{
			name:  "URL-encoded key and value",
			query: "Tag%2EName=my%20tag",
			expected: map[string]string{
				"Tag.Name": "my tag",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ParseAWSQueryArgs(tc.query)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestParseArgsToStruct(t *testing.T) {
	// ParseArgsToStruct wraps QueryParamsToStruct errors as ErrorInvalidParameter.
	// The *any parameter causes a reflection kind mismatch (Interface vs Struct)
	// in QueryParamsToStruct, so this function always returns the wrapped error.
	// Tests verify the error wrapping behavior.

	type simpleInput struct {
		Action string `locationName:"Action"`
	}

	t.Run("struct pointer wrapped in any returns InvalidParameter", func(t *testing.T) {
		args := map[string]string{"Action": "RunInstances"}
		var input any = &simpleInput{}
		err := ParseArgsToStruct(&input, args)
		assert.Error(t, err)
		assert.Equal(t, "InvalidParameter", err.Error())
	})

	t.Run("non-pointer input returns InvalidParameter", func(t *testing.T) {
		args := map[string]string{"Action": "Test"}
		var input any = "not a struct"
		err := ParseArgsToStruct(&input, args)
		assert.Error(t, err)
		assert.Equal(t, "InvalidParameter", err.Error())
	})

	t.Run("empty args still returns InvalidParameter", func(t *testing.T) {
		args := map[string]string{}
		var input any = &simpleInput{}
		err := ParseArgsToStruct(&input, args)
		assert.Error(t, err)
		assert.Equal(t, "InvalidParameter", err.Error())
	})
}
