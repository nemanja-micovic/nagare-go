package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nemke/nagare-go/internal/models"
)

// These tests follow a message down every delivery path, from send to answer,
// and check at each step the state the mailbox will show. The failure they
// guard against is quiet: an agent answers, and the mailbox still says the
// message was never answered — or never read.

// stateOf is what the mailbox would show for a message right now.
func stateOf(t *testing.T, id string) State {
	t.Helper()
	envs, err := AllMessages()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range envs {
		if e.ID == id {
			return e.State(time.Now())
		}
	}
	t.Fatalf("message %s not stored", id)
	return 0
}

var stateNames = map[State]string{
	StateQueued: "queued", StateLost: "lost", StateNeedsReply: "needs reply",
	StateDelivered: "delivered", StateAnswered: "answered",
}

func expectState(t *testing.T, id string, want State, when string) {
	t.Helper()
	if got := stateOf(t, id); got != want {
		t.Errorf("%s: mailbox shows %s, want %s", when, stateNames[got], stateNames[want])
	}
}

// ask sends a question from api (alice) to target and returns its id.
func ask(t *testing.T, target models.Session, expectsReply bool) string {
	t.Helper()
	t.Setenv("TMUX_PANE", alice.PaneID)
	askCount++
	// Distinct text, or the duplicate guard would drop repeat questions.
	msg, resolved, errText := prepare("api", target.Name, fmt.Sprintf("what port? #%d", askCount), expectsReply)
	if errText != "" {
		t.Fatal(errText)
	}
	deliver(msg, resolved)
	return msg.ID
}

var askCount int

