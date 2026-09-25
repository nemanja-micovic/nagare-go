package mcp

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/state"
)

func TestFinalMessageIsSentBackAsTheReply(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	t.Setenv("TMUX_PANE", alice.PaneID)
	SendMessageHandler("api", SendMessageInput{Target: "web", Message: "q"})
	// A fire-and-forget message owes nothing.
	if SettleOwed(bob.PaneID, "whatever") != 0 {
		t.Fatal("a message that asked for no reply was answered")
	}

	msg, _, errText := prepare("api", "web", "what port?", true)
	if errText != "" {
		t.Fatal(errText)
	}
	deliver(msg, bob)
	// bob (Codex) was told it can just answer.
	if last := w.pastes[bob.PaneID][len(w.pastes[bob.PaneID])-1]; !strings.Contains(last, "Just answer") {
		t.Errorf("auto-reply agent not told it can just answer:\n%s", last)
	}

	if n := SettleOwed(bob.PaneID, "  8080  "); n != 1 {
		t.Fatalf("settled %d, want 1", n)
	}
	got, _ := FindMessage(msg.ID)
	if got.Status != StatusCompleted || got.Response == nil || *got.Response != "8080" || !got.AutoReply {
		t.Fatalf("message after settle = %+v", got)
	}
	pasted := w.pastes[alice.PaneID]
	if len(pasted) != 1 || !strings.Contains(pasted[0], "8080") {
		t.Errorf("sender did not get the automatic reply: %v", pasted)
	}
	// Settled debts are gone: the next turn's final message answers nothing.
	if SettleOwed(bob.PaneID, "later") != 0 {
		t.Error("a debt was settled twice")
	}
}

func TestExplicitReplyWinsOverAutomaticOne(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	t.Setenv("TMUX_PANE", alice.PaneID)
	msg, _, _ := prepare("api", "web", "q?", true)
	deliver(msg, bob)
	ReplyHandler("web", ReplyInput{MessageID: msg.ID, Content: "explicit"})
	if SettleOwed(bob.PaneID, "final message") != 0 {
		t.Error("an answered message was answered again from the final message")
	}
	got, _ := FindMessage(msg.ID)
	if *got.Response != "explicit" {
		t.Errorf("response = %q", *got.Response)
	}
	if len(w.pastes[alice.PaneID]) != 1 {
		t.Errorf("sender got %d replies, want 1", len(w.pastes[alice.PaneID]))
	}
}

func TestUserPromptCancelsTheDebt(t *testing.T) {
	newFakeWorld(t, []models.Session{alice, bob})
	msg, _, _ := prepare("api", "web", "q?", true)
	deliver(msg, bob)
	ForgetOwed(bob.PaneID)
	if SettleOwed(bob.PaneID, "answer to the user") != 0 {
		t.Error("the answer to the user's own prompt was sent to another agent")
	}
	if !IsNagarePrompt("  [nagare] Message from api:") || !IsNagarePrompt("<peer>\n[nagare] Reply from web to") ||
		IsNagarePrompt("fix the bug") {
		t.Error("IsNagarePrompt misclassifies")
	}
}

func TestNonAutoAgentIsPointedAtReplyTool(t *testing.T) {
	text := renderPush(Message{ID: "x", FromSession: "api", ExpectsReply: true, Content: "q"}, false)
	if strings.Contains(text, "Just answer") || !strings.Contains(text, "reply tool") {
		t.Errorf("render = %q", text)
	}
}

