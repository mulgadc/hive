package gateway

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/google/uuid"
	"github.com/mulgadc/hive/hive/awsec2query"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/gateway/policy"
	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
	"github.com/nats-io/nats.go"
)

type GatewayConfig struct {
	Debug          bool       `json:"debug"`
	DisableLogging bool       `json:"disable_logging"`
	NATSConn       *nats.Conn // Shared NATS connection for service communication
	Config         string     // Shared AWS Gateway config for S3 auth
	ExpectedNodes  int        // Number of expected hive nodes for multi-node operations
	Region         string     // Region this gateway is running in
	AZ             string     // Availability zone this gateway is running in
	IAMService     handlers_iam.IAMService
	IAMMasterKey   []byte // loaded from master.key file at startup
}

var supportedServices = map[string]bool{
	"ec2":     true,
	"iam":     true,
	"account": true,
}

type ErrorResponse struct {
	XMLName   xml.Name `xml:"Response"`
	Errors    Errors   `xml:"Errors"`
	RequestID string   `xml:"RequestID"`
}

type Errors struct {
	Error ErrorDetail `xml:"Error"`
}

type ErrorDetail struct {
	Code    string `xml:"Code"`
	Message error  `xml:"Message"`
}

func (gw *GatewayConfig) SetupRoutes() *fiber.App {

	var logLevel slog.Level

	if gw.Debug {
		logLevel = slog.LevelDebug
	} else if gw.DisableLogging {
		logLevel = slog.LevelError
	} else {
		logLevel = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})

	// Create a new logger with the custom handler
	slogger := slog.New(handler)

	// Set it as the default logger
	slog.SetDefault(slogger)

	// Configure slog for logging
	slog.New(slog.NewTextHandler(os.Stdout, nil))

	app := fiber.New(fiber.Config{

		// Disable the startup banner
		DisableStartupMessage: gw.DisableLogging,

		// Set the body limit for S3 specs to 5GiB
		//BodyLimit: 5 * 1024 * 1024 * 1024,

		// Override default error handler
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			return gw.ErrorHandler(ctx, err)
		}},
	)

	if !gw.DisableLogging {
		app.Use(logger.New())
	}

	// Add CORS middleware for browser requests
	app.Use(cors.New(cors.Config{
		AllowOrigins:     "https://localhost:3000",
		AllowMethods:     "GET,POST,PUT,DELETE,HEAD,OPTIONS",
		AllowHeaders:     "*",
		AllowCredentials: true,
	}))

	// Add AWS SigV4 authentication middleware
	app.Use(gw.SigV4AuthMiddleware())
	// Define routes
	app.Get("/*", func(c *fiber.Ctx) error {

		return gw.Request(c)
	})

	app.Post("/*", func(c *fiber.Ctx) error {

		return gw.Request(c)
	})

	return app
}

// Note, custom endpoints can be configured via ENV vars to the AWS SDK/CLI tool, with individual endpoints depending the service
// AWS_ENDPOINT_URL_EC2=https://localhost:9999/ aws  --no-verify-ssl ec2 describe-instances
// aws --endpoint-url https://localhost:9999/  --no-verify-ssl eks list-clusters
// AWS_ENDPOINT_URL=https://localhost:9999/ aws  --no-verify-ssl ec2 describe-instances

func (gw *GatewayConfig) Request(ctx *fiber.Ctx) error {

	// Route the request to the appropriate endpoint (e.g EC2, IAM, etc)
	svc, err := gw.GetService(ctx)
	slog.Info("Request", "service", svc, "method", ctx.Method(), "path", ctx.Path())

	if err != nil {
		slog.Error("GetService error", "error", err)
		return gw.ErrorHandler(ctx, err)
	}

	switch svc {
	case "ec2":
		err = gw.EC2_Request(ctx)
	case "account":
		err = gw.Account_Request(ctx)
	case "iam":
		err = gw.IAM_Request(ctx)
	default:
		err = errors.New(awserrors.ErrorUnsupportedOperation)
	}

	if err != nil {
		slog.Error("Service request error", "service", svc, "error", err)
		return gw.ErrorHandler(ctx, err)
	} else {
		slog.Info("Service request completed", "service", svc)
	}

	return nil

}