// registerListener makes a pane look owned by a live in-process listener, as
// the pi and OpenCode integrations do.
func registerListener(t *testing.T, pane string) {
	t.Helper()
	path := ListenerPath(pane)
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"pid":%d}`, os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}
}

// hookDrain is what hook-state does at PostToolUse, UserPromptSubmit or Stop.
func hookDrain(pane string) []Push {
	pushes := Drain(pane)
	Owe(pane, OwedIDs(pushes)...)
	MarkRead(pushes)
	return pushes
}

func busy(s models.Session) models.Session {
	s.Status = models.StatusRunning
	return s
}

func TestLifecycleIdlePaste(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	id := ask(t, bob, true)
	expectState(t, id, StateNeedsReply, "pasted into an idle pane")

	SettleOwed(bob.PaneID, "8080")
	expectState(t, id, StateAnswered, "after the recipient's turn ended")
	if len(w.pastes[alice.PaneID]) != 1 {
		t.Fatal("the answer never reached the sender")
	}
	// The pushed-back answer is a message of its own, and it was delivered.
	for _, e := range mustAll(t) {
		if e.InReplyTo == id && e.State(time.Now()) != StateDelivered {
			t.Errorf("pushed-back reply shows %s", stateNames[e.State(time.Now())])
		}
	}
}

func TestLifecycleNoteNeedsNoAnswer(t *testing.T) {
	newFakeWorld(t, []models.Session{alice, bob})
	id := ask(t, bob, false)
	expectState(t, id, StateDelivered, "a note pasted into an idle pane")
	SettleOwed(bob.PaneID, "ok")
	expectState(t, id, StateDelivered, "a note is not answered by a final message")
}

func TestLifecycleBusyThenHookDrain(t *testing.T) {
	newFakeWorld(t, []models.Session{alice, busy(bob)})
	id := ask(t, busy(bob), true)
	expectState(t, id, StateQueued, "sent to a busy agent")

	if len(hookDrain(bob.PaneID)) != 1 {
		t.Fatal("hook drained nothing")
	}
	expectState(t, id, StateNeedsReply, "drained by the recipient's hook")
	SettleOwed(bob.PaneID, "8080")
	expectState(t, id, StateAnswered, "after the turn the hook delivered into")
}

// The watcher delivered a question without recording that it was owed an
// answer, so the recipient's answer was never sent back and the mailbox said
// "needs reply" forever.
func TestLifecycleBusyThenWatcher(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, busy(bob)})
	id := ask(t, busy(bob), true)
	scan = func() []models.Session { return []models.Session{alice, bob} } // now idle
	WatchPane(bob.PaneID)
	if len(w.pastes[bob.PaneID]) != 1 {
		t.Fatal("watcher did not paste the queued message")
	}
	expectState(t, id, StateNeedsReply, "pasted by the watcher")
	SettleOwed(bob.PaneID, "8080")
	expectState(t, id, StateAnswered, "after the turn the watcher started")
}

func TestLifecycleWatcherPasteFails(t *testing.T) {
	newFakeWorld(t, []models.Session{alice, busy(bob)})
	id := ask(t, busy(bob), true)
	scan = func() []models.Session { return []models.Session{alice, bob} }
	pasteToPane = func(string, string) error { return errors.New("pane gone") }
	WatchPane(bob.PaneID)
	// Not delivered, so it must not look delivered.
	expectState(t, id, StateQueued, "after a failed paste")
	if Pending(bob.PaneID) != 1 {
		t.Error("the undelivered message was not put back in the queue")
	}
}

func TestLifecycleListener(t *testing.T) {
	carol := models.Session{Name: "docs", PaneID: "%3", AgentType: models.AgentPi, Status: models.StatusRunning}
	w := newFakeWorld(t, []models.Session{alice, carol})
	registerListener(t, carol.PaneID)
	id := ask(t, carol, true)
	expectState(t, id, StateNeedsReply, "handed to pi's listener")

	Drain(carol.PaneID) // the extension claims the push; it records nothing
	expectState(t, id, StateNeedsReply, "after the listener injected it")
	SettleOwed(carol.PaneID, "8080")
	expectState(t, id, StateAnswered, "after pi's run settled")
	if len(w.pastes[alice.PaneID]) != 1 {
		t.Error("the answer never reached the sender")
	}
}

func TestLifecycleClaudeInbox(t *testing.T) {
	target := busy(alice)
	target.Name, target.PaneID = "claude-api", "%5"
	newFakeWorld(t, []models.Session{bob, target})
	seedClaudeState(t, target.PaneID, "/tmp/x.sock", "default")
	postToInbox = func(string, string) error { return nil }

	t.Setenv("TMUX_PANE", bob.PaneID)
	msg, resolved, _ := prepare("web", "claude-api", "q?", true)
	deliver(msg, resolved)
	expectState(t, msg.ID, StateNeedsReply, "posted to Claude's inbox")
	SettleOwed(target.PaneID, "answer")
	expectState(t, msg.ID, StateAnswered, "after Claude's Stop hook")
}

func TestLifecycleCheckMessagesThenReply(t *testing.T) {
	newFakeWorld(t, []models.Session{alice, busy(bob)})
	id := ask(t, busy(bob), true)
	t.Setenv("TMUX_PANE", bob.PaneID)
	if out := CheckMessagesHandler("web"); !strings.Contains(out, "what port?") {
		t.Fatalf("check_messages = %q", out)
	}
	expectState(t, id, StateNeedsReply, "read with check_messages")
	if Pending(bob.PaneID) != 0 {
		t.Error("a message read with check_messages is still queued to be injected again")
	}
	ReplyHandler("web", ReplyInput{MessageID: id, Content: "8080"})
	expectState(t, id, StateAnswered, "after reply()")
}

func TestLifecycleUserPromptThenExplicitReply(t *testing.T) {
	newFakeWorld(t, []models.Session{alice, bob})
	id := ask(t, bob, true)
	ForgetOwed(bob.PaneID) // the user typed something to the recipient
	SettleOwed(bob.PaneID, "an answer to the user")
	// Nothing answered the message, and the mailbox must not claim otherwise.
	expectState(t, id, StateNeedsReply, "after the user's prompt took the turn")
	ReplyHandler("web", ReplyInput{MessageID: id, Content: "8080"})
	expectState(t, id, StateAnswered, "after an explicit reply")
}

func TestLifecycleSendAndWait(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	t.Setenv("TMUX_PANE", alice.PaneID)
	go func() {
		for i := 0; i < 100 && SettleOwed(bob.PaneID, "8080") == 0; i++ {
			time.Sleep(10 * time.Millisecond)
		}
	}()
	out := SendMessageAndWaitHandler(context.Background(), "api",
		SendMessageAndWaitInput{Target: "web", Message: "port?", Timeout: 5})
	if !strings.Contains(out, "8080") {
		t.Fatalf("wait returned %q", out)
	}
	for _, e := range mustAll(t) {
		if e.InReplyTo == "" && e.State(time.Now()) != StateAnswered {
			t.Errorf("the waited-on message shows %s", stateNames[e.State(time.Now())])
		}
	}
	if len(w.pastes[alice.PaneID]) != 0 {
		t.Error("a waiting sender was also sent the answer as a message")
	}
}

func TestLifecycleLateDeliveryReportCannotUnanswer(t *testing.T) {
	newFakeWorld(t, []models.Session{alice, bob})
	id := ask(t, bob, true)
	ReplyHandler("web", ReplyInput{MessageID: id, Content: "8080"})
	setStatus("web", id, StatusDelivered)
	markRead("web", id)
	expectState(t, id, StateAnswered, "after a late delivery report")
	if got, _ := FindMessage(id); got.Response == nil || *got.Response != "8080" {
		t.Error("a late delivery report dropped the answer")
	}
}

// Deliveries, check_messages and replies run in different processes and can
// touch one message at once. Before updates were locked, a delivery report
// that read the file just before a reply was written would save over it.
func TestConcurrentUpdatesNeverLoseTheAnswer(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	for round := 0; round < 30; round++ {
		id := ask(t, busy(bob), true)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(2)
			go func() { defer wg.Done(); setStatus("web", id, StatusDelivered) }()
			go func() { defer wg.Done(); markRead("web", id) }()
		}
		wg.Add(2)
		go func() { defer wg.Done(); ReplyHandler("web", ReplyInput{MessageID: id, Content: "explicit"}) }()
		go func() { defer wg.Done(); SettleOwed(bob.PaneID, "automatic") }()
		wg.Wait()

		got, _ := FindMessage(id)
		if got.Status != StatusCompleted || got.Response == nil {
			t.Fatalf("round %d: status %s, response %v — the answer was lost", round, got.Status, got.Response)
		}
	}
	// Explicit and automatic replies raced every round; each question must
	// have produced exactly one answer to the sender.
	if n := len(w.pastes[alice.PaneID]); n != 30 {
		t.Errorf("sender received %d answers to 30 questions", n)
	}
}

func mustAll(t *testing.T) []Envelope {
	t.Helper()
	envs, err := AllMessages()
	if err != nil {
		t.Fatal(err)
	}
	return envs
}

// The whatsapp flow: ask without blocking, keep working, get the answer
// pushed back when the other agent finishes.
func TestLifecycleNonBlockingQuestion(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, busy(bob)})
	t.Setenv("TMUX_PANE", alice.PaneID)
	out := SendMessageHandler("api", SendMessageInput{Target: "web", Message: "which port?", ExpectsReply: true})
	id := strings.Fields(out[strings.Index(out, "(message ")+len("(message "):])[0]
	id = strings.TrimSuffix(id, "):")
	expectState(t, id, StateQueued, "question to a busy agent")
	hookDrain(bob.PaneID)
	expectState(t, id, StateNeedsReply, "delivered mid-turn")
	SettleOwed(bob.PaneID, "It listens on 8080.")
	expectState(t, id, StateAnswered, "after the recipient finished")
	if p := w.pastes[alice.PaneID]; len(p) != 1 || !strings.Contains(p[0], "8080") {
		t.Errorf("the answer was not pushed to the sender: %v", p)
	}
}
