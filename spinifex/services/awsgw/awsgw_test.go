package awsgw

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadThrottleConfig_Enabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "awsgw.toml")
	content := `
version = 2
region = "us-east-1"

[ratelimit]
enabled = true
rate = 20
burst = 100

[ratelimit.action.RunInstances]
rate = 2
burst = 40
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	cfg, err := loadThrottleConfig(path)
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, 20, cfg.Rate)
	assert.Equal(t, 100, cfg.Burst)
	assert.Equal(t, 2, cfg.Action["RunInstances"].Rate)
	assert.Equal(t, 40, cfg.Action["RunInstances"].Burst)
}

func TestLoadThrottleConfig_Disabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "awsgw.toml")
	content := `
version = 2
[ratelimit]
enabled = false
rate = 20
burst = 100
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	cfg, err := loadThrottleConfig(path)
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
}

func TestLoadThrottleConfig_NoSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "awsgw.toml")
	content := `
version = "1.0"
region = "us-east-1"
debug = false
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	cfg, err := loadThrottleConfig(path)
	require.NoError(t, err)
	// Missing section → zero-value config (disabled, rate=0, burst=0).
	assert.False(t, cfg.Enabled)
	assert.Equal(t, 0, cfg.Rate)
}

func TestLoadThrottleConfig_MissingFile(t *testing.T) {
	_, err := loadThrottleConfig("/nonexistent/awsgw.toml")
	assert.Error(t, err)
}