func (gw *GatewayConfig) GetService(ctx *fiber.Ctx) (string, error) {
	svc, ok := ctx.Locals("sigv4.service").(string)
	if !ok {
		return "", errors.New(awserrors.ErrorAuthFailure)
	}
	if !supportedServices[svc] {
		slog.Debug("Unsupported service", "service", svc)
		return "", errors.New(awserrors.ErrorUnsupportedOperation)
	}
	return svc, nil
}

// checkPolicy evaluates IAM policies for the current request. Returns nil
// if access is allowed, or an ErrorAccessDenied error if denied.
// Root users bypass evaluation entirely. If the IAM service is unavailable,
// access is allowed (pre-IAM compatibility).
func (gw *GatewayConfig) checkPolicy(ctx *fiber.Ctx, service, action string) error {
	if gw.IAMService == nil {
		slog.Warn("checkPolicy: IAM service not available, skipping policy check",
			"service", service, "action", action)
		return nil
	}

	identityVal := ctx.Locals("sigv4.identity")
	if identityVal == nil {
		// No auth context — pre-IAM compatibility
		return nil
	}
	identity, ok := identityVal.(string)
	if !ok {
		slog.Error("checkPolicy: identity has unexpected type", "type", fmt.Sprintf("%T", identityVal))
		return errors.New(awserrors.ErrorInternalError)
	}
	if identity == "" || identity == "root" {
		return nil
	}

	// Resolve the IAM action string (e.g. "ec2:RunInstances")
	iamAction, ok := policy.LookupAction(service, action)
	if !ok {
		// Action not in mapping table — construct it directly so wildcard
		// policies (e.g. "ec2:*") still match.
		iamAction = policy.IAMAction(service, action)
	}

	policies, err := gw.IAMService.GetUserPolicies(identity)
	if err != nil {
		slog.Error("checkPolicy: failed to get user policies", "user", identity, "err", err)
		return errors.New(awserrors.ErrorInternalError)
	}

	if policy.EvaluateAccess(identity, iamAction, "*", policies) == policy.Deny {
		slog.Info("checkPolicy: access denied", "user", identity, "action", iamAction)
		return errors.New(awserrors.ErrorAccessDenied)
	}

	return nil
}

func (gw *GatewayConfig) ErrorHandler(ctx *fiber.Ctx, err error) error {
	svc, _ := gw.GetService(ctx)
	slog.Debug("ErrorHandler", "service", svc, "error", err.Error())

	// Get the request ID
	var requestId = uuid.NewString()
	requestId = ctx.Get("x-amz-request-id", requestId)

	var errorMsg = awserrors.ErrorMessage{}

	// Check if the error lookup exists
	if _, exists := awserrors.ErrorLookup[err.Error()]; !exists {
		slog.Warn("Unknown error code", "error", err.Error())
		err = errors.New(awserrors.ErrorInternalError)
	}

	errorMsg = awserrors.ErrorLookup[err.Error()]

	// IAM uses a different error XML format than EC2
	var xmlError []byte
	if svc == "iam" {
		xmlError = GenerateIAMErrorResponse(err.Error(), errorMsg.Message, requestId)
	} else {
		xmlError = GenerateEC2ErrorResponse(err.Error(), errorMsg.Message, requestId)
	}

	slog.Debug("Generated error response", "error", err.Error(), "xml", string(xmlError), "requestId", requestId)

	if errorMsg.HTTPCode == 0 {
		errorMsg.HTTPCode = 500
	}

	ctx.Set("Content-Type", "application/xml")
	return ctx.Status(errorMsg.HTTPCode).Send(xmlError)
}

// Parse AWS query arguments (used by some services like EC2/S3)
// Properly URL-decodes both keys and values
func ParseAWSQueryArgs(query string) map[string]string {
	params := make(map[string]string)
	pairs := strings.SplitSeq(query, "&")
	for pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			key, _ := url.QueryUnescape(kv[0])
			value, _ := url.QueryUnescape(kv[1])
			params[key] = value
		} else if len(kv) == 1 {
			key, _ := url.QueryUnescape(kv[0])
			params[key] = ""
		}
	}
	return params
}

