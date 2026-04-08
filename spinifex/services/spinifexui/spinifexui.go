package spinifexui

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mulgadc/spinifex/spinifex/utils"
)

var serviceName = "spinifex-ui"

//go:embed all:frontend/dist
var distFS embed.FS

// Config holds the configuration for the spinifex-ui service
type Config struct {
	Port    int    `json:"port"`
	Host    string `json:"host"`
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`
}

// Service represents the spinifex-ui service
type Service struct {
	Config *Config
	server *http.Server
	mu     sync.Mutex
}

// New creates a new spinifex-ui service
func New(config any) (*Service, error) {
	cfg, ok := config.(*Config)
	if !ok {
		return nil, fmt.Errorf("invalid config type for spinifex-ui service")
	}

	// Set defaults
	if cfg.Port == 0 {
		cfg.Port = 3000
	}
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}

	// Default TLS paths: production layout (/etc/spinifex/) if it exists,
	// otherwise fall back to dev layout (~/spinifex/config/).
	if cfg.TLSCert == "" || cfg.TLSKey == "" {
		if info, err := os.Stat("/etc/spinifex"); err == nil && info.IsDir() {
			if cfg.TLSCert == "" {
				cfg.TLSCert = "/etc/spinifex/server.pem"
			}
			if cfg.TLSKey == "" {
				cfg.TLSKey = "/etc/spinifex/server.key"
			}
		} else if homeDir, err := os.UserHomeDir(); err == nil {
			if cfg.TLSCert == "" {
				cfg.TLSCert = filepath.Join(homeDir, "spinifex", "config", "server.pem")
			}
			if cfg.TLSKey == "" {
				cfg.TLSKey = filepath.Join(homeDir, "spinifex", "config", "server.key")
			}
		}
	}

	return &Service{
		Config: cfg,
	}, nil
}

// Start starts the spinifex-ui service
func (svc *Service) Start() (int, error) {
	if err := utils.WritePidFile(serviceName, os.Getpid()); err != nil {
		slog.Error("Failed to write pid file", "err", err)
	}

	err := svc.launchService()
	if err != nil {
		return 0, err
	}

	return os.Getpid(), nil
}

// Stop stops the spinifex-ui service
func (svc *Service) Stop() error {
	return utils.StopProcess(serviceName)
}

// Status returns the status of the spinifex-ui service
func (svc *Service) Status() (string, error) {
	return utils.ServiceStatus("", serviceName)
}

// Shutdown gracefully shuts down the spinifex-ui service
func (svc *Service) Shutdown() error {
	svc.mu.Lock()
	server := svc.server
	svc.mu.Unlock()

	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
	}
	return svc.Stop()
}

// Reload reloads the spinifex-ui service configuration
func (svc *Service) Reload() error {
	return nil
}

// launchService starts the HTTP server
func (svc *Service) launchService() error {
	// Strip the "frontend/dist" prefix from embedded filesystem
	contentFS, err := fs.Sub(distFS, "frontend/dist")
	if err != nil {
		slog.Error("Failed to create sub filesystem", "error", err)
		return fmt.Errorf("failed to get embedded filesystem: %w", err)
	}

	// Check if certificates exist
	if _, err := os.Stat(svc.Config.TLSCert); os.IsNotExist(err) {
		slog.Error("Certificate file not found", "path", svc.Config.TLSCert)
		return fmt.Errorf("certificate file not found: %s", svc.Config.TLSCert)
	}
	if _, err := os.Stat(svc.Config.TLSKey); os.IsNotExist(err) {
		slog.Error("Key file not found", "path", svc.Config.TLSKey)
		return fmt.Errorf("key file not found: %s", svc.Config.TLSKey)
	}

	// Derive CA cert path from server cert directory.
	caCertPath := filepath.Join(filepath.Dir(svc.Config.TLSCert), "ca.pem")

	// Build TLS transport for reverse proxies using the same CA the UI trusts.
	proxyTransport, err := newProxyTransport(caCertPath)
	if err != nil {
		return fmt.Errorf("proxy transport: %w", err)
	}

	// Serve static files from embedded filesystem
	fileServer := http.FileServer(http.FS(contentFS))

	// SPA handler: try to serve the file, fallback to index.html
	spaHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Check if the requested path is a file that exists in embedded FS
		file, err := contentFS.Open(path)
		if err == nil {
			_ = file.Close()
			// Use no-cache to force revalidation; http.FileServer sets ETags
			// so browsers will get 304 Not Modified when files haven't changed
			w.Header().Set("Cache-Control", "no-cache")
			fileServer.ServeHTTP(w, r)
			return
		}

		// File doesn't exist, serve index.html for SPA routing
		w.Header().Set("Cache-Control", "no-cache")
		indexContent, err := fs.ReadFile(contentFS, "index.html")
		if err != nil {
			http.Error(w, "index.html not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write(indexContent); err != nil {
			slog.Error("Failed to write index.html response", "error", err)
		}
	})

	mux := http.NewServeMux()

	// Reverse proxy routes — must be registered before the SPA catch-all.
	mux.Handle("/proxy/awsgw/", newReverseProxy("localhost:9999", "/proxy/awsgw", proxyTransport))
	mux.Handle("/proxy/s3/", newReverseProxy("localhost:8443", "/proxy/s3", proxyTransport))

	// CA certificate download.
	mux.HandleFunc("/api/ca.pem", func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(caCertPath); os.IsNotExist(err) {
			slog.Warn("CA certificate requested but not found", "path", caCertPath)
			http.Error(w, "CA certificate not yet generated. Run 'spx admin init' to create it.", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="spinifex-ca.pem"`)
		http.ServeFile(w, r, caCertPath)
	})

	// SPA catch-all.
	mux.Handle("/", spaHandler)

	// Wrap handler with security headers and gzip compression
	finalHandler := securityHeadersMiddleware(gzipMiddleware(mux))

	addr := fmt.Sprintf("%s:%d", svc.Config.Host, svc.Config.Port)

	server := &http.Server{
		Addr:              addr,
		Handler:           finalHandler,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	svc.mu.Lock()
	svc.server = server
	svc.mu.Unlock()

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		slog.Info("Received shutdown signal, gracefully shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			slog.Error("Failed to shutdown server gracefully", "err", err)
		}
	}()

	// Listen on the port and detect TLS vs plain HTTP on the same port.
	// Plain HTTP connections get a 301 redirect to HTTPS instead of an error.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	cert, err := tls.LoadX509KeyPair(svc.Config.TLSCert, svc.Config.TLSKey)
	if err != nil {
		return fmt.Errorf("load TLS keypair: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	splitLn := &tlsSplitListener{
		Listener: ln,
		port:     svc.Config.Port,
		tlsCfg:   tlsConfig,
	}

	slog.Info("Starting spinifex-ui service with HTTPS (auto-redirect HTTP)", "addr", addr)
	return server.Serve(splitLn)
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	csp := buildCSP()
	slog.Info("Content-Security-Policy configured", "csp", csp)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), browsing-topics=()")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// buildCSP constructs the Content-Security-Policy header. All API requests are
