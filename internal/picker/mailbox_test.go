package picker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemke/nagare-go/internal/mcp"
	"github.com/nemke/nagare-go/internal/models"
)

// seedMail writes one message in each state the mailbox distinguishes into a
// fresh HOME, and returns the sessions it should consider running.
func seedMail(t *testing.T) []models.Session {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Now().UTC()
	at := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339) }
	answer := "All **42** tests pass.\n\n```go\nfunc TestX(t *testing.T) {}\n```"
	answeredAt := at(2*time.Hour - 3*time.Minute)

	msgs := []mcp.Message{
		{ID: "answered1", FromSession: "api", ToSession: "web", Status: mcp.StatusCompleted,
			ExpectsReply: true, CreatedAt: at(2 * time.Hour), RespondedAt: &answeredAt, Response: &answer,
			Content: "## Test run\n\nPlease run the suite and report:\n\n- unit tests\n- `go vet`"},
		{ID: "needsreply", FromSession: "web", ToSession: "api", Status: mcp.StatusRead,
			ExpectsReply: true, CreatedAt: at(30 * time.Minute), Content: "Which port does the API listen on?"},
		{ID: "queued1", FromSession: "api", ToSession: "web", Status: mcp.StatusPending,
			CreatedAt: at(1 * time.Minute), Content: "Heads up: I am changing the schema."},
		{ID: "lost1", FromSession: "api", ToSession: "billing", Status: mcp.StatusPending,
			CreatedAt: at(9 * 24 * time.Hour), Content: "Old note that never arrived."},
		{ID: "note1", FromSession: "web", ToSession: "api", Status: mcp.StatusRead,
			CreatedAt: at(40 * 24 * time.Hour), Content: "FYI the build is green."},
		{ID: "reply1", FromSession: "web", ToSession: "api", Status: mcp.StatusRead,
			CreatedAt: at(2*time.Hour - 3*time.Minute), InReplyTo: "answered1", Content: answer},
	}
	for _, m := range msgs {
		if err := mcp.WriteMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := mcp.Enqueue("%2", mcp.Push{ID: "queued1", ToSession: "web", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	return []models.Session{
		{Name: "api", SessionName: "api", PaneID: "%1", Status: models.StatusIdle, AgentType: models.AgentClaude},
		{Name: "web/codex", SessionName: "web", PaneID: "%2", Status: models.StatusRunning, AgentType: models.AgentCodex},
	}
}

func mailModel(t *testing.T, w, h int) Model {
	t.Helper()
	sessions := seedMail(t)
	m := NewForTest()
	// Frames are asserted at rest; a dialog mid-entry sits lower than it will.
	m.animEnabled = false
	m = driveModel(t, m,
		tea.WindowSizeMsg{Width: w, Height: h},
		SessionsUpdatedMsg(sessions),
		tea.KeyPressMsg{Code: tea.KeyF5},
	)
	if m.mail == nil {
		t.Fatal("F5 did not open the mailbox")
	}
	return m
}

func entryByID(t *testing.T, b *mailbox, id string) mailEntry {
	t.Helper()
	e, ok := b.find(id)
	if !ok {
		t.Fatalf("message %s not loaded", id)
	}
	return e
}

func TestMailboxClassifiesEveryState(t *testing.T) {
	m := mailModel(t, 160, 40)
	for id, want := range map[string]mailState{
		"answered1":  mailAnswered,
		"needsreply": mailNeedsReply,
		"queued1":    mailQueued,
		"lost1":      mailLost,
		"note1":      mailDelivered,
	} {
		if got := entryByID(t, m.mail, id).state; got != want {
			t.Errorf("%s: state %s, want %s", id, got.label(), want.label())
		}
	}
	// "billing" is not running, so an undelivered message to it is a problem;
	// "web" is running as "web/codex", which must not count as gone.
	if !entryByID(t, m.mail, "lost1").gone {
		t.Error("message to a recipient that is not running not flagged")
	}
	if entryByID(t, m.mail, "queued1").gone {
		t.Error("a renamed recipient (web → web/codex) was flagged as gone")
	}
}

// A message stored a moment ago is in transit, not lost: sends store before
// they deliver.
func TestFreshPendingMessageIsNotLost(t *testing.T) {
	env := mcp.Envelope{Message: mcp.Message{Status: mcp.StatusPending,
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}}
	if got := classify(env, nil, time.Now()).state; got != mailQueued {
		t.Errorf("fresh pending message classified %s", got.label())
	}
}

func TestMailboxFiltersAndSearch(t *testing.T) {
	m := mailModel(t, 160, 40)
	b := m.mail
	ids := func() []string {
		var out []string
		for _, e := range b.shown {
			out = append(out, e.env.ID)
		}
		return out
	}

	b.filter = filterProblems
	b.refresh()
	if got := ids(); len(got) != 1 || got[0] != "lost1" {
		t.Errorf("Problems = %v, want [lost1]", got)
	}
	b.filter = filterUnanswered
	b.refresh()
	if got := ids(); len(got) != 1 || got[0] != "needsreply" {
		t.Errorf("Unanswered = %v, want [needsreply]", got)
	}

	b.filter = filterAll
	m = typeString(t, m, "which port")
	if got := ids(); len(got) != 1 || got[0] != "needsreply" {
		t.Errorf("search 'which port' = %v, want [needsreply]", got)
	}
	// Esc clears the search before it closes the mailbox.
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mail == nil || len(m.mail.shown) != 6 {
		t.Fatal("first Esc should clear the search, not close the mailbox")
	}
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mail != nil {
		t.Error("second Esc should close the mailbox")
	}
}

func TestMailboxGroupsConversations(t *testing.T) {
	m := mailModel(t, 160, 40)
	var headers []string
	for _, r := range m.mail.rows {
		if r.group != nil {
			headers = append(headers, r.group.a+"⇄"+r.group.b)
		}
	}
	// api⇄web holds messages in both directions; api⇄billing is its own.
	if len(headers) != 2 {
		t.Fatalf("conversations = %v, want 2", headers)
	}
	// Timeline view drops the headers.
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	for _, r := range m.mail.rows {
		if r.group != nil {
			t.Fatal("timeline view still shows conversation headers")
		}
	}
}

func TestMailboxCleanupKeepsOpenMessages(t *testing.T) {
	m := mailModel(t, 160, 40)
	m = driveModel(t, m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if m.mail.confirm == nil || !m.mail.confirm.cleanup {
		t.Fatal("Ctrl+d did not open the cleanup dialog")
	}
	// Default age is 7 days: the 9-day lost message and the 40-day note go.
	m = driveModel(t, m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	left := map[string]bool{}
	for _, e := range m.mail.all {
		left[e.env.ID] = true
	}
	if left["lost1"] || left["note1"] {
		t.Errorf("old finished messages survived cleanup: %v", left)
	}
	for _, id := range []string{"needsreply", "queued1", "answered1", "reply1"} {
		if !left[id] {
			t.Errorf("%s was deleted but is open or recent", id)
		}
	}
	if !strings.Contains(m.mail.note, "Deleted 2") {
		t.Errorf("note = %q", m.mail.note)
	}
}

func TestMailboxDeleteRemovesQueuedCopy(t *testing.T) {
	m := mailModel(t, 160, 40)
	b := m.mail
	for i, e := range b.shown {
		if e.env.ID == "queued1" {
			b.cursor = i
		}
	}
	m = driveModel(t, m,
		tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'y', Text: "y"})
	if _, ok := m.mail.find("queued1"); ok {
		t.Fatal("message not deleted")
	}
	// A deleted message must never be delivered afterwards.
	if n := mcp.Pending("%2"); n != 0 {
		t.Errorf("%d queued push copies survived the delete", n)
	}
}

func TestMailboxDetailRendersMarkdown(t *testing.T) {
	m := mailModel(t, 160, 40)
	b := m.mail
	for i, e := range b.shown {
		if e.env.ID == "answered1" {
			b.cursor = i
		}
	}
	text := ansi.Strip(strings.Join(m.mailDetailRows(b.shown[b.cursor], 80), "\n"))
	for _, want := range []string{"Test run", "• unit tests", "func TestX", "Reply from web", "42 tests pass", "answered 3m after"} {
		if !strings.Contains(text, want) {
			t.Errorf("detail missing %q:\n%s", want, text)
		}
	}
	// Markdown syntax is rendered, not shown.
	if strings.Contains(text, "**42**") || strings.Contains(text, "```") {
		t.Errorf("raw markdown leaked into the detail:\n%s", text)
	}
}

// The mailbox replaces the whole frame, so it must fill it exactly and never
// leave a cell on the terminal's default background.
func TestMailboxFrameIsExactAndOpaque(t *testing.T) {
	for _, sz := range [][2]int{{200, 50}, {160, 40}, {120, 30}, {100, 24}, {90, 24}, {70, 20}, {60, 16}} {
		for _, mode := range []string{"list", "reader", "cleanup", "delete", "timeline"} {
			w, h := sz[0], sz[1]
			t.Run(fmt.Sprintf("%s/%dx%d", mode, w, h), func(t *testing.T) {
				m := mailModel(t, w, h)
				switch mode {
				case "reader":
					m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
				case "cleanup":
					m = driveModel(t, m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
				case "delete":
					m = driveModel(t, m, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
				case "timeline":
					m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
				}
				frame, _ := m.view()
				rows := strings.Split(frame, "\n")
				if len(rows) != h {
					t.Fatalf("frame has %d rows, want %d", len(rows), h)
				}
				for i, row := range rows {
					if got := ansi.StringWidth(row); got != w {
						t.Errorf("row %d is %d cells wide, want %d", i, got, w)
					}
					for col, bg := range cellBackgrounds(row) {
						if bg == "default" {
							t.Fatalf("row %d col %d is on the default background", i, col)
						}
					}
				}
				// The way back is always on screen — in the footer, or in the
				// dialog's own hint line when a dialog covers it.
				if !strings.Contains(strings.ToLower(ansi.Strip(frame)), "esc") {
					t.Error("no way out is shown")
				}
			})
		}
	}
}

func TestMailboxEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := driveModel(t, NewForTest(), tea.WindowSizeMsg{Width: 120, Height: 30}, tea.KeyPressMsg{Code: tea.KeyF5})
	frame, _ := m.view()
	if !strings.Contains(ansi.Strip(frame), "No messages yet") {
		t.Error("empty mailbox does not say so")
	}
}

// TestMailboxDump writes a rendered frame for eyeballing: MAIL_DUMP=path.
func TestMailboxDump(t *testing.T) {
	out := os.Getenv("MAIL_DUMP")
	if out == "" {
		t.Skip("set MAIL_DUMP to write a frame")
	}
	m := mailModel(t, 150, 42)
	frame, _ := m.view()
	os.WriteFile(filepath.Join(out, "list.ans"), []byte(frame), 0644)
	for i, e := range m.mail.shown {
		if e.env.ID == "answered1" {
			m.mail.cursor = i
		}
	}
	frame, _ = m.view()
	os.WriteFile(filepath.Join(out, "split.ans"), []byte(frame), 0644)
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	frame, _ = m.view()
	os.WriteFile(filepath.Join(out, "reader.ans"), []byte(frame), 0644)
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	frame, _ = m.view()
	os.WriteFile(filepath.Join(out, "cleanup.ans"), []byte(frame), 0644)
}

// A question and its reply stamped in the same second must still read in
// order: the reply follows what it answers.
func TestConversationPutsReplyAfterItsQuestion(t *testing.T) {
	at := time.Now().UTC().Format(time.RFC3339)
	q := mailEntry{env: mcp.Envelope{Message: mcp.Message{ID: "q", FromSession: "a", ToSession: "b", CreatedAt: at}}}
	r := mailEntry{env: mcp.Envelope{Message: mcp.Message{ID: "r", FromSession: "b", ToSession: "a", CreatedAt: at, InReplyTo: "q"}}}
	for _, input := range [][]mailEntry{{q, r}, {r, q}} {
		shown, _ := groupConversations(input, sortNewest)
		if shown[0].env.ID != "q" || shown[1].env.ID != "r" {
			t.Errorf("order = %s, %s; want the question first", shown[0].env.ID, shown[1].env.ID)
		}
	}
}
