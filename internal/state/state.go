package state

import (
	"encoding/json"
	"github.com/nemke/nagare-go/internal/paths"
	"os"
	"path/filepath"
	"strings"

	"github.com/nemke/nagare-go/internal/fsutil"
	"github.com/nemke/nagare-go/internal/models"
)

// DefaultStatesDir returns the default states directory path.
func DefaultStatesDir() string {
	return filepath.Join(paths.Data(), "states")
}

// LoadAllStates loads all state files from dir, keyed by cwd.
// Conflict resolution: live beats dead, then newer timestamp wins.
func LoadAllStates(dir string) map[string]models.SessionState {
	states := make(map[string]models.SessionState)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return states
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var s models.SessionState
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}

		if s.Cwd == "" {
			continue
		}

		existing, exists := states[s.Cwd]
		if !exists {
			states[s.Cwd] = s
			continue
		}

		// Live beats dead
		if existing.State == "dead" && s.State != "dead" {
			states[s.Cwd] = s
		} else if existing.State != "dead" && s.State == "dead" {
			// Keep existing live state
		} else if s.Timestamp > existing.Timestamp {
			// Same liveness: newer wins
			states[s.Cwd] = s
		}
	}

	return states
}

// LoadStatesByPaneID loads all state files from dir, keyed by PaneID.
// State files without a PaneID are skipped. Conflict resolution matches
// LoadAllStates: live beats dead, then newer timestamp wins.
func LoadStatesByPaneID(dir string) map[string]models.SessionState {
	states := make(map[string]models.SessionState)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return states
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var s models.SessionState
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}

		if s.PaneID == "" {
			continue
		}

		existing, exists := states[s.PaneID]
		if !exists {
			states[s.PaneID] = s
			continue
		}

		// Live beats dead
		if existing.State == "dead" && s.State != "dead" {
			states[s.PaneID] = s
		} else if existing.State != "dead" && s.State == "dead" {
			// Keep existing live state
		} else if s.Timestamp > existing.Timestamp {
			// Same liveness: newer wins
			states[s.PaneID] = s
		}
	}

	return states
}

// StateFileName returns a filesystem-safe file name for a session ID. Session
// identifiers are opaque strings chosen by each agent — pi's, for example, is a
// path to a JSONL file — so separators and leading dots are neutralized to keep
// every state file a single flat entry inside the states directory.
func StateFileName(sessionID string) string {
	safe := strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(sessionID)
	safe = strings.TrimLeft(safe, ".")
	if safe == "" {
		safe = "unknown"
	}
	return safe + ".json"
}

// LoadStateByID loads a single state file by session ID. Returns zero value and false if not found.
func LoadStateByID(dir, sessionID string) (models.SessionState, bool) {
	path := filepath.Join(dir, StateFileName(sessionID))
	data, err := os.ReadFile(path)
	if err != nil {
		return models.SessionState{}, false
	}
	var s models.SessionState
	if err := json.Unmarshal(data, &s); err != nil {
		return models.SessionState{}, false
	}
	return s, true
}

// WriteState writes a session state to dir/{session_id}.json atomically.
func WriteState(dir string, s models.SessionState) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}

	path := filepath.Join(dir, StateFileName(s.SessionID))
	return fsutil.AtomicWrite(path, data, 0644)
}
