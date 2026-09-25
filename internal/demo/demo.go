// Package demo runs nagare against simulated agents, so it can be tried — and
// recorded — without installing or paying for any real coding agent.
package demo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/nemke/nagare-go/internal/paths"
	"github.com/nemke/nagare-go/internal/tmux"
)

// Env is a running demo: a private tmux server, a throwaway data directory and
// a few git repositories with simulated agents working in them.
type Env struct {
	dir    string
	socket string // the demo's private tmux server; empty until it exists
}

// NoteEnv carries the welcome line the picker shows in demo mode.
const NoteEnv = "NAGARE_DEMO_NOTE"

// project is one demo repository and the agents working in it.
type project struct {
	name   string
	files  map[string]string
	agents []demoAgent
}

type demoAgent struct {
	bin      string // claude, codex or opencode
	window   string // task name shown in nagare
	scenario string
	worktree string // run in this worktree of the project, if set
}

var projects = []project{
	{
		name: "api",
		files: map[string]string{
			"go.mod":                   "module github.com/acme/api\n\ngo 1.24\n",
			"internal/auth/refresh.go": "package auth\n\n// refresh exchanges the refresh token for a new access token.\nfunc refresh(ctx context.Context) error {\n\treturn client.Refresh(ctx)\n}\n",
			"internal/http/middleware.go": "package http\n\n// Chain wraps h in the standard middleware stack.\n" +
				"func Chain(h http.Handler, cfg Config) http.Handler {\n\treturn Logging(Auth(h))\n}\n",
		},
		agents: []demoAgent{
			{bin: "codex", window: "rate-limiter", scenario: "ratelimit"},
			{bin: "claude", window: "token-retry", scenario: "retry", worktree: "token-retry"},
		},
	},
	{
		name: "web",
		files: map[string]string{
			"package.json":        "{\n  \"name\": \"acme-web\",\n  \"private\": true\n}\n",
			"src/pages/index.tsx": "export default function Home() {\n  return <main />\n}\n",
		},
		agents: []demoAgent{{bin: "claude", window: "hero", scenario: "hero"}},
	},
	{
		name:   "docs",
		files:  map[string]string{"README.md": "# acme\n\nThe acme CLI.\n"},
		agents: []demoAgent{{bin: "opencode", window: "getting-started", scenario: "docs"}},
	},
}

// Start builds the demo and points this process at it: every tmux call goes to
// the demo's private server and every state file to its data directory. speed
// scales the agents' pacing (2 is twice as fast).
func Start(speed float64) (*Env, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("the demo needs tmux installed")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("the demo needs git installed")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	sweepStale()
	dir, err := os.MkdirTemp("", "nagare-demo-")
	if err != nil {
		return nil, err
	}
	e := &Env{dir: dir}
	// Recorded so a later demo can tell this directory is abandoned if this
	// process dies without cleaning up.
	os.WriteFile(filepath.Join(dir, "pid"), []byte(strconv.Itoa(os.Getpid())), 0o644)

	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		e.Close()
		return nil, err
	}
	for name := range looks {
		if err := os.Symlink(exe, filepath.Join(bin, name)); err != nil {
			e.Close()
			return nil, err
		}
	}

	// Everything started from here on — the tmux server, its panes, the agents
	// in them — inherits this environment.
	os.Setenv(agentEnv, "1")
	os.Setenv(parentEnv, strconv.Itoa(os.Getpid()))
	os.Setenv(speedEnv, strconv.FormatFloat(speed, 'f', -1, 64))
	os.Setenv(paths.DataDirEnv, filepath.Join(dir, "data"))
	e.socket = fmt.Sprintf("nagare-demo-%d", os.Getpid())
	os.Setenv(tmux.SocketEnv, e.socket)
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.Setenv(NoteEnv, "demo · these agents are simulated · Enter opens one, try typing to it")
	// The demo server is not the one a surrounding tmux client belongs to, so
	// nagare must treat itself as running outside tmux.
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")

	for _, p := range projects {
		if err := e.startProject(p); err != nil {
			e.Close()
			return nil, err
		}
	}
	return e, nil
}

func (e *Env) startProject(p project) error {
	root := filepath.Join(e.dir, "projects", p.name)
	for rel, body := range p.files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	if err := git(root, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	if err := git(root, "add", "-A"); err != nil {
		return err
	}
	if err := git(root, "commit", "-q", "-m", "Initial commit"); err != nil {
		return err
	}

	for i, a := range p.agents {
		dir := root
		if a.worktree != "" {
			dir = filepath.Join(root, ".worktrees", a.worktree)
			if err := git(root, "worktree", "add", "-q", "-b", a.worktree, dir); err != nil {
				return err
			}
		}
		args := []string{"-d", "-n", a.window, "-c", dir, "-e", scenarioEnv + "=" + a.scenario}
		if i == 0 {
			args = append([]string{"new-session", "-s", p.name, "-x", "200", "-y", "50"}, args...)
		} else {
			args = append([]string{"new-window", "-t", p.name + ":"}, args...)
		}
		args = append(args, a.bin)
		if out, err := tmux.Command(args...).CombinedOutput(); err != nil {
			return fmt.Errorf("start %s/%s: %v: %s", p.name, a.window, err, out)
		}
	}
	return nil
}

func git(dir string, args ...string) error {
	full := append([]string{"-C", dir, "-c", "user.name=nagare demo",
		"-c", "user.email=demo@nagare.invalid", "-c", "commit.gpgsign=false"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %v: %s", args, err, out)
	}
	return nil
}

// Close stops the demo's tmux server and deletes everything it created.
//
// The server is named explicitly rather than through the environment: a
// kill-server that fell through to the default socket would take down every one
// of the user's real sessions.
func (e *Env) Close() {
	if e.socket != "" {
		out, _ := exec.Command("tmux", "-L", e.socket, "display-message", "-p", "#{socket_path}").Output()
		exec.Command("tmux", "-L", e.socket, "kill-server").Run()
		// tmux leaves the socket file behind; one per demo run adds up.
		if path := strings.TrimSpace(string(out)); path != "" {
			os.Remove(path)
		}
	}
	os.RemoveAll(e.dir)
}

// sweepStale removes directories left by demos whose process died without
// running Close — a kill -9, a crash. Their tmux servers are already gone: the
// agents exit when their parent does.
func sweepStale() {
	dirs, _ := filepath.Glob(filepath.Join(os.TempDir(), "nagare-demo-*"))
	for _, dir := range dirs {
		b, err := os.ReadFile(filepath.Join(dir, "pid"))
		if err != nil {
			continue // not ours to judge
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid <= 0 {
			continue
		}
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			os.RemoveAll(dir)
		}
	}
}
