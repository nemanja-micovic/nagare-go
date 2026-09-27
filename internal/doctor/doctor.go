// Package doctor checks that everything nagare depends on is in place, and
// says how to fix what is not.
package doctor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/nemke/nagare-go/internal/paths"
	"github.com/nemke/nagare-go/internal/theme"
)

// Level is how a check came out.
type Level int

const (
	OK Level = iota
	Warn
	Fail
	Info // not a problem, just worth knowing
)

// Check is one line of the report.
type Check struct {
	Level  Level
	Name   string
	Detail string
	Fix    string // what to do about a Warn or Fail
}

// Env is everything the checks look at, so tests can point them elsewhere.
type Env struct {
	Home     string
	DataDir  string
	LookPath func(string) (string, error)
	Output   func(name string, args ...string) (string, error)
	Getenv   func(string) string
	Now      time.Time
}

// DefaultEnv is the real machine.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		Home:     home,
		DataDir:  paths.Data(),
		LookPath: exec.LookPath,
		Output: func(name string, args ...string) (string, error) {
			out, err := exec.Command(name, args...).Output()
			return string(out), err
		},
		Getenv: os.Getenv,
		Now:    time.Now(),
	}
}

// agent is one supported agent and where setup connects it.
type agent struct {
	name   string
	bin    string
	status string // file whose presence (and hook-state mention) shows status reporting
	hook   bool   // status file must mention hook-state, not just exist
	mcp    string // file that must register the nagare MCP server
}

func agents(home string) []agent {
	j := func(p ...string) string { return filepath.Join(append([]string{home}, p...)...) }
	return []agent{
		{"Claude Code", "claude", j(".claude", "settings.json"), true, j(".claude.json")},
		{"Codex", "codex", j(".codex", "hooks.json"), true, j(".codex", "config.toml")},
		{"Gemini CLI", "gemini", j(".gemini", "settings.json"), true, j(".gemini", "settings.json")},
		{"OpenCode", "opencode", j(".config", "opencode", "plugins", "nagare.js"), false, j(".config", "opencode", "opencode.json")},
		{"Crush", "crush", "", false, j(".config", "crush", "crush.json")},
		{"pi", "pi", j(".pi", "agent", "extensions", "nagare.ts"), false, ""},
		{"OhMyPi", "omp", j(".omp", "agent", "extensions", "nagare.ts"), false, j(".omp", "agent", "mcp.json")},
	}
}

var tmuxVersion = regexp.MustCompile(`tmux (?:next-)?(\d+)\.(\d+)`)

