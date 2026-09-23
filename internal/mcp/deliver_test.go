package mcp

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nemke/nagare-go/internal/models"
)

// fakeWorld isolates a test from tmux and the real data directory: sessions
// come from a fixed list, pastes and watcher launches are recorded.
type fakeWorld struct {
	pastes   map[string][]string // pane → texts pasted
	watchers []string
}

func newFakeWorld(t *testing.T, sessions []models.Session) *fakeWorld {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	w := &fakeWorld{pastes: map[string][]string{}}
	oldScan, oldPaste, oldSpawn := scan, pasteToPane, spawnWatcher
	scan = func() []models.Session { return sessions }
	pasteToPane = func(pane, text string) error {
		w.pastes[pane] = append(w.pastes[pane], text)
		return nil
	}
	spawnWatcher = func(pane string) { w.watchers = append(w.watchers, pane) }
	t.Cleanup(func() { scan, pasteToPane, spawnWatcher = oldScan, oldPaste, oldSpawn })
	return w
}

var (
	alice = models.Session{Name: "api", PaneID: "%1", AgentType: models.AgentClaude, Status: models.StatusIdle}
	bob   = models.Session{Name: "web", PaneID: "%2", AgentType: models.AgentCodex, Status: models.StatusIdle}
)

func TestSendToIdleAgentPastesFullMessage(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	t.Setenv("TMUX_PANE", alice.PaneID)

	out := SendMessageHandler("api", SendMessageInput{Target: "web", Message: "run the tests\nand report"})
	if !strings.Contains(out, "delivered") {
		t.Fatalf("result = %q", out)
	}
	pasted := w.pastes[bob.PaneID]
	if len(pasted) != 1 {
		t.Fatalf("pastes to web = %d, want 1", len(pasted))
	}
	// The recipient must get the content itself, not a prompt to go fetch it.
	for _, want := range []string{"[nagare] Message from api", "run the tests\nand report", "message_id="} {
		if !strings.Contains(pasted[0], want) {
			t.Errorf("pasted text missing %q:\n%s", want, pasted[0])
		}
	}
	inbox, _ := ListInbox("web")
	if len(inbox) != 1 || inbox[0].Status != StatusRead {
		t.Errorf("stored message = %+v, want one read message", inbox)
	}
	if len(w.watchers) != 0 {
		t.Errorf("no watcher needed for an idle agent, got %v", w.watchers)
	}
}

func TestSendToBusyAgentQueuesInsteadOfRefusing(t *testing.T) {
	busy := bob
	busy.Status = models.StatusRunning
	w := newFakeWorld(t, []models.Session{alice, busy})

	out := SendMessageHandler("api", SendMessageInput{Target: "web", Message: "hi"})
	if strings.HasPrefix(out, "Error") {
		t.Fatalf("send to a busy agent failed: %q", out)
	}
	if len(w.pastes) != 0 {
		t.Error("typed into a busy pane")
	}
	if Pending(busy.PaneID) != 1 {
		t.Errorf("queued = %d, want 1", Pending(busy.PaneID))
	}
	if len(w.watchers) != 1 || w.watchers[0] != busy.PaneID {
		t.Errorf("watchers = %v, want fallback watcher for %s", w.watchers, busy.PaneID)
	}
}

func TestSendNeverTypesIntoPermissionPrompt(t *testing.T) {
	asking := bob
	asking.Status = models.StatusWaitingInput
	w := newFakeWorld(t, []models.Session{alice, asking})

	SendMessageHandler("api", SendMessageInput{Target: "web", Message: "hi"})
	if len(w.pastes) != 0 {
		t.Error("typed into a pane showing a permission prompt — that would answer it")
	}
	if Pending(asking.PaneID) != 1 {
		t.Error("message not queued")
	}
}

func TestSendToListenerSkipsPaste(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	os.MkdirAll(dataDir()+"/listeners", 0755)
	os.WriteFile(ListenerPath(bob.PaneID), []byte(`{"pid":`+strconv.Itoa(os.Getpid())+`,"agent":"pi"}`), 0644)

	SendMessageHandler("api", SendMessageInput{Target: "web", Message: "hi"})
	if len(w.pastes) != 0 {
		t.Error("pasted into a pane whose listener injects natively")
	}
	if Pending(bob.PaneID) != 1 {
		t.Error("message not handed to the listener's push directory")
	}
}

func TestDeadListenerIsIgnored(t *testing.T) {
	newFakeWorld(t, nil)
	os.MkdirAll(dataDir()+"/listeners", 0755)
	os.WriteFile(ListenerPath("%9"), []byte(`{"pid":999999999}`), 0644)
	if ListenerAlive("%9") {
		t.Error("a listener whose process is gone must not count")
	}
}

