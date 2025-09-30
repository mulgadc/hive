package gateway

import (
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/mulgadc/predastore/s3"

	"github.com/google/uuid"
)

type GatewayConfig struct {
	Debug          bool `json:"debug"`
	DisableLogging bool `json:"disable_logging"`
}

var supportedServices = map[string]bool{
	"ec2":     true,
	"iam":     true,
	"account": true,
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

	// Determine the service from the Authorization header
	// "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20250930/ap-southeast-2/account/aws4_request, SignedHeaders=content-type;host;x-amz-date, Signature=adb8183545a9d6f5b626908582b4ca5f9a309137b1d4bcc802f0e79ede7af7bc"

	authHeader := ctx.Get("Authorization")

	parts := strings.Split(authHeader, ", ")
	if len(parts) != 3 {
		slog.Debug("Invalid Authorization header format")
		return errors.New("AccessDenied")
	}

	// Parse credential
	creds := strings.Split(strings.TrimPrefix(parts[0], "AWS4-HMAC-SHA256 Credential="), "/")
	if len(creds) != 5 {
		slog.Debug("Invalid credential scope")
		return errors.New("AccessDenied")
	}

	svc := creds[3]

	if !supportedServices[svc] {
		slog.Debug("Unsupported service", "service", svc)
		return errors.New("UnsupportedOperation")
	}

	slog.Info("Request", "service", svc, "method", ctx.Method(), "path", ctx.Path())

	switch svc {
	case "ec2":
		return gw.EC2_Request(ctx)
	case "account":
		return gw.Account_Request(ctx)
	case "iam":
		return gw.IAM_Request(ctx)
	default:
		return errors.New("UnsupportedOperation")
	}

}

func (gw *GatewayConfig) ErrorHandler(ctx *fiber.Ctx, err error) error {
	// TODO: Support service type specific errors (e.g EC2, S3, etc)
	// Status code defaults to 500
	httpCode := fiber.StatusInternalServerError
	var s3error s3.S3Error
	var e *fiber.Error

	// Check for specific error types
	switch {
	case strings.Contains(err.Error(), "NoSuchBucket") || strings.Contains(err.Error(), "Bucket not found"):
		// File or bucket not found
		httpCode = fiber.StatusNotFound
		s3error.Code = "NoSuchBucket"
		s3error.Message = "The specified bucket does not exist"

	case strings.Contains(err.Error(), "AccessDenied") || strings.Contains(err.Error(), "Not enough permissions"):
		// Permission error
		httpCode = fiber.StatusForbidden
		s3error.Code = "AccessDenied"
		s3error.Message = "Access Denied"

	case strings.Contains(err.Error(), "NoSuchObject") || strings.Contains(err.Error(), "not found") ||
		errors.Is(err, os.ErrNotExist):
		// File not found
		httpCode = fiber.StatusNotFound
		s3error.Code = "NoSuchKey"
		s3error.Message = "The specified key does not exist"

	case strings.Contains(err.Error(), "Invalid signature") || strings.Contains(err.Error(), "Invalid access key"):
		// Authentication error
		httpCode = fiber.StatusForbidden
		s3error.Code = "SignatureDoesNotMatch"
		s3error.Message = "The request signature does not match"

	case strings.Contains(err.Error(), "Missing Authorization header"):
		// Missing auth header
		httpCode = fiber.StatusForbidden
		s3error.Code = "AccessDenied"
		s3error.Message = "Access Denied"

	case strings.Contains(err.Error(), "UnsupportedOperation"):
		// Unsupported operation
		httpCode = fiber.StatusBadRequest
		s3error.Code = "UnsupportedOperation"
		s3error.Message = "The operation is not supported"
		ctx.Set("X-Amzn-Errortype", "UnsupportedOperationException")

	case errors.As(err, &e):
		httpCode = e.Code
		s3error.Message = e.Message
		s3error.Code = "InternalError"
	default:
		s3error.Code = "InternalError"
		s3error.Message = err.Error()
	}

	// Add request ID and host ID

	s3error.RequestId = ctx.GetRespHeader("x-amz-request-id", uuid.NewString())
	s3error.HostId = ctx.Hostname()

	// Set standard S3 error response headers
	ctx.Set("Content-Type", "application/xml")

	return ctx.Status(httpCode).XML(s3error)
}

// Parse AWS query arguments (used by some services like EC2/S3)
func parseAWSQueryArgs(query string) map[string]string {
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
