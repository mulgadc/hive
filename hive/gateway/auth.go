package gateway

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
	"github.com/mulgadc/predastore/auth"
)

const (
	// Maximum allowed clock skew for signature validation (5 minutes)
	maxClockSkew = 5 * time.Minute
)

// SigV4AuthMiddleware returns a Fiber middleware that validates AWS Signature V4 authentication.
// It verifies the Authorization header against the configured credentials.
func (gw *GatewayConfig) SigV4AuthMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Skip OPTIONS requests (CORS preflight)
		if c.Method() == "OPTIONS" {
			return c.Next()
		}

		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return gw.writeSigV4Error(c, awserrors.ErrorMissingAuthenticationToken)
		}

		// Parse the Authorization header
		// Format: AWS4-HMAC-SHA256 Credential=ACCESS_KEY/DATE/REGION/SERVICE/aws4_request, SignedHeaders=..., Signature=...
		if !strings.HasPrefix(authHeader, "AWS4-HMAC-SHA256 ") {
			return gw.writeSigV4Error(c, awserrors.ErrorIncompleteSignature)
		}

		// Extract components from the Authorization header
		parts := strings.Split(authHeader[len("AWS4-HMAC-SHA256 "):], ", ")
		if len(parts) != 3 {
			return gw.writeSigV4Error(c, awserrors.ErrorIncompleteSignature)
		}

		// Parse Credential
		var accessKey, date, region, service string
		if after, ok := strings.CutPrefix(parts[0], "Credential="); ok {
			credParts := strings.Split(after, "/")
			if len(credParts) != 5 || credParts[4] != "aws4_request" {
				return gw.writeSigV4Error(c, awserrors.ErrorIncompleteSignature)
			}
			accessKey = credParts[0]
			date = credParts[1]
			region = credParts[2]
			service = credParts[3]
		} else {
			return gw.writeSigV4Error(c, awserrors.ErrorIncompleteSignature)
		}

		// Parse SignedHeaders
		var signedHeaders string
		if after, ok := strings.CutPrefix(parts[1], "SignedHeaders="); ok {
			signedHeaders = after
		} else {
			return gw.writeSigV4Error(c, awserrors.ErrorIncompleteSignature)
		}

		// Parse Signature
		var providedSignature string
		if after, ok := strings.CutPrefix(parts[2], "Signature="); ok {
			providedSignature = after
		} else {
			return gw.writeSigV4Error(c, awserrors.ErrorIncompleteSignature)
		}

		// Lookup access key in IAM KV store
		if gw.IAMService == nil {
			return gw.writeSigV4Error(c, awserrors.ErrorInternalError)
		}

		ak, err := gw.IAMService.LookupAccessKey(accessKey)
		if err != nil {
			slog.Debug("Access key not found", "accessKeyID", accessKey)
			return gw.writeSigV4Error(c, awserrors.ErrorInvalidClientTokenId)
		}
		if ak.Status != "Active" {
			slog.Debug("Access key inactive", "accessKeyID", accessKey)
			return gw.writeSigV4Error(c, awserrors.ErrorInvalidClientTokenId)
		}

		secret, err := handlers_iam.DecryptSecret(ak.SecretAccessKey, gw.IAMMasterKey)
		if err != nil {
			slog.Error("Failed to decrypt IAM secret", "accessKeyID", accessKey, "err", err)
			return gw.writeSigV4Error(c, awserrors.ErrorInternalError)
		}

		// Get timestamp from X-Amz-Date header
		timestamp := c.Get("X-Amz-Date")
		if timestamp == "" {
			return gw.writeSigV4Error(c, awserrors.ErrorIncompleteSignature)
		}

		// Validate timestamp is within acceptable bounds to prevent replay attacks
		parsedTime, err := time.Parse("20060102T150405Z", timestamp)
		if err != nil {
			slog.Debug("Invalid timestamp format", "timestamp", timestamp)
			return gw.writeSigV4Error(c, awserrors.ErrorIncompleteSignature)
		}
		if time.Since(parsedTime).Abs() > maxClockSkew {
			slog.Debug("Signature expired", "timestamp", timestamp, "skew", time.Since(parsedTime))
			return gw.writeSigV4Error(c, awserrors.ErrorSignatureDoesNotMatch)
		}

		// Compute expected signature using decrypted secret
		expectedSignature := computeSignatureWithSecret(c, secret, date, timestamp, region, service, signedHeaders)

		// Compare signatures using constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(expectedSignature), []byte(providedSignature)) != 1 {
			slog.Debug("Signature mismatch",
				"expected", expectedSignature,
				"provided", providedSignature,
			)
			return gw.writeSigV4Error(c, awserrors.ErrorSignatureDoesNotMatch)
		}

		// Store parsed auth data in context for downstream handlers
		c.Locals("sigv4.identity", ak.UserName)
		c.Locals("sigv4.service", service)
		c.Locals("sigv4.region", region)
		c.Locals("sigv4.accessKey", accessKey)

		slog.Debug("SigV4 authentication successful", "accessKey", accessKey, "identity", ak.UserName)
		return c.Next()
	}
}