// seedClaudeState records a Claude pane's inbox socket the way hook-state
// does.
func seedClaudeState(t *testing.T, pane, sock, mode string) {
	t.Helper()
	if err := state.WriteState(state.DefaultStatesDir(), models.SessionState{
		State: "idle", SessionID: "s-" + pane, PaneID: pane, MessagingSocket: sock, PermissionMode: mode,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeTargetGetsInboxSocketNotPaste(t *testing.T) {
	busy := alice
	busy.Status = models.StatusRunning
	w := newFakeWorld(t, []models.Session{busy, bob})
	seedClaudeState(t, busy.PaneID, "/tmp/x.sock", "default")
	var posted []string
	postToInbox = func(sock, text string) error { posted = append(posted, sock+"|"+text); return nil }

	out := SendMessageHandler("web", SendMessageInput{Target: "api", Message: "hello"})
	if !strings.Contains(out, "delivered") {
		t.Fatalf("result = %q", out)
	}
	// Busy or not, the inbox takes it: no paste, no queue, no watcher.
	if len(posted) != 1 || !strings.HasPrefix(posted[0], "/tmp/x.sock|[nagare]") {
		t.Fatalf("posted = %v", posted)
	}
	if len(w.pastes) != 0 || len(w.watchers) != 0 || Pending(busy.PaneID) != 0 {
		t.Error("fell back to paste or queue despite a live inbox")
	}
}

func TestBypassModeClaudeKeepsPastePath(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	seedClaudeState(t, alice.PaneID, "/tmp/x.sock", "bypassPermissions")
	postToInbox = func(string, string) error { t.Error("posted to a session that would hold it"); return nil }
	SendMessageHandler("web", SendMessageInput{Target: "api", Message: "hello"})
	if len(w.pastes[alice.PaneID]) != 1 {
		t.Error("bypass-mode session did not get the paste")
	}
}

func TestDeadInboxFallsBack(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	seedClaudeState(t, alice.PaneID, filepath.Join(t.TempDir(), "gone.sock"), "default")
	postToInbox = udsPost // the real one, against a socket nobody listens on
	SendMessageHandler("web", SendMessageInput{Target: "api", Message: "hello"})
	if len(w.pastes[alice.PaneID]) != 1 {
		t.Error("a dead inbox lost the message instead of falling back to paste")
	}
}

// The line format is what Claude Code's reader accepts; get it wrong and
// messages vanish without an error, so it is pinned here.
func TestUDSPostWireFormat(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "in.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		line, _ := bufio.NewReader(conn).ReadString('\n')
		got <- line
		conn.Close()
	}()
	if err := udsPost(sock, "hi\nthere"); err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Type    string `json:"type"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Priority string `json:"priority"`
	}
	line := <-got
	if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("not exactly one line: %q", line)
	}
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "user" || msg.Message.Content != "hi\nthere" || msg.Priority != "next" {
		t.Errorf("wire message = %+v", msg)
	}
}

func TestSendToSeveralAndToAll(t *testing.T) {
	carol := models.Session{Name: "docs", PaneID: "%3", AgentType: models.AgentPi, Status: models.StatusIdle}
	gone := models.Session{Name: "old", PaneID: "%4", AgentType: models.AgentClaude, Status: models.StatusDead}
	w := newFakeWorld(t, []models.Session{alice, bob, carol, gone})
	t.Setenv("TMUX_PANE", alice.PaneID)

	out := SendMessageHandler("api", SendMessageInput{Target: "web, docs", Message: "schema changed"})
	if strings.Count(out, "Sent to") != 2 {
		t.Fatalf("comma targets: %q", out)
	}
	out = SendMessageHandler("api", SendMessageInput{Target: "all", Message: "release in 5 min"})
	// Everyone but the sender and the dead.
	if strings.Count(out, "Sent to") != 2 || strings.Contains(out, "old") {
		t.Fatalf("broadcast: %q", out)
	}
	if len(w.pastes[bob.PaneID]) != 2 || len(w.pastes[carol.PaneID]) != 2 || len(w.pastes[alice.PaneID]) != 0 {
		t.Errorf("pastes = %v", w.pastes)
	}
	if _, _, errText := prepare("api", "web, docs", "q?", true); !strings.Contains(errText, "one target") {
		t.Errorf("waiting on several targets should be refused, got %q", errText)
	}
}

func TestIdenticalResendIsDropped(t *testing.T) {
	w := newFakeWorld(t, []models.Session{alice, bob})
	SendMessageHandler("api", SendMessageInput{Target: "web", Message: "ping"})
	out := SendMessageHandler("api", SendMessageInput{Target: "web", Message: "ping"})
	if !strings.Contains(out, "not sent again") {
		t.Errorf("resend result = %q", out)
	}
	if len(w.pastes[bob.PaneID]) != 1 {
		t.Errorf("recipient got the same message %d times", len(w.pastes[bob.PaneID]))
	}
	// Different text is a different message.
	SendMessageHandler("api", SendMessageInput{Target: "web", Message: "pong"})
	if len(w.pastes[bob.PaneID]) != 2 {
		t.Error("a new message was dropped as a duplicate")
	}
}
