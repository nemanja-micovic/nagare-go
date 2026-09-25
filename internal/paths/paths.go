// Package paths locates nagare's data directory.
package paths

import (
	"os"
	"path/filepath"
)

// DataDirEnv overrides the data directory. The demo uses it to run against a
// throwaway directory, so trying nagare never touches a user's real state.
const DataDirEnv = "NAGARE_DATA_DIR"

// Data returns the directory holding nagare's state files, registry, log and
// message store: $NAGARE_DATA_DIR, or ~/.local/share/nagare.
func Data() string {
	if dir := os.Getenv(DataDirEnv); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nagare")
}