// proxied through the same origin so connect-src only needs 'self'.
func buildCSP() string {
	return "default-src 'self'; script-src 'self'; style-src 'self'; " +
		"img-src 'self'; font-src 'self' data:; connect-src 'self'; " +
		"object-src 'none'; base-uri 'self'; form-action 'self'; " +
		"frame-ancestors 'none'; upgrade-insecure-requests;"
}

// newProxyTransport creates an *http.Transport that trusts the given CA
// certificate so the reverse proxy can connect to backend services using
// self-signed TLS certificates.
func newProxyTransport(caCertPath string) (*http.Transport, error) {
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert %s: %w", caCertPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA cert from %s", caCertPath)
	}
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: pool,
		},
	}, nil
}

// newReverseProxy creates a reverse proxy that forwards requests to the given
// backend host:port after stripping the pathPrefix from the request path.
// The proxy sets req.Host to the backend address so SigV4 signature verification
// succeeds (the gateway uses r.Host for the canonical host header).
func newReverseProxy(backendHost, pathPrefix string, transport *http.Transport) http.Handler {
	target := &url.URL{
		Scheme: "https",
		Host:   backendHost,
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host

			// Strip the proxy path prefix.
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, pathPrefix)
			if pr.Out.URL.Path == "" {
				pr.Out.URL.Path = "/"
			}
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("Proxy error", "backend", backendHost, "path", r.URL.Path, "error", err)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
				`<Error><Code>BadGateway</Code>`+
				`<Message>upstream connection failed</Message></Error>`)
		},
	}

	return proxy
}

