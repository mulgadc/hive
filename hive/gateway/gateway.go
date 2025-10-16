package gateway

import (
	"encoding/xml"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/google/uuid"
	"github.com/mulgadc/hive/hive/awsec2query"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/predastore/s3"
	"github.com/nats-io/nats.go"
)

type GatewayConfig struct {
	Debug          bool        `json:"debug"`
	DisableLogging bool        `json:"disable_logging"`
	NATSConn       *nats.Conn  // Shared NATS connection for service communication
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

	s3 := s3.New(&s3.Config{})

	// TODO: Support env var for config path, and external IAM
	s3.ConfigPath = "config/awsgw/awsgw.toml"

	err := s3.ReadConfig()

	if err != nil {
		slog.Error("Error reading config", "error", err)
		return nil
	}

	// Add authentication middleware for all requests
	app.Use(s3.SigV4AuthMiddleware)

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

func (gw *GatewayConfig) GetService(ctx *fiber.Ctx) (srv string, err error) {

	// Determine the service from the Authorization header
	// "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20250930/ap-southeast-2/account/aws4_request, SignedHeaders=content-type;host;x-amz-date, Signature=adb8183545a9d6f5b626908582b4ca5f9a309137b1d4bcc802f0e79ede7af7bc"

	authHeader := ctx.Get("Authorization")

	parts := strings.Split(authHeader, ", ")
	if len(parts) != 3 {
		slog.Debug("Invalid Authorization header format")
		return srv, errors.New(awserrors.ErrorAuthFailure)
	}

	// Parse credential
	creds := strings.Split(strings.TrimPrefix(parts[0], "AWS4-HMAC-SHA256 Credential="), "/")
	if len(creds) != 5 {
		slog.Debug("Invalid credential scope")
		return srv, errors.New(awserrors.ErrorAuthFailure)
	}

	svc := creds[3]

	if !supportedServices[svc] {
		slog.Debug("Unsupported service", "service", svc)
		return srv, errors.New("UnsupportedOperation")
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
func ParseAWSQueryArgs(query string) map[string]string {
	params := make(map[string]string)
	pairs := strings.Split(query, "&")
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			params[kv[0]] = kv[1]
		} else if len(kv) == 1 {
			params[kv[0]] = ""
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

func ParseArgsToStruct(input *interface{}, args map[string]string) (err error) {

	// Generated from input shape: RunInstancesRequest
	err = awsec2query.QueryParamsToStruct(args, input)

	if err != nil {
		return errors.New("InvalidParameter")
	}

	return nil

}
