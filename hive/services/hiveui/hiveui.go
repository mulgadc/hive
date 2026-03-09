package hiveui

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mulgadc/hive/hive/utils"
)

var serviceName = "hive-ui"

//go:embed all:frontend/dist
var distFS embed.FS

// Config holds the configuration for the hive-ui service
type Config struct {
	Port    int    `json:"port"`
	Host    string `json:"host"`
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`
}

// Service represents the hive-ui service
type Service struct {
	Config *Config
	server *http.Server
	mu     sync.Mutex
}

// New creates a new hive-ui service
func New(config any) (*Service, error) {
	cfg, ok := config.(*Config)
	if !ok {
		return nil, fmt.Errorf("invalid config type for hive-ui service")
	}

	// Set defaults
	if cfg.Port == 0 {
		cfg.Port = 3000
	}
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}

	// Default TLS paths from home directory if not specified
	if cfg.TLSCert == "" || cfg.TLSKey == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			if cfg.TLSCert == "" {
				cfg.TLSCert = filepath.Join(homeDir, "hive", "config", "server.pem")
			}
			if cfg.TLSKey == "" {
				cfg.TLSKey = filepath.Join(homeDir, "hive", "config", "server.key")
			}
		}
	}

	return &Service{
		Config: cfg,
	}, nil
}

// Start starts the hive-ui service
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

// Stop stops the hive-ui service
func (svc *Service) Stop() error {
	return utils.StopProcess(serviceName)
}

// Status returns the status of the hive-ui service
func (svc *Service) Status() (string, error) {
	pid, err := utils.ReadPidFile(serviceName)
	if err != nil {
		return "stopped", nil
	}
	return fmt.Sprintf("running (pid: %d)", pid), nil
}

// Shutdown gracefully shuts down the hive-ui service
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

// Reload reloads the hive-ui service configuration
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

	// Serve static files from embedded filesystem
	fileServer := http.FileServer(http.FS(contentFS))

	// SPA handler: try to serve the file, fallback to index.html
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	// Wrap handler with security headers and gzip compression
	finalHandler := securityHeadersMiddleware(gzipMiddleware(handler))

	addr := fmt.Sprintf("%s:%d", svc.Config.Host, svc.Config.Port)

	server := &http.Server{
		Addr:              addr,
		Handler:           finalHandler,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
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

	slog.Info("Starting hive-ui service with HTTPS (auto-redirect HTTP)", "addr", addr)
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

// getLocalIPs returns all non-loopback IPv4 addresses on the machine.
func getLocalIPs() []string {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		slog.Warn("Failed to get network interfaces for CSP", "error", err)
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}
			ips = append(ips, ip.String())
		}
	}
	return ips
}

// buildCSP constructs the Content-Security-Policy header, dynamically adding
// each local IP with the AWS Gateway (:9999) and daemon (:8443) ports to connect-src.
func buildCSP() string {
	var b strings.Builder
	b.WriteString("'self' https://localhost:9999 https://localhost:8443")
	for _, ip := range getLocalIPs() {
		fmt.Fprintf(&b, " https://%s:9999 https://%s:8443", ip, ip)
	}
	return fmt.Sprintf(
		"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self' data:; connect-src %s; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; upgrade-insecure-requests;",
		b.String(),
	)
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
			conn.Close()
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
