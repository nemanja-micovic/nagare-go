package mcp

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nemke/nagare-go/internal/memory"
)

// Tool descriptions carry the memory policy, since they are all an agent
// reads before deciding to call a tool.
const (
	rememberDesc = "Save a lesson for future sessions and the other agents on this repository: something learned the hard way that is not obvious from the code — a build or test quirk, a gotcha and its root cause, a decision and why, a user correction or preference. Do not save what the code or git history already says, task progress, or secrets. One self-contained memory per call; the first line is its title."
	recallDesc   = "Search memories saved by agents and the user on this repository (plus global ones). Use before non-trivial work, and after hitting a surprising error. Returns one line per memory; fetch the full text with get_memory."
	getDesc      = "Read memories in full, by id (from recall)."
	updateDesc   = "Change a memory: correct its text, retag it, pin it into every session's digest, or archive it (status=archived) when it is wrong or obsolete. The previous version is kept."
)

// memoryStore is swapped out by tests.
var memoryStore = memory.Open

func memoryCwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func who(mySession string) memory.Who {
	author := mySession
	if author == "" {
		author = os.Getenv("NAGARE_PANE")
	}
	return memory.Who{Author: author, Session: os.Getenv("NAGARE_PANE")}
}

// RememberHandler saves a memory for the repository the agent works in.
func RememberHandler(mySession, cwd string, in memory.RememberInput) string {
	m, err := memoryStore().Remember(cwd, in, who(mySession))
	var dup *memory.DuplicateError
	switch {
	case errors.As(err, &dup):
		return "Not saved: " + err.Error()
	case err != nil:
		return "Not saved: " + err.Error()
	}
	return fmt.Sprintf("Saved as [%s] (%s, %s).", m.ID, m.Kind, m.Scope)
}

// RecallHandler searches memories.
func RecallHandler(cwd string, in memory.RecallInput) string {
	if strings.TrimSpace(in.Query) == "" {
		return "Give a query: identifiers, error text or topics."
	}
	hits := memoryStore().Recall(cwd, in)
	if len(hits) == 0 {
		return "No memories match. (Save one with remember once you learn something worth keeping.)"
	}
	now := time.Now()
	lines := make([]string, 0, len(hits))
	for _, h := range hits {
		lines = append(lines, memory.Line(h.Memory, now))
	}
	return strings.Join(lines, "\n") + "\n\nFetch full text with get_memory(ids=[...])."
}

// GetMemoryHandler returns memories in full.
func GetMemoryHandler(cwd string, in memory.GetInput) string {
	mems := memoryStore().Get(cwd, in.IDs)
	if len(mems) == 0 {
		return "No memory with those ids."
	}
	var b strings.Builder
	for i, m := range mems {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		fmt.Fprintf(&b, "[%s] %s · %s", m.ID, m.Kind, m.Scope)
		if len(m.Paths) > 0 {
			fmt.Fprintf(&b, " · %s", strings.Join(m.Paths, ", "))
		}
		if m.Commit != "" {
			fmt.Fprintf(&b, " · written at %s", m.Commit)
		}
		b.WriteString("\n" + m.Body)
	}
	return b.String()
}

// UpdateMemoryHandler changes a memory.
func UpdateMemoryHandler(cwd string, in memory.UpdateInput) string {
	m, err := memoryStore().Update(cwd, in)
	if err != nil {
		return "Not updated: " + err.Error()
	}
	return fmt.Sprintf("Updated [%s] (%s).", m.ID, m.Status)
}
