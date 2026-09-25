package demo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nemke/nagare-go/internal/models"
)

// TestScenariosAreComplete — every agent the demo starts has a script, and
// every script ends by saying something, so the agent is left with a last
// message and an idle prompt rather than stopping mid-thought.
func TestScenariosAreComplete(t *testing.T) {
	for _, p := range projects {
		for _, a := range p.agents {
			sc, ok := scenarios[a.scenario]
			if !ok {
				t.Errorf("%s/%s plays unknown scenario %q", p.name, a.window, a.scenario)
				continue
			}
			if _, ok := looks[a.bin]; !ok {
				t.Errorf("%s/%s runs as %q, which has no look (and no symlink)", p.name, a.window, a.bin)
			}
			if sc.task == "" || len(sc.steps) == 0 || sc.steps[len(sc.steps)-1].say == "" {
				t.Errorf("scenario %q does not end with a message", a.scenario)
			}
		}
	}
}

// TestDemoShowsEveryState — the point of the scripted timing is that a viewer
// sees agents waiting as well as working; at least two scenarios must stop at
// a permission prompt, or the waiting queue has nothing to show.
func TestDemoShowsEveryState(t *testing.T) {
	asks := 0
	for _, sc := range scenarios {
		for _, s := range sc.steps {
			if s.ask != "" {
				asks++
			}
		}
	}
	if asks < 2 {
		t.Errorf("only %d permission prompts across the demo; the waiting queue needs at least 2", asks)
	}
}

func TestHookStatesCoverReportedStatuses(t *testing.T) {
	for _, st := range []models.SessionStatus{models.StatusRunning, models.StatusWaitingInput, models.StatusIdle} {
		if hookStates[st] == "" {
			t.Errorf("status %s has no hook state string", st)
		}
	}
	if hookStates[models.StatusRunning] != "working" {
		t.Errorf("running must be written as %q, the string the scanner reads", "working")
	}
}

func TestFollowUpNamesAFile(t *testing.T) {
	steps := followUp("Add a CHANGELOG entry, please!")
	var path string
	for _, s := range steps {
		if s.edit != nil {
			path = s.edit.path
		}
	}
	if path != "notes/add-a-changelog-entry-please.md" {
		t.Errorf("follow-up edits %q", path)
	}
	if strings.ContainsAny(path, " ,!") {
		t.Errorf("follow-up path %q carries characters unsafe in a file name", path)
	}
}

// TestSweepStaleRemovesOnlyDeadDemos — a demo killed with -9 leaves its
// directory behind; the next demo removes it, but never one whose process is
// still running, nor a directory it cannot vouch for.
func TestSweepStaleRemovesOnlyDeadDemos(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	mk := func(name, pid string) string {
		dir := filepath.Join(tmp, name)
		os.MkdirAll(dir, 0o755)
		if pid != "" {
			os.WriteFile(filepath.Join(dir, "pid"), []byte(pid), 0o644)
		}
		return dir
	}
	dead := mk("nagare-demo-dead", "999999999")
	alive := mk("nagare-demo-alive", "1")
	unknown := mk("nagare-demo-nopid", "")
	other := mk("something-else", "999999999")

	sweepStale()

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Error("a dead demo's directory was not removed")
	}
	for _, dir := range []string{alive, unknown, other} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s was removed but should have been kept", filepath.Base(dir))
		}
	}
}
