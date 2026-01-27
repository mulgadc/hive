package main

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/NYTimes/gziphandler"
)

func main() {
	// setup logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Get the project root (current directory when run via make)
	projectRoot, err := os.Getwd()
	if err != nil {
		slog.Error("Failed to get project root:", "error", err)
		os.Exit(1)
	}

	// Check if dist directory exists
	distDir := filepath.Join(projectRoot, "frontend", "dist")
	if _, err := os.Stat(distDir); os.IsNotExist(err) {
		slog.Error("Frontend dist directory not found:", "error", err)
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

	// Serve static files
	fs := http.FileServer(http.Dir(distDir))

	// SPA handler: try to serve the file, fallback to index.html
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if the requested path is a file that exists
		requestedPath := filepath.Join(distDir, r.URL.Path)
		info, err := os.Stat(requestedPath)
		if err == nil && !info.IsDir() && filepath.Ext(requestedPath) != ".html" {
			// File exists, serve it and set cache headers. vite outputs unique hashes for files
			// so we can cache them forever. cache would only hit if it's not modified since last request
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			fs.ServeHTTP(w, r)
			return
		}

		// File doesn't exist, serve index.html for SPA routing
		// set cache headers to no-cache so browser always fetches the latest version
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
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
