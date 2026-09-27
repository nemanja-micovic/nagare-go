package doctor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testEnv(t *testing.T, onPath map[string]bool, tmuxV string) Env {
	home := t.TempDir()
	return Env{
		Home:    home,
		DataDir: filepath.Join(home, "data"),
		LookPath: func(b string) (string, error) {
			if onPath[b] {
				return "/usr/bin/" + b, nil
			}
			return "", errors.New("not found")
		},
		Output: func(name string, args ...string) (string, error) { return tmuxV + "\n", nil },
		Getenv: func(string) string { return "truecolor" },
		Now:    time.Now(),
	}
}

func find(checks []Check, name string) (Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestTmuxVersion(t *testing.T) {
	for v, want := range map[string]Level{"tmux 3.4": OK, "tmux 3.2a": OK, "tmux next-3.5": OK, "tmux 3.1c": Fail, "tmux 2.9": Fail, "weird": Warn} {
		env := testEnv(t, map[string]bool{"tmux": true, "git": true}, v)
		c, _ := find(Run(env), "tmux")
		if c.Level != want {
			t.Errorf("%q: level %d, want %d", v, c.Level, want)
		}
	}
	env := testEnv(t, map[string]bool{"git": true}, "")
	if c, _ := find(Run(env), "tmux"); c.Level != Fail {
		t.Error("missing tmux is not a failure")
	}
}

// TestAgentConnection — an installed agent without hooks or MCP fails with the
// fix; once setup's files are in place it passes; an agent that is not
// installed is not mentioned at all.
func TestAgentConnection(t *testing.T) {
	env := testEnv(t, map[string]bool{"tmux": true, "git": true, "claude": true}, "tmux 3.4")
	c, _ := find(Run(env), "Claude Code")
	if c.Level != Fail || !strings.Contains(c.Fix, "nagare-go setup") {
		t.Errorf("unconnected Claude: %+v", c)
	}
	os.MkdirAll(filepath.Join(env.Home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(env.Home, ".claude", "settings.json"), []byte(`{"hooks":{"Stop":[{"command":"/x/nagare-go hook-state"}]}}`), 0o644)
	os.WriteFile(filepath.Join(env.Home, ".claude.json"), []byte(`{"mcpServers":{"nagare":{}}}`), 0o644)
	c, _ = find(Run(env), "Claude Code")
	if c.Level != OK {
		t.Errorf("connected Claude: %+v", c)
	}
	if _, ok := find(Run(env), "Codex"); ok {
		t.Error("an agent that is not installed was reported")
	}
}

func TestNoAgentsSuggestsDemo(t *testing.T) {
	env := testEnv(t, map[string]bool{"tmux": true, "git": true}, "tmux 3.4")
	c, ok := find(Run(env), "agents")
	if !ok || !strings.Contains(c.Fix, "nagare-go demo") {
		t.Errorf("no agents: %+v", c)
	}
}

func TestStatusEventsAndPrint(t *testing.T) {
	env := testEnv(t, map[string]bool{"tmux": true, "git": true}, "tmux 3.4")
	if c, _ := find(Run(env), "status events"); c.Level != Info {
		t.Errorf("no events yet: %+v", c)
	}
	os.MkdirAll(filepath.Join(env.DataDir, "states"), 0o755)
	os.WriteFile(filepath.Join(env.DataDir, "states", "a.json"), []byte("{}"), 0o644)
	if c, _ := find(Run(env), "status events"); c.Level != OK || !strings.Contains(c.Detail, "just now") {
		t.Errorf("fresh event: %+v", c)
	}
	var buf bytes.Buffer
	failed := Print(&buf, Run(env))
	if failed || !strings.Contains(buf.String(), "tmux 3.4") {
		t.Errorf("print failed=%v output:\n%s", failed, buf.String())
	}
}
