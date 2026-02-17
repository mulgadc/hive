package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetViper(t *testing.T) {
	t.Cleanup(func() { viper.Reset() })
}

func TestLoadConfig_ValidTOMLFile(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.toml")

	toml := `
version = "1.0"
epoch = 1
node = "node1"

[nodes.node1]
node = "node1"
region = "us-east-1"
az = "us-east-1a"
accesskey = "AKIATEST"
secretkey = "SECRET"

[nodes.node1.daemon]
host = "127.0.0.1:8080"

[nodes.node1.nats]
host = "127.0.0.1:4222"

[nodes.node1.nats.acl]
token = "nats_testtoken"

[nodes.node1.predastore]
host = "127.0.0.1:8443"
bucket = "predastore"
region = "us-east-1"
`
	require.NoError(t, os.WriteFile(path, []byte(toml), 0600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, uint64(1), cfg.Epoch)
	assert.Equal(t, "node1", cfg.Node)
	assert.Equal(t, "1.0", cfg.Version)

	node, ok := cfg.Nodes["node1"]
	require.True(t, ok, "node1 should exist in Nodes map")
	assert.Equal(t, "us-east-1", node.Region)
	assert.Equal(t, "us-east-1a", node.AZ)
	assert.Equal(t, "AKIATEST", node.AccessKey)
	assert.Equal(t, "127.0.0.1:8080", node.Daemon.Host)
	assert.Equal(t, "127.0.0.1:4222", node.NATS.Host)
	assert.Equal(t, "nats_testtoken", node.NATS.ACL.Token)
	assert.Equal(t, "127.0.0.1:8443", node.Predastore.Host)
	assert.Equal(t, "predastore", node.Predastore.Bucket)
}

func TestLoadConfig_MultipleNodes(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.toml")

	toml := `
epoch = 2
node = "leader"
version = "2.0"

[nodes.leader]
region = "us-east-1"

[nodes.follower]
region = "us-west-2"
`
	require.NoError(t, os.WriteFile(path, []byte(toml), 0600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Len(t, cfg.Nodes, 2)
	assert.Equal(t, "us-east-1", cfg.Nodes["leader"].Region)
	assert.Equal(t, "us-west-2", cfg.Nodes["follower"].Region)
}

func TestLoadConfig_EmptyConfigPath(t *testing.T) {
	resetViper(t)
	cfg, err := LoadConfig("")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	// All zero values
	assert.Equal(t, uint64(0), cfg.Epoch)
	assert.Empty(t, cfg.Node)
}

func TestLoadConfig_NonexistentFile(t *testing.T) {
	resetViper(t)
	cfg, err := LoadConfig("/tmp/nonexistent-hive-config-test-12345.toml")
	// Not an error - falls through to defaults
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

func TestLoadConfig_MalformedTOML(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	require.NoError(t, os.WriteFile(path, []byte("this is not valid toml {{{"), 0600))

	cfg, err := LoadConfig(path)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "error reading config file")
}

func TestLoadConfig_PartialConfig(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.toml")
	require.NoError(t, os.WriteFile(path, []byte(`node = "partial-node"
epoch = 5
`), 0600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "partial-node", cfg.Node)
	assert.Equal(t, uint64(5), cfg.Epoch)
	assert.Empty(t, cfg.Version)
	assert.Nil(t, cfg.Nodes)
}

func TestLoadConfig_EnvVarOverrideWithFile(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.toml")

	// Viper's AutomaticEnv only works for keys Viper already knows about
	// (from a config file or explicit BindEnv). Provide a minimal config
	// so Viper registers the "epoch" key, then override via env.
	require.NoError(t, os.WriteFile(path, []byte(`epoch = 1
node = "file-node"
`), 0600))

	t.Setenv("HIVE_NODE", "env-node")

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	// Env vars override file values for keys Viper knows about
	assert.Equal(t, "env-node", cfg.Node)
}

func TestLoadConfig_NestedStructParsing(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "nested.toml")

	toml := `
node = "n1"

[nodes.n1]
region = "ap-southeast-2"

[nodes.n1.daemon]
host = "0.0.0.0:8080"
tlskey = "server.key"
tlscert = "server.pem"

[nodes.n1.nats]
host = "0.0.0.0:4222"

[nodes.n1.nats.acl]
token = "secret-token"

[nodes.n1.nats.sub]
subject = "test-subject"

[nodes.n1.predastore]
host = "0.0.0.0:8443"
bucket = "mybucket"
region = "ap-southeast-2"
accesskey = "AK"
secretkey = "SK"
base_dir = "/data"
node_id = 1

[nodes.n1.awsgw]
host = "0.0.0.0:9999"
debug = true
expected_nodes = 3
`
	require.NoError(t, os.WriteFile(path, []byte(toml), 0600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	n := cfg.Nodes["n1"]
	assert.Equal(t, "0.0.0.0:8080", n.Daemon.Host)
	assert.Equal(t, "server.key", n.Daemon.TLSKey)
	assert.Equal(t, "server.pem", n.Daemon.TLSCert)
	assert.Equal(t, "0.0.0.0:4222", n.NATS.Host)
	assert.Equal(t, "secret-token", n.NATS.ACL.Token)
	assert.Equal(t, "test-subject", n.NATS.Sub.Subject)
	assert.Equal(t, "0.0.0.0:8443", n.Predastore.Host)
	assert.Equal(t, "mybucket", n.Predastore.Bucket)
	assert.Equal(t, "AK", n.Predastore.AccessKey)
	assert.Equal(t, "/data", n.Predastore.BaseDir)
	assert.Equal(t, 1, n.Predastore.NodeID)
	assert.Equal(t, "0.0.0.0:9999", n.AWSGW.Host)
	assert.True(t, n.AWSGW.Debug)
	assert.Equal(t, 3, n.AWSGW.ExpectedNodes)
}

// Tests for HasService / GetServices

func TestHasService_ExplicitList(t *testing.T) {
	c := Config{Services: []string{"nats", "daemon"}}

	assert.True(t, c.HasService("nats"))
	assert.True(t, c.HasService("daemon"))
	assert.False(t, c.HasService("predastore"))
	assert.False(t, c.HasService("viperblock"))
	assert.False(t, c.HasService("ui"))
}

func TestHasService_EmptyListBackwardCompat(t *testing.T) {
	c := Config{} // no Services set

	// Empty list means all services
	for _, svc := range AllServices {
		assert.True(t, c.HasService(svc), "expected %s to be available with empty list", svc)
	}
}

func TestHasService_UnknownService(t *testing.T) {
	c := Config{Services: []string{"nats"}}
	assert.False(t, c.HasService("unknown"))
}

func TestGetServices_DefaultsToAll(t *testing.T) {
	c := Config{}
	services := c.GetServices()
	assert.Equal(t, AllServices, services)
}

func TestGetServices_ExplicitList(t *testing.T) {
	c := Config{Services: []string{"nats", "predastore"}}
	services := c.GetServices()
	assert.Equal(t, []string{"nats", "predastore"}, services)
}

// Tests for type constants

func TestNBDTransportConstants(t *testing.T) {
	assert.Equal(t, NBDTransport("socket"), NBDTransportSocket)
	assert.Equal(t, NBDTransport("tcp"), NBDTransportTCP)
}

// Tests for ViperblockConfig

func TestLoadConfig_ViperblockShardWAL_Explicit(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.toml")

	toml := `
node = "n1"

[nodes.n1]
region = "us-east-1"

[nodes.n1.viperblock]
shardwal = false
`
	require.NoError(t, os.WriteFile(path, []byte(toml), 0600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	n := cfg.Nodes["n1"]
	require.NotNil(t, n.Viperblock.ShardWAL, "ShardWAL should be set when explicitly configured")
	assert.False(t, *n.Viperblock.ShardWAL)
}

func TestLoadConfig_ViperblockShardWAL_DefaultNil(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.toml")

	toml := `
node = "n1"

[nodes.n1]
region = "us-east-1"
`
	require.NoError(t, os.WriteFile(path, []byte(toml), 0600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	n := cfg.Nodes["n1"]
	assert.Nil(t, n.Viperblock.ShardWAL, "ShardWAL should be nil when not configured (defaults to false in service)")
}

func TestLoadConfig_ViperblockShardWAL_True(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.toml")

	toml := `
node = "n1"

[nodes.n1]
region = "us-east-1"

[nodes.n1.viperblock]
shardwal = true
`
	require.NoError(t, os.WriteFile(path, []byte(toml), 0600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	n := cfg.Nodes["n1"]
	require.NotNil(t, n.Viperblock.ShardWAL)
	assert.True(t, *n.Viperblock.ShardWAL)
}