// gzipContentTypes lists the MIME types eligible for gzip compression.
var gzipContentTypes = map[string]bool{
	"text/html":              true,
	"text/css":               true,
	"application/javascript": true,
	"text/javascript":        true,
	"application/json":       true,
	"image/svg+xml":          true,
	"text/plain":             true,
}

// gzipResponseWriter wraps http.ResponseWriter to compress eligible responses.
type gzipResponseWriter struct {
	http.ResponseWriter

	gw          *gzip.Writer
	wroteHeader bool
	compress    bool
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.compress {
		return w.gw.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	ct := w.ResponseWriter.Header().Get("Content-Type")
	// Strip parameters (e.g. "text/html; charset=utf-8" → "text/html")
	if idx := strings.IndexByte(ct, ';'); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}
	if gzipContentTypes[ct] {
		w.compress = true
		w.ResponseWriter.Header().Set("Content-Encoding", "gzip")
		w.ResponseWriter.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeader(code)
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gz, _ := gzip.NewWriterLevel(w, gzip.DefaultCompression)
		defer gz.Close()

		grw := &gzipResponseWriter{ResponseWriter: w, gw: gz}
		next.ServeHTTP(grw, r)
	})
}

const tlsRecordTypeHandshake = 0x16

// tlsSplitListener accepts connections and reads the first byte. If it looks
// like a TLS ClientHello (0x16), the connection is wrapped with TLS. Otherwise,
// a plain-HTTP redirect to HTTPS is sent and the connection is closed.
type tlsSplitListener struct {
	net.Listener

	port   int
	tlsCfg *tls.Config
}

func (ln *tlsSplitListener) Accept() (net.Conn, error) {
	for {
		conn, err := ln.Listener.Accept()
		if err != nil {
			return nil, err
		}

		// Set a deadline for the initial protocol detection byte so a
		// slow/malicious client cannot block the accept loop.
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

		var buf [1]byte
		if _, err := io.ReadFull(conn, buf[:]); err != nil {
			_ = conn.Close()
			continue
		}

		// Clear the deadline before handing off the connection.
		_ = conn.SetReadDeadline(time.Time{})

		if buf[0] == tlsRecordTypeHandshake {
			// Prepend the peeked byte and upgrade to TLS.
			merged := &prefixConn{
				Conn: conn,
				r:    io.MultiReader(bytes.NewReader(buf[:]), conn),
			}
			return tls.Server(merged, ln.tlsCfg), nil
		}

		// Plain HTTP — send redirect and close.
		go ln.redirectHTTP(conn, buf[0])
	}
}

// redirectHTTP reads an HTTP request from conn (with the first byte already
// consumed into firstByte), writes a 301 redirect to HTTPS, and closes conn.
func (ln *tlsSplitListener) redirectHTTP(conn net.Conn, firstByte byte) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	br := bufio.NewReader(io.MultiReader(bytes.NewReader([]byte{firstByte}), conn))
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	defer req.Body.Close()

	host := req.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// Sanitize host to prevent header injection via CRLF.
	if strings.ContainsAny(host, "\r\n") {
		return
	}

	target := fmt.Sprintf("https://%s:%d%s", host, ln.port, req.URL.RequestURI())
	resp := &http.Response{
		StatusCode: http.StatusMovedPermanently,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Location": {target}, "Connection": {"close"}},
		Body:       http.NoBody,
	}
	_ = resp.Write(conn)
}

// prefixConn wraps a net.Conn with a reader that replays prefixed bytes.
type prefixConn struct {
	net.Conn

	r io.Reader
}

func (c *prefixConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}