// Run performs every check.
func Run(env Env) []Check {
	var checks []Check

	// tmux: required, and 3.2+ for popups, client flags and control mode.
	if _, err := env.LookPath("tmux"); err != nil {
		checks = append(checks, Check{Fail, "tmux", "not installed", "install tmux 3.2 or newer with your package manager"})
	} else {
		out, _ := env.Output("tmux", "-V")
		v := strings.TrimSpace(out)
		m := tmuxVersion.FindStringSubmatch(v)
		switch {
		case m == nil:
			checks = append(checks, Check{Warn, "tmux", fmt.Sprintf("unrecognised version %q", v), "nagare is tested with tmux 3.2 and newer"})
		case atoi(m[1]) < 3 || (atoi(m[1]) == 3 && atoi(m[2]) < 2):
			checks = append(checks, Check{Fail, "tmux", v + " is too old", "upgrade to tmux 3.2 or newer"})
		default:
			checks = append(checks, Check{OK, "tmux", v, ""})
		}
	}

	if _, err := env.LookPath("git"); err != nil {
		checks = append(checks, Check{Warn, "git", "not installed", "install git for branches, worktrees and the review panel"})
	} else {
		checks = append(checks, Check{OK, "git", "installed", ""})
	}

	switch ct := env.Getenv("COLORTERM"); {
	case ct == "truecolor" || ct == "24bit":
		checks = append(checks, Check{OK, "colour", "truecolor terminal", ""})
	default:
		checks = append(checks, Check{Info, "colour", "terminal does not advertise truecolor (COLORTERM)",
			"themes fall back to 256 colours; inside tmux add `set -as terminal-features ',*:RGB'`"})
	}

	// Agents: installed ones must be connected; missing ones are just noted.
	found := 0
	for _, a := range agents(env.Home) {
		if _, err := env.LookPath(a.bin); err != nil {
			continue
		}
		found++
		var missing []string
		if a.status != "" && !mentions(a.status, a.hook) {
			missing = append(missing, "status reporting")
		}
		if a.mcp != "" && !registersMCP(a.mcp) {
			missing = append(missing, "messaging (MCP)")
		}
		switch {
		case len(missing) > 0:
			checks = append(checks, Check{Fail, a.name, "not connected: " + strings.Join(missing, ", "), "run `nagare-go setup`"})
		case a.status == "":
			checks = append(checks, Check{OK, a.name, "connected (messaging only: it has no status hooks)", ""})
		default:
			checks = append(checks, Check{OK, a.name, "connected", ""})
		}
	}
	if found == 0 {
		checks = append(checks, Check{Warn, "agents", "no supported agent found on PATH",
			"install Claude Code, Codex, OpenCode, Gemini CLI, Crush, pi or OhMyPi — or try `nagare-go demo`"})
	}

	// Status events: the proof that hooks actually fire.
	states := filepath.Join(env.DataDir, "states")
	var newest time.Time
	entries, _ := os.ReadDir(states)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".json") && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	if newest.IsZero() {
		checks = append(checks, Check{Info, "status events", "none received yet",
			"after setup, start an agent in tmux: its state appears as soon as it does anything"})
	} else {
		checks = append(checks, Check{OK, "status events", "last one " + ago(env.Now.Sub(newest)), ""})
	}

	if err := os.MkdirAll(env.DataDir, 0o755); err != nil {
		checks = append(checks, Check{Fail, "data directory", err.Error(), "make " + env.DataDir + " writable"})
	} else if f, err := os.CreateTemp(env.DataDir, ".doctor-*"); err != nil {
		checks = append(checks, Check{Fail, "data directory", "not writable", "make " + env.DataDir + " writable"})
	} else {
		f.Close()
		os.Remove(f.Name())
		checks = append(checks, Check{OK, "data directory", env.DataDir, ""})
	}
	return checks
}

// mentions reports whether the file exists — and, when hook is set, whether it
// mentions nagare's hook-state command.
func mentions(path string, hook bool) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return !hook || strings.Contains(string(b), "hook-state")
}

// registersMCP reports whether a config names the nagare MCP server.
func registersMCP(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, `"nagare"`) || strings.Contains(s, "[mcp_servers.nagare]")
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

// Print writes the report, and returns whether anything failed.
func Print(w io.Writer, checks []Check) bool {
	c := theme.Current().Colors
	mark := map[Level]string{
		OK:   lipgloss.NewStyle().Foreground(c.Success).Render("✓"),
		Warn: lipgloss.NewStyle().Foreground(c.Warning).Render("!"),
		Fail: lipgloss.NewStyle().Foreground(c.Error).Render("✗"),
		Info: lipgloss.NewStyle().Foreground(c.Muted).Render("·"),
	}
	name := lipgloss.NewStyle().Foreground(c.Foreground).Bold(true).Width(16)
	muted := lipgloss.NewStyle().Foreground(c.Muted)
	fix := lipgloss.NewStyle().Foreground(c.Accent)
	failed := false
	for _, ch := range checks {
		fmt.Fprintf(w, " %s %s %s\n", mark[ch.Level], name.Render(ch.Name), muted.Render(ch.Detail))
		if ch.Fix != "" && ch.Level != OK {
			fmt.Fprintf(w, "   %s %s\n", strings.Repeat(" ", 16), fix.Render("→ "+ch.Fix))
		}
		if ch.Level == Fail {
			failed = true
		}
	}
	return failed
}
