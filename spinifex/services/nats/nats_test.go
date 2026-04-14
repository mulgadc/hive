package nats

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// templatePath returns the absolute path to the canonical nats.conf template
// used by admin init. Reading at test time (rather than embedding a copy)
// ensures the test always validates the real template.
func templatePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "cmd", "spinifex", "cmd", "templates", "nats.conf")
}

// TestRenderedConfig_EnforcesAuth renders the production nats.conf template with
// a known token, starts an embedded NATS server from the resulting config, and
// verifies that unauthenticated and wrong-token connections are rejected while
// correct-token connections succeed.
func TestRenderedConfig_EnforcesAuth(t *testing.T) {
	token := "nats_test-secret-token-1234"
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "nats.conf")

	// Read and render the production template.
	raw, err := os.ReadFile(templatePath(t))
	require.NoError(t, err)

	tmpl, err := template.New("nats.conf").Parse(string(raw))
	require.NoError(t, err)

	f, err := os.Create(confPath)
	require.NoError(t, err)

	err = tmpl.Execute(f, map[string]any{
		"BindIP":    "127.0.0.1",
		"Node":      "test-node",
		"NatsToken": token,
		"DataDir":   tmpDir,
		"LogDir":    tmpDir,
	})
	f.Close()
	require.NoError(t, err)

	// Start NATS server from the rendered config.
	opts, err := server.ProcessConfigFile(confPath)
	require.NoError(t, err)

	// Override for test isolation: random port, no monitoring.
	opts.Port = -1
	opts.LogFile = ""
	opts.HTTPHost = ""
	opts.HTTPPort = 0

	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	t.Run("no token rejected", func(t *testing.T) {
		_, err := nats.Connect(ns.ClientURL(), nats.MaxReconnects(0))
		assert.Error(t, err, "unauthenticated connection should be rejected")
	})

	t.Run("wrong token rejected", func(t *testing.T) {
		_, err := nats.Connect(ns.ClientURL(), nats.Token("wrong-token"), nats.MaxReconnects(0))
		assert.Error(t, err, "wrong token should be rejected")
	})

	t.Run("correct token accepted", func(t *testing.T) {
		nc, err := nats.Connect(ns.ClientURL(), nats.Token(token), nats.MaxReconnects(0))
		require.NoError(t, err, "correct token should be accepted")
		defer nc.Close()
		assert.True(t, nc.IsConnected())
	})
}

// TestRenderedConfig_HasMigrationVersionMarker ensures the nats.conf template
// stamps the current migration version on the first line. Without this marker,
// the migration framework treats fresh installs as version 0 and tries to run
// the 0→1 migration on next upgrade. The marker must be the literal first line
// — NATSConfVersionReader only checks line 0.
func TestRenderedConfig_HasMigrationVersionMarker(t *testing.T) {
	raw, err := os.ReadFile(templatePath(t))
	require.NoError(t, err)

	firstLine := strings.SplitN(string(raw), "\n", 2)[0]
	assert.Equal(t, "# spinifex-config-version: 1", firstLine,
		"nats.conf template must start with the current migration version marker")
}
