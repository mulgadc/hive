package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mulgadc/spinifex/spinifex/vm"
)

const (
	// LocalStateSchemaVersion is the on-disk schema version for instance-state.json.
	// Bump on any breaking change to the LocalState shape; daemon refuses to start
	// on an unknown version rather than silently losing data.
	LocalStateSchemaVersion = 1

	// DefaultLocalStateDir is the default directory under DataDir for local state files.
	DefaultLocalStateDir = "state"

	// LocalStateFileName is the on-disk filename for per-node instance state.
	LocalStateFileName = "instance-state.json"

	// DefaultDataDir is the fallback DataDir when config.DataDir is empty.
	DefaultDataDir = "/var/lib/spinifex"
)

// LocalState is the on-disk representation of a node's instance state.
// SchemaVersion gates compatibility; unknown versions are rejected.
type LocalState struct {
	SchemaVersion int               `json:"schema_version"`
	VMS           map[string]*vm.VM `json:"vms"`
}

// LocalStatePath returns the absolute path to the per-node instance state file.
// Resolution order: dataDir, then baseDir, then DefaultDataDir. The first
// non-empty value wins. Production sets dataDir (/var/lib/spinifex); tests
// typically set baseDir to a t.TempDir().
func LocalStatePath(dataDir, baseDir string) string {
	root := dataDir
	if root == "" {
		root = baseDir
	}
	if root == "" {
		root = DefaultDataDir
	}
	return filepath.Join(root, DefaultLocalStateDir, LocalStateFileName)
}

// WriteLocalState atomically writes the instance state to path.
// Caller must hold instances.Mu.
//
// Atomicity: marshal → write to <path>.tmp → fsync → rename. The rename is
// atomic on POSIX so a concurrent reader sees either the old file or the new
// one, never a half-written file.
func WriteLocalState(path string, instances *vm.Instances) error {
	state := LocalState{
		SchemaVersion: LocalStateSchemaVersion,
		VMS:           instances.VMS,
	}

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal local state: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open tmp %s: %w", tmp, err)
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write tmp %s: %w", tmp, err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("fsync tmp %s: %w", tmp, err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close tmp %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}

	return nil
}

// ReadLocalState reads the per-node instance state from path.
//
// Returns (nil, nil) if the file does not exist — fresh-install signal,
// caller should start with an empty instance map.
//
// Returns an error on:
//   - JSON parse failure (corruption — caller refuses start)
//   - Unknown SchemaVersion (caller refuses start; safer than silent migration)
func ReadLocalState(path string) (*LocalState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Debug("Local state file missing, treating as fresh install", "path", path)
			return nil, nil
		}
		return nil, fmt.Errorf("read local state %s: %w", path, err)
	}

	var state LocalState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse local state %s: %w", path, err)
	}

	if state.SchemaVersion != LocalStateSchemaVersion {
		return nil, fmt.Errorf("local state %s: unknown schema_version %d (expected %d)",
			path, state.SchemaVersion, LocalStateSchemaVersion)
	}

	if state.VMS == nil {
		state.VMS = make(map[string]*vm.VM)
	}

	return &state, nil
}
