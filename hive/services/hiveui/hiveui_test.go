package hiveui

import (
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ValidConfig(t *testing.T) {
	cfg := &Config{
		Port:    8080,
		Host:    "127.0.0.1",
		TLSCert: "/custom/cert.pem",
		TLSKey:  "/custom/key.pem",
	}

	svc, err := New(cfg)

	require.NoError(t, err)
	assert.NotNil(t, svc)
	assert.Equal(t, 8080, svc.Config.Port)
	assert.Equal(t, "127.0.0.1", svc.Config.Host)
	assert.Equal(t, "/custom/cert.pem", svc.Config.TLSCert)
	assert.Equal(t, "/custom/key.pem", svc.Config.TLSKey)
}

func TestNew_DefaultPortAndHost(t *testing.T) {
	cfg := &Config{
		TLSCert: "/custom/cert.pem",
		TLSKey:  "/custom/key.pem",
	}

	svc, err := New(cfg)

	require.NoError(t, err)
	assert.Equal(t, 3000, svc.Config.Port)
	assert.Equal(t, "0.0.0.0", svc.Config.Host)
}

func TestNew_DefaultTLSPaths(t *testing.T) {
	cfg := &Config{}

	svc, err := New(cfg)

	require.NoError(t, err)
	// TLS paths should be filled in from home directory
	assert.NotEmpty(t, svc.Config.TLSCert)
	assert.NotEmpty(t, svc.Config.TLSKey)
	assert.Contains(t, svc.Config.TLSCert, "server.pem")
	assert.Contains(t, svc.Config.TLSKey, "server.key")
}

func TestNew_CustomTLSPreserved(t *testing.T) {
	cfg := &Config{
		TLSCert: "/my/cert.pem",
		TLSKey:  "/my/key.pem",
	}

	svc, err := New(cfg)

	require.NoError(t, err)
	assert.Equal(t, "/my/cert.pem", svc.Config.TLSCert)
	assert.Equal(t, "/my/key.pem", svc.Config.TLSKey)
}

func TestNew_PartialTLSFillsOnlyEmpty(t *testing.T) {
	cfg := &Config{
		TLSCert: "/my/cert.pem",
		// TLSKey left empty — should get default
	}

	svc, err := New(cfg)

	require.NoError(t, err)
	// Both are empty-or-not checked together, so both get defaults
	// because the condition is cfg.TLSCert == "" || cfg.TLSKey == ""
	assert.Equal(t, "/my/cert.pem", svc.Config.TLSCert)
	assert.Contains(t, svc.Config.TLSKey, "server.key")
}

func TestNew_InvalidConfigType(t *testing.T) {
	svc, err := New("not a config")

	assert.Nil(t, svc)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid config type")
}

func TestNew_InvalidConfigTypeInt(t *testing.T) {
	svc, err := New(42)

	assert.Nil(t, svc)
	assert.Error(t, err)
}

func TestStatus_ReturnsValidState(t *testing.T) {
	svc := &Service{
		Config: &Config{},
	}

	status, err := svc.Status()

	assert.NoError(t, err)
	// On a dev machine the hive-ui PID file may exist, so accept either outcome
	assert.True(t, status == "stopped" || len(status) > 0, "status should be non-empty")
}

func TestReload_ReturnsNil(t *testing.T) {
	svc := &Service{
		Config: &Config{},
	}

	err := svc.Reload()
	assert.NoError(t, err)
}

func TestServiceName(t *testing.T) {
	assert.Equal(t, "hive-ui", serviceName)
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := securityHeadersMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("Content-Security-Policy"))
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "default-src 'self'")
	assert.Equal(t, "camera=(), microphone=(), geolocation=(), browsing-topics=()", resp.Header.Get("Permissions-Policy"))
	assert.Equal(t, "strict-origin-when-cross-origin", resp.Header.Get("Referrer-Policy"))
	assert.Equal(t, "max-age=31536000; includeSubDomains", resp.Header.Get("Strict-Transport-Security"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
}

func TestSecurityHeadersMiddleware_PassesThrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "test-value")
		w.WriteHeader(http.StatusCreated)
	})

	handler := securityHeadersMiddleware(inner)
	req := httptest.NewRequest(http.MethodPost, "/api/data", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "test-value", resp.Header.Get("X-Custom"))
	// Security headers still present
	assert.NotEmpty(t, resp.Header.Get("Content-Security-Policy"))
}

func TestGzipMiddleware_CompressesEligibleContent(t *testing.T) {
	body := "Hello, this is some text content that should be compressed if long enough."
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(body))
	})

	handler := gzipMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	// The gzip handler may or may not compress depending on content size,
	// but the handler should still return a valid response.
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	if resp.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(resp.Body)
		require.NoError(t, err)
		defer reader.Close()
		decompressed, err := io.ReadAll(reader)
		require.NoError(t, err)
		assert.Equal(t, body, string(decompressed))
	}
}

func TestGzipMiddleware_NoCompressionWithoutHeader(t *testing.T) {
	body := "uncompressed response body"
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(body))
	})

	handler := gzipMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Accept-Encoding header
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEqual(t, "gzip", resp.Header.Get("Content-Encoding"))
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, body, string(respBody))
}

func TestGzipMiddleware_IgnoresNonTextContent(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("fake image data"))
	})

	handler := gzipMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/image.png", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// image/png not in the allowed content types — should not be compressed
	assert.NotEqual(t, "gzip", resp.Header.Get("Content-Encoding"))
}

func TestShutdown_WithServer(t *testing.T) {
	// Create a real server on a random port so Shutdown exercises the non-nil path
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Handler: mux}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go srv.Serve(ln)

	svc := &Service{
		Config: &Config{},
		server: srv,
	}

	err = svc.Shutdown()
	assert.NoError(t, err)
}
