package picker

import (
	"strings"
	"testing"
	"time"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

// The spinner must stay up until the agent's pane actually exists, not merely
// until nagare's own git and tmux work returns.
func TestPendingWorktreeSatisfiedBy(t *testing.T) {
	p := pendingWorktree{name: "test-worktree"}

	if p.satisfiedBy(nil) {
		t.Error("satisfiedBy(nil) = true, want false")
	}
	other := []models.Session{
		{Name: "repo/shipping", Details: models.SessionDetails{Worktree: "shipping"}},
	}
	if p.satisfiedBy(other) {
		t.Error("an unrelated worktree must not satisfy the wait")
	}
	arrived := append(other, models.Session{
		Name:    "repo/test-worktree",
		Details: models.SessionDetails{Worktree: "test-worktree"},
	})
	if !p.satisfiedBy(arrived) {
		t.Error("the awaited pane did not satisfy the wait")
	}
}

// A launch that never produces a pane must stop spinning.
func TestPendingWorktreeExpired(t *testing.T) {
	now := time.Now()
	p := pendingWorktree{name: "wt", deadline: now.Add(worktreeWait)}

	if p.expired(now) {
		t.Error("expired immediately")
	}
	if p.expired(now.Add(worktreeWait - time.Second)) {
		t.Error("expired before the deadline")
	}
	if !p.expired(now.Add(worktreeWait + time.Second)) {
		t.Error("did not expire after the deadline")
	}
}

// While creating, the status line must show progress rather than the search box;
// a failure must be visible rather than only logged.
func TestStatusLine(t *testing.T) {
	m := New()

	m.pending = &pendingWorktree{name: "test-worktree", deadline: time.Now().Add(worktreeWait)}
	if got := m.statusLine(); !strings.Contains(got, "test-worktree") || !strings.Contains(got, "Creating") {
		t.Errorf("statusLine while pending = %q, want progress text", got)
	}

	m.pending = nil
	m.statusErr = "worktree \"x\" already exists"
	if got := m.statusLine(); !strings.Contains(got, "already exists") {
		t.Errorf("statusLine with an error = %q, want the error text", got)
	}

	m.statusErr = ""
	if got := m.statusLine(); strings.Contains(got, "Creating") {
		t.Errorf("idle statusLine = %q, want the search input", got)
	}
}