func GenerateEC2ErrorResponse(code, message, requestID string) (output []byte) {

	errorXml := ErrorResponse{
		Errors: Errors{
			Error: ErrorDetail{
				Code:    code,
				Message: errors.New(message),
			},
		},
		RequestID: requestID,
	}

	output, err := xml.MarshalIndent(errorXml, "", "  ")

	if err != nil {
		slog.Error("Failed to build XML", "error", err)
		return []byte(xml.Header + "<Response><Errors><Error><Code>InternalError</Code><Message>Internal error</Message></Error></Errors><RequestID>" + requestID + "</RequestID></Response>")
	}

	// Add XML header
	output = append([]byte(xml.Header), output...)

	return output
}

// IAMErrorResponse represents the IAM-style error XML format:
// <ErrorResponse><Error><Type>Sender</Type><Code>...</Code><Message>...</Message></Error><RequestId>...</RequestId></ErrorResponse>
type IAMErrorResponse struct {
	XMLName   xml.Name       `xml:"ErrorResponse"`
	Error     IAMErrorDetail `xml:"Error"`
	RequestID string         `xml:"RequestId"`
}

type IAMErrorDetail struct {
	Type    string `xml:"Type"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func GenerateIAMErrorResponse(code, message, requestID string) (output []byte) {
	errorXml := IAMErrorResponse{
		Error: IAMErrorDetail{
			Type:    "Sender",
			Code:    code,
			Message: message,
		},
		RequestID: requestID,
	}

	output, err := xml.MarshalIndent(errorXml, "", "  ")
	if err != nil {
		slog.Error("Failed to build IAM error XML", "error", err)
		return []byte(xml.Header + "<ErrorResponse><Error><Type>Sender</Type><Code>InternalError</Code><Message>Internal error</Message></Error><RequestId>" + requestID + "</RequestId></ErrorResponse>")
	}

	output = append([]byte(xml.Header), output...)
	return output
}

func ParseArgsToStruct(input *any, args map[string]string) (err error) {

	// Generated from input shape: RunInstancesRequest
	err = awsec2query.QueryParamsToStruct(args, input)

	if err != nil {
		return errors.New(awserrors.ErrorInvalidParameter)
	}

	return nil

}

// NodeDiscoverResponse is the response from a node discovery request
type NodeDiscoverResponse struct {
	Node string `json:"node"`
}

// DiscoverActiveNodes discovers the number of active hive daemon nodes in the cluster
// by publishing a discovery request and counting unique responses.
// Returns the number of active nodes (minimum 1 if fallback is needed).
func (gw *GatewayConfig) DiscoverActiveNodes() int {
	if gw.NATSConn == nil {
		slog.Warn("DiscoverActiveNodes: NATS connection not available, using ExpectedNodes fallback", "fallback", gw.ExpectedNodes)
		return gw.ExpectedNodes
	}

	// Create an inbox for collecting responses from all nodes
	inbox := nats.NewInbox()
	sub, err := gw.NATSConn.SubscribeSync(inbox)
	if err != nil {
		slog.Error("DiscoverActiveNodes: Failed to create inbox subscription", "err", err)
		return gw.ExpectedNodes
	}
	defer sub.Unsubscribe()

	// Publish discovery request to all nodes
	err = gw.NATSConn.PublishRequest("hive.nodes.discover", inbox, []byte("{}"))
	if err != nil {
		slog.Error("DiscoverActiveNodes: Failed to publish request", "err", err)
		return gw.ExpectedNodes
	}

	// Collect responses with a short timeout
	// We use a short timeout since discovery should be fast
	timeout := 500 * time.Millisecond
	deadline := time.Now().Add(timeout)

	nodesSeen := make(map[string]bool)

	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		msg, err := sub.NextMsg(remaining)
		if err != nil {
			if err == nats.ErrTimeout {
				break
			}
			slog.Debug("DiscoverActiveNodes: Error receiving message", "err", err)
			break
		}

		var response NodeDiscoverResponse
		if err := json.Unmarshal(msg.Data, &response); err != nil {
			slog.Debug("DiscoverActiveNodes: Failed to unmarshal response", "err", err)
			continue
		}

		nodesSeen[response.Node] = true
	}

	activeNodes := len(nodesSeen)
	if activeNodes == 0 {
		// Fallback to configured value if no responses
		slog.Warn("DiscoverActiveNodes: No nodes responded, using ExpectedNodes fallback", "fallback", gw.ExpectedNodes)
		return gw.ExpectedNodes
	}

	slog.Debug("DiscoverActiveNodes: Discovered active nodes", "count", activeNodes)
	return activeNodes
}
