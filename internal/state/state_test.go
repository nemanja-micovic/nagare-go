package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

func writeState(t *testing.T, dir string, filename string, s models.SessionState) {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAllStates_Empty(t *testing.T) {
	dir := t.TempDir()
	states := LoadAllStates(dir)
	if len(states) != 0 {
		t.Errorf("expected 0 states, got %d", len(states))
	}
}

func TestLoadAllStates_SingleFile(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, "abc.json", models.SessionState{
		State:     "idle",
		SessionID: "abc",
		Cwd:       "/home/user/project",
		Event:     "Stop",
		Timestamp: "2026-03-29T10:00:00Z",
	})

	states := LoadAllStates(dir)
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}
	s, ok := states["/home/user/project"]
	if !ok {
		t.Fatal("expected state keyed by cwd")
	}
	if s.State != "idle" {
		t.Errorf("state = %q, want %q", s.State, "idle")
	}
}

func TestLoadAllStates_ConflictLiveOverDead(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, "dead.json", models.SessionState{
		State:     "dead",
		SessionID: "dead-id",
		Cwd:       "/home/user/project",
		Event:     "SessionEnd",
		Timestamp: "2026-03-29T12:00:00Z",
	})
	writeState(t, dir, "live.json", models.SessionState{
		State:     "working",
		SessionID: "live-id",
		Cwd:       "/home/user/project",
		Event:     "UserPromptSubmit",
		Timestamp: "2026-03-29T10:00:00Z",
	})

	states := LoadAllStates(dir)
	s := states["/home/user/project"]
	if s.State != "working" {
		t.Errorf("live should beat dead: got %q", s.State)
	}
}

func TestLoadAllStates_ConflictNewerWins(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, "old.json", models.SessionState{
		State:     "idle",
		SessionID: "old-id",
		Cwd:       "/home/user/project",
		Event:     "Stop",
		Timestamp: "2026-03-29T10:00:00Z",
	})
	writeState(t, dir, "new.json", models.SessionState{
		State:     "working",
		SessionID: "new-id",
		Cwd:       "/home/user/project",
		Event:     "UserPromptSubmit",
		Timestamp: "2026-03-29T12:00:00Z",
	})

	states := LoadAllStates(dir)
	s := states["/home/user/project"]
	if s.State != "working" {
		t.Errorf("newer should win: got %q", s.State)
	}
}

func TestLoadStatesByPaneID(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(s models.SessionState) {
		if err := WriteState(dir, s); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(models.SessionState{SessionID: "a", Cwd: "/x", PaneID: "%1", State: "working", Timestamp: "2026-04-17T12:00:00Z"})
	mustWrite(models.SessionState{SessionID: "b", Cwd: "/x", PaneID: "%2", State: "idle", Timestamp: "2026-04-17T12:00:01Z"})

	got := LoadStatesByPaneID(dir)
	if got["%1"].State != "working" {
		t.Errorf("%%1 state = %q", got["%1"].State)
	}
	if got["%2"].State != "idle" {
		t.Errorf("%%2 state = %q", got["%2"].State)
	}
}

func TestLoadStatesByPaneID_ConflictLiveOverDead(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, "dead.json", models.SessionState{
		State:     "dead",
		SessionID: "dead-id",
		PaneID:    "%5",
		Cwd:       "/home/user/project",
		Event:     "SessionEnd",
		Timestamp: "2026-03-29T12:00:00Z",
	})
	writeState(t, dir, "live.json", models.SessionState{
		State:     "working",
		SessionID: "live-id",
		PaneID:    "%5",
		Cwd:       "/home/user/project",
		Event:     "UserPromptSubmit",
		Timestamp: "2026-03-29T10:00:00Z",
	})

	states := LoadStatesByPaneID(dir)
	s := states["%5"]
	if s.State != "working" {
		t.Errorf("live should beat dead: got %q", s.State)
	}
}

func TestLoadStatesByPaneID_ConflictNewerWins(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, "old.json", models.SessionState{
		State:     "idle",
		SessionID: "old-id",
		PaneID:    "%5",
		Cwd:       "/home/user/project",
		Event:     "Stop",
		Timestamp: "2026-03-29T10:00:00Z",
	})
	writeState(t, dir, "new.json", models.SessionState{
		State:     "working",
		SessionID: "new-id",
		PaneID:    "%5",
		Cwd:       "/home/user/project",
		Event:     "UserPromptSubmit",
		Timestamp: "2026-03-29T12:00:00Z",
	})

	states := LoadStatesByPaneID(dir)
	s := states["%5"]
	if s.State != "working" {
		t.Errorf("newer should win: got %q", s.State)
	}
}

func TestLoadStatesByPaneIDSkipsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := WriteState(dir, models.SessionState{SessionID: "a", Cwd: "/x", Timestamp: "2026-04-17T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	got := LoadStatesByPaneID(dir)
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestWriteState(t *testing.T) {
	dir := t.TempDir()
	s := models.SessionState{
		State:     "idle",
		SessionID: "test-id",
		Cwd:       "/home/user/project",
		Event:     "Stop",
		Timestamp: "2026-03-29T10:00:00Z",
	}

	err := WriteState(dir, s)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "test-id.json"))
	if err != nil {
		t.Fatal(err)
	}

	var loaded models.SessionState
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.State != "idle" {
		t.Errorf("state = %q, want %q", loaded.State, "idle")
	}
}

func TestStateFileName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"abc-123", "abc-123.json"},
		{"/home/u/.pi/agent/sessions/abc.jsonl", "-home-u-.pi-agent-sessions-abc.jsonl.json"},
		{"../../escape", "-..-escape.json"},
		{"", "unknown.json"},
	}
	for _, tt := range tests {
		if got := StateFileName(tt.in); got != tt.want {
			t.Errorf("StateFileName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A session id containing separators must still round-trip through write/load.
func TestWriteAndLoadStateWithPathLikeID(t *testing.T) {
	dir := t.TempDir()
	id := "/home/u/.pi/agent/sessions/abc.jsonl"
	if err := WriteState(dir, models.SessionState{SessionID: id, State: "working"}); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadStateByID(dir, id)
	if !ok {
		t.Fatal("state not found by path-like id")
	}
	if got.State != "working" {
		t.Errorf("state = %q, want %q", got.State, "working")
	}
}
