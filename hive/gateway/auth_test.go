package gateway

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/mulgadc/predastore/auth"
)

const (
	testAccessKey = "AKIAIOSFODNN7EXAMPLE"
	testSecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	testRegion    = "us-east-1"
	testService   = "ec2"
)

// generateTestAuthHeader creates a valid AWS SigV4 Authorization header for testing.
func generateTestAuthHeader(method, path, queryString, body, accessKey, secretKey, region, service string) (authHeader, timestamp string) {
	now := time.Now().UTC()
	timestamp = now.Format(auth.TimeFormat)
	date := now.Format(auth.ShortTimeFormat)

	// Build canonical URI
	canonicalURI := auth.UriEncode(path, false)
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	// Build canonical query string
	canonicalQueryString := buildCanonicalQueryString(queryString)

	// Build canonical headers
	host := "localhost:9999"
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-date:%s\n", host, timestamp)
	signedHeaders := "host;x-amz-date"

	// Hash payload
	payloadHash := auth.HashSHA256(body)

	// Build canonical request
	canonicalRequest := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s\n%s",
		method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
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

	// Build Authorization header
	authHeader = fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s/%s/%s/aws4_request, SignedHeaders=%s, Signature=%s",
		accessKey,
		date,
		region,
		service,
		signedHeaders,
		signature,
	)

	return authHeader, timestamp
}

func setupTestApp(accessKey, secretKey string) *fiber.App {
	gw := &GatewayConfig{
		DisableLogging: true,
		AccessKey:      accessKey,
		SecretKey:      secretKey,
		Region:         testRegion,
	}

	app := fiber.New()
	app.Use(gw.SigV4AuthMiddleware())

	// Simple test handler
	app.All("/*", func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	return app
}

func TestSigV4Auth_NoAuthorizationHeader(t *testing.T) {
	app := setupTestApp(testAccessKey, testSecretKey)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "localhost:9999"

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != 403 {
		t.Errorf("Expected status 403, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "MissingAuthenticationToken") {
		t.Errorf("Expected MissingAuthenticationToken error, got: %s", string(body))
	}
}

func TestSigV4Auth_MalformedHeader(t *testing.T) {
	app := setupTestApp(testAccessKey, testSecretKey)

	testCases := []struct {
		name       string
		authHeader string
	}{
		{"empty prefix", "InvalidPrefix Credential=test"},
		{"missing parts", "AWS4-HMAC-SHA256 Credential=test"},
		{"invalid credential format", "AWS4-HMAC-SHA256 Credential=a/b/c, SignedHeaders=host, Signature=sig"},
		{"missing SignedHeaders prefix", "AWS4-HMAC-SHA256 Credential=a/b/c/d/aws4_request, Headers=host, Signature=sig"},
		{"missing Signature prefix", "AWS4-HMAC-SHA256 Credential=a/b/c/d/aws4_request, SignedHeaders=host, Sig=abc"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = "localhost:9999"
			req.Header.Set("Authorization", tc.authHeader)
			req.Header.Set("X-Amz-Date", time.Now().UTC().Format(auth.TimeFormat))

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("Failed to test request: %v", err)
			}

			if resp.StatusCode != 400 {
				t.Errorf("Expected status 400, got %d", resp.StatusCode)
			}

			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), "IncompleteSignature") {
				t.Errorf("Expected IncompleteSignature error, got: %s", string(body))
			}
		})
	}
}

func TestSigV4Auth_InvalidAccessKey(t *testing.T) {
	app := setupTestApp(testAccessKey, testSecretKey)

	authHeader, timestamp := generateTestAuthHeader(
		"GET", "/", "", "",
		"INVALID_ACCESS_KEY", testSecretKey, testRegion, testService,
	)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "localhost:9999"
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Amz-Date", timestamp)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != 403 {
		t.Errorf("Expected status 403, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "InvalidClientTokenId") {
		t.Errorf("Expected InvalidClientTokenId error, got: %s", string(body))
	}
}