// computeSignatureWithSecret builds the canonical request and computes the AWS Signature V4 signature
// using the provided secret key.
func computeSignatureWithSecret(c *fiber.Ctx, secretKey, date, timestamp, region, service, signedHeaders string) string {
	// Build canonical URI (URI-encoded path)
	path := c.Path()
	if path == "" {
		path = "/"
	}
	canonicalURI := auth.UriEncode(path, false)
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	// Build canonical query string (sorted, encoded)
	canonicalQueryString := buildCanonicalQueryString(string(c.Request().URI().QueryString()))

	// Build canonical headers from SignedHeaders list
	headersList := strings.Split(signedHeaders, ";")
	sort.Strings(headersList)

	var canonicalHeaders strings.Builder
	for _, header := range headersList {
		header = strings.ToLower(strings.TrimSpace(header))
		var value string
		if header == "host" {
			value = string(c.Request().Host())
		} else {
			value = c.Get(canonicalHeaderName(header))
		}
		// Trim leading/trailing whitespace and collapse multiple spaces
		value = strings.TrimSpace(value)
		canonicalHeaders.WriteString(header)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(value)
		canonicalHeaders.WriteString("\n")
	}

	// Hash payload body with SHA256
	payloadHash := auth.HashSHA256(string(c.Body()))

	// Build canonical request
	canonicalRequest := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s\n%s",
		c.Method(),
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	)

	hashedCanonicalRequest := auth.HashSHA256(canonicalRequest)

	// Build string-to-sign
	scope := fmt.Sprintf("%s/%s/%s/aws4_request", date, region, service)
	stringToSign := fmt.Sprintf(
		"AWS4-HMAC-SHA256\n%s\n%s\n%s",
		timestamp,
		scope,
		hashedCanonicalRequest,
	)

	// Derive signing key and compute signature
	signingKey := auth.GetSigningKey(secretKey, date, region, service)
	signature := auth.HmacSHA256Hex(signingKey, stringToSign)

	return signature
}

// buildCanonicalQueryString creates the canonical query string according to AWS specs.
func buildCanonicalQueryString(queryString string) string {
	if queryString == "" {
		return ""
	}

	// Parse query parameters
	params := make(map[string][]string)
	pairs := strings.SplitSeq(queryString, "&")
	for pair := range pairs {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		key := kv[0]
		value := ""
		if len(kv) == 2 {
			value = kv[1]
		}
		params[key] = append(params[key], value)
	}

	// Sort keys
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build canonical query string
	var result []string
	for _, key := range keys {
		values := params[key]
		sort.Strings(values)
		encodedKey := auth.UriEncode(key, true)
		for _, v := range values {
			encodedValue := auth.UriEncode(v, true)
			result = append(result, fmt.Sprintf("%s=%s", encodedKey, encodedValue))
		}
	}

	return strings.Join(result, "&")
}

// canonicalHeaderName converts a lowercase header name to the canonical form for lookup.
func canonicalHeaderName(header string) string {
	// Convert header names like "x-amz-date" to "X-Amz-Date" for Fiber's Get method
	parts := strings.Split(header, "-")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "-")
}

// writeSigV4Error writes an EC2-compatible XML error response for authentication failures.
func (gw *GatewayConfig) writeSigV4Error(c *fiber.Ctx, errorCode string) error {
	requestID := c.Get("x-amz-request-id", uuid.NewString())

	errorMsg, exists := awserrors.ErrorLookup[errorCode]
	if !exists {
		errorMsg = awserrors.ErrorMessage{HTTPCode: 500, Message: "Internal error"}
	}

	xmlError := GenerateEC2ErrorResponse(errorCode, errorMsg.Message, requestID)

	c.Set("Content-Type", "application/xml")
	return c.Status(errorMsg.HTTPCode).Send(xmlError)
}
