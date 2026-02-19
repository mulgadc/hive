package formation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"sync"
	"time"
)

// NodeInfo describes a node participating in cluster formation.
type NodeInfo struct {
	Name      string   `json:"name"`
	BindIP    string   `json:"bind_ip"`
	ClusterIP string   `json:"cluster_ip"`
	Region    string   `json:"region"`
	AZ        string   `json:"az"`
	Port      int      `json:"port"`
	Services  []string `json:"services,omitempty"`
}

// JoinRequest is the payload POSTed by joining nodes.
type JoinRequest struct {
	NodeInfo
}

// JoinResponse is returned by the formation server on join.
type JoinResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Joined   int    `json:"joined"`
	Expected int    `json:"expected"`
}

// StatusResponse is returned by the formation server status endpoint.
type StatusResponse struct {
	Complete    bool                `json:"complete"`
	Joined      int                 `json:"joined"`
	Expected    int                 `json:"expected"`
	Nodes       map[string]NodeInfo `json:"nodes,omitempty"`
	Credentials *SharedCredentials  `json:"credentials,omitempty"`
	CACert      string              `json:"ca_cert,omitempty"`
	CAKey       string              `json:"ca_key,omitempty"`
}

// SharedCredentials contains the cluster-wide credentials distributed during formation.
type SharedCredentials struct {
	AccessKey   string `json:"access_key"`
	SecretKey   string `json:"secret_key"`
	AccountID   string `json:"account_id"`
	NatsToken   string `json:"nats_token"`
	ClusterName string `json:"cluster_name"`
	Region      string `json:"region"`
}

// FormationServer is a lightweight HTTP server that coordinates cluster formation.
// Nodes register themselves via POST /formation/join. Once the expected number of
// nodes have joined, the done channel is closed and full cluster data (credentials,
// CA, node list) becomes available via GET /formation/status.
type FormationServer struct {
	mu          sync.RWMutex
	expected    int
	nodes       map[string]NodeInfo
	credentials *SharedCredentials
	caCert      string
	caKey       string
	done        chan struct{}
	server      *http.Server
}

// NewFormationServer creates a new formation server expecting the given number of nodes.
func NewFormationServer(expected int, creds *SharedCredentials, caCert, caKey string) *FormationServer {
	return &FormationServer{
		expected:    expected,
		nodes:       make(map[string]NodeInfo),
		credentials: creds,
		caCert:      caCert,
		caKey:       caKey,
		done:        make(chan struct{}),
	}
}

// RegisterNode validates and adds a node. Returns an error for duplicates.
func (fs *FormationServer) RegisterNode(info NodeInfo) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if info.Name == "" {
		return fmt.Errorf("node name is required")
	}
	if info.BindIP == "" {
		return fmt.Errorf("bind_ip is required")
	}

	// Check for duplicate name
	if _, exists := fs.nodes[info.Name]; exists {
		return fmt.Errorf("node %q already registered", info.Name)
	}

	// Check for duplicate bind IP
	for _, n := range fs.nodes {
		if n.BindIP == info.BindIP {
			return fmt.Errorf("bind IP %s already registered by node %q", info.BindIP, n.Name)
		}
	}

	fs.nodes[info.Name] = info
	slog.Info("Node registered", "name", info.Name, "bind_ip", info.BindIP, "joined", len(fs.nodes), "expected", fs.expected)

	if fs.isComplete() {
		close(fs.done)
	}

	return nil
}

// isComplete returns true when we have enough nodes. Must be called with lock held.
func (fs *FormationServer) isComplete() bool {
	return len(fs.nodes) >= fs.expected
}

// IsComplete returns true when the expected number of nodes have registered.
func (fs *FormationServer) IsComplete() bool {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.isComplete()
}

// WaitForCompletion blocks until all nodes have joined or the timeout fires.
func (fs *FormationServer) WaitForCompletion(timeout time.Duration) error {
	select {
	case <-fs.done:
		return nil
	case <-time.After(timeout):
		fs.mu.RLock()
		joined := len(fs.nodes)
		fs.mu.RUnlock()
		return fmt.Errorf("formation timed out after %s: %d/%d nodes joined", timeout, joined, fs.expected)
	}
}

// Nodes returns a copy of the registered nodes.
func (fs *FormationServer) Nodes() map[string]NodeInfo {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	out := make(map[string]NodeInfo, len(fs.nodes))
	maps.Copy(out, fs.nodes)
	return out
}

// Start launches the HTTP server on the given address (e.g. "10.0.0.1:4432").
func (fs *FormationServer) Start(bindAddr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /formation/join", fs.handleJoin)
	mux.HandleFunc("GET /formation/status", fs.handleStatus)
	mux.HandleFunc("GET /formation/health", fs.handleHealth)

	fs.server = &http.Server{
		Addr:              bindAddr,
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("formation server listen: %w", err)
	}

	go func() {
		if err := fs.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("Formation server error", "error", err)
		}
	}()

	slog.Info("Formation server started", "addr", bindAddr)
	return nil
}

// Shutdown gracefully stops the formation server.
func (fs *FormationServer) Shutdown(ctx context.Context) error {
	if fs.server == nil {
		return nil
	}
	return fs.server.Shutdown(ctx)
}

func (fs *FormationServer) handleJoin(w http.ResponseWriter, r *http.Request) {
	var req JoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, JoinResponse{
			Success: false,
			Message: fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}

	if err := fs.RegisterNode(req.NodeInfo); err != nil {
		writeJSON(w, http.StatusConflict, JoinResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	fs.mu.RLock()
	joined := len(fs.nodes)
	fs.mu.RUnlock()

	writeJSON(w, http.StatusOK, JoinResponse{
		Success:  true,
		Message:  fmt.Sprintf("node %q registered", req.Name),
		Joined:   joined,
		Expected: fs.expected,
	})
}

func (fs *FormationServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	resp := StatusResponse{
		Complete: fs.isComplete(),
		Joined:   len(fs.nodes),
		Expected: fs.expected,
	}

	// Only expose full data when formation is complete
	if fs.isComplete() {
		resp.Nodes = make(map[string]NodeInfo, len(fs.nodes))
		maps.Copy(resp.Nodes, fs.nodes)
		resp.Credentials = fs.credentials
		resp.CACert = fs.caCert
		resp.CAKey = fs.caKey
	}

	writeJSON(w, http.StatusOK, resp)
}

func (fs *FormationServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok")); err != nil {
		slog.Error("Failed to write health response", "error", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
}