func TestSigV4Auth_InvalidSignature(t *testing.T) {
	app := setupTestApp(testAccessKey, testSecretKey)

	// Generate auth header with wrong secret key
	authHeader, timestamp := generateTestAuthHeader(
		"GET", "/", "", "",
		testAccessKey, "WRONG_SECRET_KEY", testRegion, testService,
	)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "localhost:9999"
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Amz-Date", timestamp)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != 403 {
		t.Errorf("Expected status 403, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "SignatureDoesNotMatch") {
		t.Errorf("Expected SignatureDoesNotMatch error, got: %s", string(body))
	}
}

func TestSigV4Auth_ValidSignature(t *testing.T) {
	app := setupTestApp(testAccessKey, testSecretKey)

	authHeader, timestamp := generateTestAuthHeader(
		"GET", "/", "", "",
		testAccessKey, testSecretKey, testRegion, testService,
	)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "localhost:9999"
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Amz-Date", timestamp)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("Expected status 200, got %d, body: %s", resp.StatusCode, string(body))
	}
}

func TestSigV4Auth_OptionsSkipsAuth(t *testing.T) {
	app := setupTestApp(testAccessKey, testSecretKey)

	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Host = "localhost:9999"
	// No Authorization header

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200 for OPTIONS, got %d", resp.StatusCode)
	}
}

func TestSigV4Auth_ValidSignatureWithBody(t *testing.T) {
	app := setupTestApp(testAccessKey, testSecretKey)

	body := "Action=DescribeInstances&Version=2016-11-15"
	authHeader, timestamp := generateTestAuthHeader(
		"POST", "/", "", body,
		testAccessKey, testSecretKey, testRegion, testService,
	)

	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Host = "localhost:9999"
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Amz-Date", timestamp)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Errorf("Expected status 200, got %d, body: %s", resp.StatusCode, string(respBody))
	}
}

func TestSigV4Auth_ValidSignatureWithQueryString(t *testing.T) {
	app := setupTestApp(testAccessKey, testSecretKey)

	queryString := "Action=DescribeInstances&Version=2016-11-15"
	authHeader, timestamp := generateTestAuthHeader(
		"GET", "/", queryString, "",
		testAccessKey, testSecretKey, testRegion, testService,
	)

	req := httptest.NewRequest("GET", "/?"+queryString, nil)
	req.Host = "localhost:9999"
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Amz-Date", timestamp)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Errorf("Expected status 200, got %d, body: %s", resp.StatusCode, string(respBody))
	}
}

func TestParseAWSQueryArgs_URLDecoding(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		key      string
		expected string
	}{
		{"plain value", "Action=DescribeInstances", "Action", "DescribeInstances"},
		{"encoded slashes", "Device=%2Fdev%2Fsdf", "Device", "/dev/sdf"},
		{"encoded spaces", "Name=my%20volume", "Name", "my volume"},
		{"encoded plus as space", "Name=my+volume", "Name", "my volume"},
		{"no encoding needed", "VolumeId=vol-abc123", "VolumeId", "vol-abc123"},
		{"multiple params", "VolumeId=vol-abc&Device=%2Fdev%2Fsdg", "Device", "/dev/sdg"},
		{"encoded key dot", "Tag%2EKey=Name", "Tag.Key", "Name"},
		{"encoded key and value", "Filter%2E1%2EName=instance-id&Filter%2E1%2EValue=i-abc", "Filter.1.Name", "instance-id"},
		{"key-only encoded", "Tag%2EKey=", "Tag.Key", ""},
		{"key without value", "Tag%2EKey", "Tag.Key", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ParseAWSQueryArgs(tc.input)
			if result[tc.key] != tc.expected {
				t.Errorf("Expected %q for key %q, got %q", tc.expected, tc.key, result[tc.key])
			}
		})
	}
}

func TestBuildCanonicalQueryString(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"single param", "Action=Test", "Action=Test"},
		{"multiple params sorted", "Version=1&Action=Test", "Action=Test&Version=1"},
		{"encoded values", "Name=Hello World", "Name=Hello%20World"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := buildCanonicalQueryString(tc.input)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestCanonicalHeaderName(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"host", "Host"},
		{"x-amz-date", "X-Amz-Date"},
		{"content-type", "Content-Type"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := canonicalHeaderName(tc.input)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}
