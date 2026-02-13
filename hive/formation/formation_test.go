package formation

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCreds() *SharedCredentials {
	return &SharedCredentials{
		AccessKey:   "AKIATEST1234567890AB",
		SecretKey:   "testSecretKey1234567890",
		AccountID:   "123456789012",
		NatsToken:   "nats_testtoken123456",
		ClusterName: "hive",
		Region:      "us-west-1",
	}
}

func testNode(name, ip string) NodeInfo {
	return NodeInfo{
		Name:   name,
		BindIP: ip,
		Region: "us-west-1",
		AZ:     "us-west-1a",
		Port:   4432,
	}
}

func TestNewFormationServer(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(3, testCreds(), "ca-cert-pem", "ca-key-pem")

	assert.Equal(t, 3, fs.expected)
	assert.Empty(t, fs.nodes)
	assert.False(t, fs.IsComplete())
}

func TestRegisterNode(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(3, testCreds(), "", "")

	err := fs.RegisterNode(testNode("node1", "10.0.0.1"))
	require.NoError(t, err)

	nodes := fs.Nodes()
	assert.Len(t, nodes, 1)
	assert.Equal(t, "10.0.0.1", nodes["node1"].BindIP)
}

func TestRegisterDuplicateName(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(3, testCreds(), "", "")

	require.NoError(t, fs.RegisterNode(testNode("node1", "10.0.0.1")))
	err := fs.RegisterNode(testNode("node1", "10.0.0.2"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegisterDuplicateIP(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(3, testCreds(), "", "")

	require.NoError(t, fs.RegisterNode(testNode("node1", "10.0.0.1")))
	err := fs.RegisterNode(testNode("node2", "10.0.0.1"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bind IP")
}

func TestIsComplete(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(2, testCreds(), "", "")

	assert.False(t, fs.IsComplete())
	require.NoError(t, fs.RegisterNode(testNode("node1", "10.0.0.1")))
	assert.False(t, fs.IsComplete())
	require.NoError(t, fs.RegisterNode(testNode("node2", "10.0.0.2")))
	assert.True(t, fs.IsComplete())
}

func TestWaitForCompletion(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(2, testCreds(), "", "")

	go func() {
		time.Sleep(50 * time.Millisecond)
		fs.RegisterNode(testNode("node1", "10.0.0.1"))
		fs.RegisterNode(testNode("node2", "10.0.0.2"))
	}()

	err := fs.WaitForCompletion(5 * time.Second)
	require.NoError(t, err)
	assert.True(t, fs.IsComplete())
}

func TestWaitForCompletionTimeout(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(3, testCreds(), "", "")

	require.NoError(t, fs.RegisterNode(testNode("node1", "10.0.0.1")))

	err := fs.WaitForCompletion(100 * time.Millisecond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func testServer(t *testing.T, fs *FormationServer) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /formation/join", fs.handleJoin)
	mux.HandleFunc("GET /formation/status", fs.handleStatus)
	mux.HandleFunc("GET /formation/health", fs.handleHealth)
	return httptest.NewServer(mux)
}

func TestJoinEndpoint(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(3, testCreds(), "", "")
	ts := testServer(t, fs)
	defer ts.Close()

	body, _ := json.Marshal(JoinRequest{NodeInfo: testNode("node1", "10.0.0.1")})
	resp, err := http.Post(ts.URL+"/formation/join", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var jr JoinResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&jr))
	assert.True(t, jr.Success)
	assert.Equal(t, 1, jr.Joined)
	assert.Equal(t, 3, jr.Expected)
}

func TestJoinEndpointDuplicate(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(3, testCreds(), "", "")
	ts := testServer(t, fs)
	defer ts.Close()

	body, _ := json.Marshal(JoinRequest{NodeInfo: testNode("node1", "10.0.0.1")})

	resp1, err := http.Post(ts.URL+"/formation/join", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp1.Body.Close()
	assert.Equal(t, http.StatusOK, resp1.StatusCode)

	resp2, err := http.Post(ts.URL+"/formation/join", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)
}

func TestStatusEndpointIncomplete(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(3, testCreds(), "ca-cert", "ca-key")
	ts := testServer(t, fs)
	defer ts.Close()

	require.NoError(t, fs.RegisterNode(testNode("node1", "10.0.0.1")))

	resp, err := http.Get(ts.URL + "/formation/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var sr StatusResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sr))
	assert.False(t, sr.Complete)
	assert.Equal(t, 1, sr.Joined)
	assert.Equal(t, 3, sr.Expected)
	assert.Nil(t, sr.Credentials)
	assert.Empty(t, sr.CACert)
	assert.Nil(t, sr.Nodes)
}

func TestStatusEndpointComplete(t *testing.T) {
	t.Parallel()
	creds := testCreds()
	fs := NewFormationServer(2, creds, "ca-cert-data", "ca-key-data")
	ts := testServer(t, fs)
	defer ts.Close()

	require.NoError(t, fs.RegisterNode(testNode("node1", "10.0.0.1")))
	require.NoError(t, fs.RegisterNode(testNode("node2", "10.0.0.2")))

	resp, err := http.Get(ts.URL + "/formation/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var sr StatusResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sr))
	assert.True(t, sr.Complete)
	assert.Equal(t, 2, sr.Joined)
	assert.Equal(t, 2, sr.Expected)
	assert.Len(t, sr.Nodes, 2)
	require.NotNil(t, sr.Credentials)
	assert.Equal(t, creds.AccessKey, sr.Credentials.AccessKey)
	assert.Equal(t, "ca-cert-data", sr.CACert)
	assert.Equal(t, "ca-key-data", sr.CAKey)
}

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()
	fs := NewFormationServer(1, testCreds(), "", "")
	ts := testServer(t, fs)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/formation/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestBuildClusterRoutes(t *testing.T) {
	t.Parallel()
	nodes := map[string]NodeInfo{
		"node3": {Name: "node3", BindIP: "10.0.0.3"},
		"node1": {Name: "node1", BindIP: "10.0.0.1"},
		"node2": {Name: "node2", BindIP: "10.0.0.2"},
	}

	routes := BuildClusterRoutes(nodes)
	assert.Equal(t, []string{"10.0.0.1:4248", "10.0.0.2:4248", "10.0.0.3:4248"}, routes)
}

func TestBuildClusterRoutesUsesClusterIP(t *testing.T) {
	t.Parallel()
	nodes := map[string]NodeInfo{
		"node1": {Name: "node1", BindIP: "10.0.0.1", ClusterIP: "192.168.1.1"},
		"node2": {Name: "node2", BindIP: "10.0.0.2"}, // no cluster IP, fallback to bind
	}

	routes := BuildClusterRoutes(nodes)
	assert.Equal(t, []string{"192.168.1.1:4248", "10.0.0.2:4248"}, routes)
}

func TestBuildPredastoreNodes(t *testing.T) {
	t.Parallel()
	nodes := map[string]NodeInfo{
		"node3": {Name: "node3", BindIP: "10.0.0.3"},
		"node1": {Name: "node1", BindIP: "10.0.0.1"},
		"node2": {Name: "node2", BindIP: "10.0.0.2"},
	}

	pnodes := BuildPredastoreNodes(nodes)
	require.Len(t, pnodes, 3)
	assert.Equal(t, 1, pnodes[0].ID)
	assert.Equal(t, "10.0.0.1", pnodes[0].Host)
	assert.Equal(t, 2, pnodes[1].ID)
	assert.Equal(t, "10.0.0.2", pnodes[1].Host)
	assert.Equal(t, 3, pnodes[2].ID)
	assert.Equal(t, "10.0.0.3", pnodes[2].Host)
}

func TestFullFormationFlow(t *testing.T) {
	t.Parallel()
	creds := testCreds()
	fs := NewFormationServer(3, creds, "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----", "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----")
	ts := testServer(t, fs)
	defer ts.Close()

	// 3 nodes join sequentially
	for i, n := range []struct{ name, ip string }{
		{"node1", "10.0.0.1"},
		{"node2", "10.0.0.2"},
		{"node3", "10.0.0.3"},
	} {
		body, _ := json.Marshal(JoinRequest{NodeInfo: testNode(n.name, n.ip)})
		resp, err := http.Post(ts.URL+"/formation/join", "application/json", bytes.NewReader(body))
		require.NoError(t, err)

		var jr JoinResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&jr))
		resp.Body.Close()

		assert.True(t, jr.Success)
		assert.Equal(t, i+1, jr.Joined)
	}

	// All nodes poll status — should be complete
	resp, err := http.Get(ts.URL + "/formation/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var sr StatusResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sr))
	assert.True(t, sr.Complete)
	assert.Equal(t, 3, sr.Joined)
	assert.Len(t, sr.Nodes, 3)
	require.NotNil(t, sr.Credentials)
	assert.Equal(t, creds.AccessKey, sr.Credentials.AccessKey)
	assert.Equal(t, creds.SecretKey, sr.Credentials.SecretKey)
	assert.Equal(t, creds.NatsToken, sr.Credentials.NatsToken)
	assert.NotEmpty(t, sr.CACert)
	assert.NotEmpty(t, sr.CAKey)

	// Verify helper outputs
	routes := BuildClusterRoutes(sr.Nodes)
	assert.Len(t, routes, 3)

	pnodes := BuildPredastoreNodes(sr.Nodes)
	assert.Len(t, pnodes, 3)
	assert.Equal(t, 1, pnodes[0].ID)
}
