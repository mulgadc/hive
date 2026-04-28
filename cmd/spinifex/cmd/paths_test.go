package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// withProductionMarker temporarily overrides the marker path used to detect a
// production install. Tests that pass an empty string force "dev" mode by
// pointing at a non-existent path; tests that pass t.TempDir() force
// "production" mode by pointing at a real directory.
func withProductionMarker(t *testing.T, path string) {
	t.Helper()
	orig := productionMarkerPath
	productionMarkerPath = path
	t.Cleanup(func() { productionMarkerPath = orig })
}

func TestIsProductionLayout(t *testing.T) {
	t.Run("absent marker returns false", func(t *testing.T) {
		withProductionMarker(t, filepath.Join(t.TempDir(), "does-not-exist"))
		assert.False(t, isProductionLayout())
	})
	t.Run("present marker returns true", func(t *testing.T) {
		withProductionMarker(t, t.TempDir())
		assert.True(t, isProductionLayout())
	})
}

func TestDefaultPaths(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("SUDO_USER", "")

	tests := []struct {
		name           string
		productionMode bool
		wantConfigDir  string
		wantDataDir    string
		wantConfigFile string
		wantLogDir     string // for LogDirFor("/data/dir")
	}{
		{
			name:           "dev layout",
			productionMode: false,
			wantConfigDir:  filepath.Join(homeDir, "spinifex", "config"),
			wantDataDir:    filepath.Join(homeDir, "spinifex"),
			wantConfigFile: filepath.Join(homeDir, "spinifex", "config", "spinifex.toml"),
			wantLogDir:     "/data/dir/logs",
		},
		{
			name:           "production layout",
			productionMode: true,
			wantConfigDir:  "/etc/spinifex",
			wantDataDir:    "/var/lib/spinifex",
			wantConfigFile: "/etc/spinifex/spinifex.toml",
			wantLogDir:     "/var/log/spinifex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.productionMode {
				withProductionMarker(t, t.TempDir())
			} else {
				withProductionMarker(t, filepath.Join(t.TempDir(), "absent"))
			}

			assert.Equal(t, tt.wantConfigDir, DefaultConfigDir())
			assert.Equal(t, tt.wantDataDir, DefaultDataDir())
			assert.Equal(t, tt.wantConfigFile, DefaultConfigFile())
			assert.Equal(t, tt.wantLogDir, LogDirFor("/data/dir"))
		})
	}
}
