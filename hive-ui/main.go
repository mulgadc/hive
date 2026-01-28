package main

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NYTimes/gziphandler"
)

//go:embed all:frontend/dist
var distFS embed.FS

func main() {
	// setup logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Strip the "frontend/dist" prefix from embedded filesystem
	contentFS, err := fs.Sub(distFS, "frontend/dist")
	if err != nil {
		slog.Error("Failed to create sub filesystem:", "error", err)
		os.Exit(1)
	}

	// Get home directory for certificate files
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Error("Failed to get home directory:", "error", err)
		os.Exit(1)
	}
	certFile := filepath.Join(homeDir, "hive", "config", "server.pem")
	keyFile := filepath.Join(homeDir, "hive", "config", "server.key")

	// Check if certificates exist
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		slog.Error("Certificate file not found:", "error", err)
		os.Exit(1)
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		slog.Error("Key file not found:", "error", err)
		os.Exit(1)
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
	http.Handle("/", securityHeadersMiddleware(gzipMiddleware(handler)))

	port := ":3000"

	server := &http.Server{
		Addr:              port,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Info("Starting server with HTTPS", "port", port)
	if err := server.ListenAndServeTLS(certFile, keyFile); err != nil {
		slog.Error("Failed to start server:", "error", err)
		os.Exit(1)
	}
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
		slog.Warn("Failed to create gzip middleware, serving uncompressed:", "error", err)
		return next
	}
	return g(next)
}
