package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloudsync/providers"
)

// FileState records the last-synced metadata for a single file.
// We use size + modtime for fast comparison and checksum for accuracy
// when available.
type FileState struct {
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Checksum string    `json:"checksum,omitempty"`
}

// State maps a relative file path to its last-synced FileState.
type State map[string]FileState

// loadState reads the on-disk state for a provider. Returns an empty
// State (not an error) when no state file exists yet.
func loadState(stateDir, providerKey string) (State, error) {
	path := statePath(stateDir, providerKey)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}
	return s, nil
}

// saveState writes the state to disk atomically via a temp file.
func saveState(stateDir, providerKey string, s State) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	path := statePath(stateDir, providerKey)
	tmp := path + ".tmp"

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func statePath(stateDir, key string) string {
	// Sanitise key to a safe filename
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ' ' {
			return '_'
		}
		return r
	}, key)
	return filepath.Join(stateDir, safe+".json")
}

// hasChanged returns true when info's metadata differs from the previously
// recorded state, indicating the file should be re-synced.
func hasChanged(info providers.FileInfo, prev FileState) bool {
	// Prefer checksum comparison — unambiguous
	if info.Checksum != "" && prev.Checksum != "" {
		return info.Checksum != prev.Checksum
	}
	// Fall back to size + modification time (1-second tolerance for FAT/NTFS)
	if info.Size != prev.Size {
		return true
	}
	return info.ModTime.After(prev.ModTime.Add(time.Second))
}

// toFileState converts a providers.FileInfo into a storable FileState.
func toFileState(fi providers.FileInfo) FileState {
	return FileState{
		Size:     fi.Size,
		ModTime:  fi.ModTime,
		Checksum: fi.Checksum,
	}
}
