package hiveui

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/NYTimes/gziphandler"
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
	utils.WritePidFile(serviceName, os.Getpid())

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
			// File exists, check if it's not an HTML file for caching
			if filepath.Ext(path) != ".html" {
				// Vite outputs unique hashes for files so we can cache them forever
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
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
		server.Shutdown(ctx)
	}()

	slog.Info("Starting hive-ui service with HTTPS", "addr", addr)
	return server.ListenAndServeTLS(svc.Config.TLSCert, svc.Config.TLSKey)
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self' data:; connect-src 'self' https://localhost:9999 https://localhost:8443; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; upgrade-insecure-requests;")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), browsing-topics=()")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func gzipMiddleware(next http.Handler) http.Handler {
	g, err := gziphandler.GzipHandlerWithOpts(gziphandler.ContentTypes([]string{
		"text/html",
		"text/css",
		"application/javascript",
		"text/javascript",
		"application/json",
		"image/svg+xml",
		"text/plain",
	}))
	if err != nil {
		slog.Warn("Failed to create gzip middleware, serving uncompressed", "error", err)
		return next
	}
	return g(next)
}
