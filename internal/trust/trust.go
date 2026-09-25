// Package trust decides whether a repository's .nagare files may act.
//
// `.nagare/verify` runs a command when an agent stops and `.nagare/policy`
// approves tool calls on your behalf. Both live in the repository, so a
// repository you cloned could ship them — a verify command is then code that
// runs with no prompt, and a permissive policy approves anything. Like
// direnv's `allow`, a file acts only once you have approved its exact
// contents; any change (a pull, an agent's edit) needs approving again.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nemke/nagare-go/internal/fsutil"
	"github.com/nemke/nagare-go/internal/git"
)

// DefaultPath is where approvals are recorded — outside every repository.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nagare", "trust.json")
}

// Store records approved file contents by absolute path.
type Store struct {
	Path string
	mu   sync.Mutex
}

// Open returns the store at the default location.
func Open() *Store {
	return &Store{Path: DefaultPath()}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Store) load() map[string]string {
	m := map[string]string{}
	data, err := os.ReadFile(s.Path)
	if err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func abs(path string) string {
	if p, err := filepath.Abs(path); err == nil {
		path = p
	}
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	return path
}

// key names what an approval covers: a file's place in its repository —
// the main checkout plus the path inside the repo — so every worktree's copy
// of .nagare/verify shares one approval, as long as the contents match.
// Outside a repository it is just the absolute path.
func key(file string) string {
	file = abs(file)
	dir := filepath.Dir(file)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return file
	}
	top := abs(strings.TrimSpace(string(out)))
	rel, err := filepath.Rel(top, file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return file
	}
	main := git.MainRoot(dir)
	if main == "" {
		return file
	}
	return filepath.Join(abs(main), rel)
}

// Trusted reports whether file's current contents were approved.
func (s *Store) Trusted(file string) bool {
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()[key(file)] == digest(data)
}

// Allow approves file's current contents.
func (s *Store) Allow(file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	m[key(file)] = digest(data)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	return fsutil.AtomicWrite(s.Path, out, 0o600)
}

// Revoke forgets any approval of file.
func (s *Store) Revoke(file string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	delete(m, key(file))
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(s.Path, out, 0o600)
}
