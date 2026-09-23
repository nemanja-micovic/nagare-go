// Package nvim is the Go half of the Neovim plugin (lua/nagare).
//
// Inside Neovim the plugin owns the agents: each one is a :terminal buffer,
// so the editor is the multiplexer. Two things still belong in Go:
//
//   - Listing the agents that live in tmux, so the plugin can show them next
//     to its own and one board covers both worlds.
//   - Keeping the editor alive when the terminal closes. A headless Neovim
//     listening on a socket owns the PTYs and every UI attaches to it — the
//     same shape as a multiplexer server, and Neovim has had it built in
//     since --remote-ui (0.9).
package nvim

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nemke/nagare-go/internal/git"
	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/state"
	"github.com/nemke/nagare-go/internal/tmux"
)

// Entry is one tmux agent as the plugin sees it. Field names are the JSON
// contract with lua/nagare/tmux.lua.
type Entry struct {
	Name         string `json:"name"`
	Session      string `json:"session"`
	Target       string `json:"target"`
	PaneID       string `json:"pane_id"`
	Path         string `json:"path"`
	Root         string `json:"root"`
	Agent        string `json:"agent"`
	Status       string `json:"status"`
	Branch       string `json:"branch,omitempty"`
	Worktree     string `json:"worktree,omitempty"`
	Repo         string `json:"repo,omitempty"`
	LastMessage  string `json:"last_message,omitempty"`
	LastActivity string `json:"last_activity,omitempty"`
}

// Entries converts scanned sessions into plugin entries. root resolves a
// path to its repository's main checkout, so a tmux agent in a worktree lands
// in the same project as the editor's agents for that repo.
func Entries(sessions []models.Session, root func(string) string) []Entry {
	roots := make(map[string]string)
	out := make([]Entry, 0, len(sessions))
	for _, s := range sessions {
		r, ok := roots[s.Path]
		if !ok {
			r = root(s.Path)
			if r == "" {
				r = s.Path
			}
			roots[s.Path] = r
		}
		out = append(out, Entry{
			Name:         s.Name,
			Session:      s.SessionName,
			Target:       tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex),
			PaneID:       s.PaneID,
			Path:         s.Path,
			Root:         r,
			Agent:        string(s.AgentType),
			Status:       string(s.Status),
			Branch:       s.Details.GitBranch,
			Worktree:     s.Details.Worktree,
			Repo:         s.Details.RepoName,
			LastMessage:  s.LastMessage,
			LastActivity: s.Details.LastActivity,
		})
	}
	return out
}

// WriteList scans tmux and writes its agents to w as a JSON array. No tmux
// server is not an error: it is an empty list.
func WriteList(w io.Writer) error {
	dir := state.DefaultStatesDir()
	sessions := tmux.ScanSessions(state.LoadStatesByPaneID(dir), state.LoadAllStates(dir))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Entries(sessions, git.MainRoot))
}

// maxSocketPath is the longest socket path worth trying. sun_path holds 108
// bytes on Linux and 104 on macOS; past that Neovim does not fail, it just
// never listens, which looks like a hang.
const maxSocketPath = 100

// SocketPath returns where the named runtime listens: $XDG_RUNTIME_DIR, the
// conventional home for sockets, else nagare's data directory. The empty name
// is the default runtime; a name lets separate contexts (work, personal) each
// keep their own editor and agents.
func SocketPath(name string) string {
	file := "nvim.sock"
	if name != "" {
		file = "nvim-" + name + ".sock"
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "nagare", file)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nagare", file)
}

// alive reports whether something accepts connections on sock.
func alive(sock string) bool {
	c, err := net.DialTimeout("unix", sock, 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// Attach connects this terminal to the named runtime, starting it first if
// it is not running. It replaces the current process with the Neovim UI, so
// on success it does not return.
func Attach(name string) error {
	bin, err := exec.LookPath("nvim")
	if err != nil {
		return errors.New("nvim not found in PATH")
	}
	sock := SocketPath(name)
	if len(sock) > maxSocketPath {
		return fmt.Errorf("socket path %s is too long for a unix socket (%d > %d bytes); set XDG_RUNTIME_DIR to a shorter directory",
			sock, len(sock), maxSocketPath)
	}
	if !alive(sock) {
		if err := start(bin, sock); err != nil {
			return err
		}
	}
	return syscall.Exec(bin, []string{"nvim", "--remote-ui", "--server", sock}, os.Environ())
}

// start launches a headless Neovim on sock in its own session, so closing
// the terminal that started it does not take it — or its agents — down.
func start(bin, sock string) error {
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		return err
	}
	// A socket file nobody answers on is left over from a crash; Neovim
	// refuses to listen on it.
	if _, err := os.Stat(sock); err == nil {
		if err := os.Remove(sock); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}
	}
	cmd := exec.Command(bin, "--headless", "--listen", sock)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start nvim server: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return err
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if alive(sock) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("nvim server did not come up on %s", sock)
}
