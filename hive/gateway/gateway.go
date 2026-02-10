package gateway

import (
	"encoding/json"
	"encoding/xml"
	"errors"
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
	AccessKey      string     // AWS Access Key ID for authentication
	SecretKey      string     // AWS Secret Access Key for authentication
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
		err = errors.New("UnsupportedOperation")
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
		return "", errors.New("UnsupportedOperation")
	}
	return svc, nil
}

func (gw *GatewayConfig) ErrorHandler(ctx *fiber.Ctx, err error) error {
	// TODO: Support service type specific errors (e.g EC2, S3, IAM, differ)

	svc, _ := gw.GetService(ctx)
	slog.Debug("ErrorHandler", "service", svc, "error", err.Error())

	// Status code defaults to 500

	// Get the request ID
	var requestId = uuid.NewString()
	requestId = ctx.Get("x-amz-request-id", requestId)

	var errorMsg = awserrors.ErrorMessage{}

	// Check if the error lookup exists
	if _, exists := awserrors.ErrorLookup[err.Error()]; !exists {
		slog.Warn("Unknown error code", "error", err.Error())
		err = errors.New("InternalError")
	}

	errorMsg = awserrors.ErrorLookup[err.Error()]

	xmlError := GenerateEC2ErrorResponse(err.Error(), errorMsg.Message, requestId)

	slog.Debug("Generated error response", "error", err.Error(), "xml", string(xmlError), "requestId", requestId)

	if errorMsg.HTTPCode == 0 {
		errorMsg.HTTPCode = 500
	}

	// Set standard S3 error response headers
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
		return nil
	}

	// Add XML header
	output = append([]byte(xml.Header), output...)

	return output
}

func ParseArgsToStruct(input *any, args map[string]string) (err error) {

	// Generated from input shape: RunInstancesRequest
	err = awsec2query.QueryParamsToStruct(args, input)

	if err != nil {
		return errors.New("InvalidParameter")
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