func TestUnknownTargetListsAgents(t *testing.T) {
	newFakeWorld(t, []models.Session{alice, bob})
	out := SendMessageHandler("api", SendMessageInput{Target: "nope", Message: "hi"})
	// Listing the agents in the error saves the sender a list_agents round trip.
	if !strings.Contains(out, "not found") || !strings.Contains(out, "- web") {
		t.Errorf("error should list available agents, got %q", out)
	}
}

func TestDrainClaimsEachPushOnce(t *testing.T) {
	newFakeWorld(t, nil)
	for _, id := range []string{"a", "b", "c"} {
		if err := Enqueue("%5", Push{ID: id, Text: "text " + id}); err != nil {
			t.Fatal(err)
		}
	}
	got := Drain("%5")
	if len(got) != 3 || got[0].ID != "a" || got[2].ID != "c" {
		t.Fatalf("drained %+v, want a, b, c in order", got)
	}
	if again := Drain("%5"); len(again) != 0 {
		t.Errorf("second drain returned %d pushes, want 0", len(again))
	}
}

func TestReplyIsPushedBackToSender(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	t.Setenv("TMUX_PANE", alice.PaneID)
	SendMessageHandler("api", SendMessageInput{Target: "web", Message: "what port?"})
	inbox, _ := ListInbox("web")

	t.Setenv("TMUX_PANE", bob.PaneID)
	out := ReplyHandler("web", ReplyInput{MessageID: inbox[0].ID, Content: "8080"})
	if !strings.Contains(out, "delivered") {
		t.Fatalf("reply result = %q", out)
	}
	pasted := w.pastes[alice.PaneID]
	if len(pasted) != 1 || !strings.Contains(pasted[0], "8080") || !strings.Contains(pasted[0], "Reply from web") {
		t.Fatalf("sender did not receive the reply: %v", pasted)
	}

	// The pushed reply is itself a message, so the conversation can go on.
	back, _ := ListInbox("api")
	if len(back) != 1 || back[0].InReplyTo != inbox[0].ID {
		t.Fatalf("sender inbox = %+v", back)
	}
	t.Setenv("TMUX_PANE", alice.PaneID)
	ReplyHandler("api", ReplyInput{MessageID: back[0].ID, Content: "thanks"})
	if len(w.pastes[bob.PaneID]) != 2 {
		t.Errorf("reply to a reply did not reach web: %v", w.pastes[bob.PaneID])
	}
}

func TestSendAndWaitReturnsReplyPromptly(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	t.Setenv("TMUX_PANE", alice.PaneID)

	go func() {
		for {
			time.Sleep(20 * time.Millisecond)
			inbox, _ := ListInbox("web")
			if len(inbox) == 1 {
				ReplyHandler("web", ReplyInput{MessageID: inbox[0].ID, Content: "42"})
				return
			}
		}
	}()

	start := time.Now()
	out := SendMessageAndWaitHandler(context.Background(), "api",
		SendMessageAndWaitInput{Target: "web", Message: "answer?", Timeout: 5})
	if !strings.Contains(out, "42") {
		t.Fatalf("result = %q", out)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("reply took %v to collect; polling is too slow", elapsed)
	}
	// A waiting sender collects the answer itself; pushing it too would
	// deliver it twice.
	if len(w.pastes[alice.PaneID]) != 0 {
		t.Errorf("reply was also pushed to a sender that was waiting for it")
	}
}

func TestRenderPushAsksForReplyWhenExpected(t *testing.T) {
	text := renderPush(Message{ID: "abc", FromSession: "api", Content: "q?", ExpectsReply: true})
	if !strings.Contains(text, "waiting for your answer") || !strings.Contains(text, `message_id="abc"`) {
		t.Errorf("render = %q", text)
	}
}

// tmux renaming a window changes an agent's display name between the send and
// the reply; the reply must still find the message.
func TestReplySurvivesRename(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	t.Setenv("TMUX_PANE", alice.PaneID)
	SendMessageHandler("api", SendMessageInput{Target: "web", Message: "q"})
	inbox, _ := ListInbox("web")

	renamed := bob
	renamed.Name = "web/codex"
	scan = func() []models.Session { return []models.Session{alice, renamed} }
	t.Setenv("TMUX_PANE", bob.PaneID)
	if out := ReplyHandler("web/codex", ReplyInput{MessageID: inbox[0].ID, Content: "a"}); strings.HasPrefix(out, "Error") {
		t.Fatalf("reply after rename failed: %q", out)
	}
	if len(w.pastes[alice.PaneID]) != 1 {
		t.Error("reply did not reach the sender")
	}
}
