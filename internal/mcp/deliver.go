package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nemke/nagare-go/internal/bin"
	"github.com/nemke/nagare-go/internal/fsutil"
	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/tmux"
)

// Delivery puts a message straight into the recipient's conversation, so the
// recipient never has to spend a turn calling check_messages to learn it has
// mail. One of three paths is taken per message:
//
//  1. A live in-process listener — the pi extension or the OpenCode plugin —
//     watches the pane's push directory and injects the message natively,
//     whether the agent is idle or busy.
//  2. An idle pane gets the message pasted as its next prompt.
//  3. Anything else is queued in the push directory. The agent's own hooks
//     drain it at the next tool call or turn end (Claude Code, Codex), and a
//     detached watcher pastes it once the pane goes idle, for agents without
//     such hooks and for status that went stale.
//
// A queued push is claimed by renaming it, so exactly one consumer delivers it
// however many race for it.

// Push is one queued delivery: the rendered text a recipient sees, plus the
// inbox coordinates needed to mark the stored message read.
type Push struct {
	ID          string `json:"id"`
	FromSession string `json:"from_session"`
	ToSession   string `json:"to_session"`
	Text        string `json:"text"`
}

// listener is what an in-process listener writes to announce itself.
type listener struct {
	PID   int    `json:"pid"`
	Agent string `json:"agent"`
}

func dataDir() string {
	return filepath.Dir(MessagesDir())
}

// paneKey turns a tmux pane id ("%23") into a path component ("23").
func paneKey(paneID string) string {
	return strings.TrimPrefix(paneID, "%")
}

// PushDir returns the directory queued deliveries for a pane are written to.
func PushDir(paneID string) string {
	return filepath.Join(dataDir(), "push", paneKey(paneID))
}

// ListenerPath returns where a pane's in-process listener registers itself.
func ListenerPath(paneID string) string {
	return filepath.Join(dataDir(), "listeners", paneKey(paneID)+".json")
}

// Enqueue queues a push for a pane. File names sort by enqueue time, so a
// drain delivers messages in the order they were sent.
func Enqueue(paneID string, p Push) error {
	if paneID == "" {
		return errors.New("no pane to deliver to")
	}
	dir := PushDir(paneID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d_%s.json", time.Now().UnixNano(), p.ID)
	return fsutil.AtomicWrite(filepath.Join(dir, name), data, 0644)
}

// queuedNames returns a pane's unclaimed pushes, oldest first.
func queuedNames(paneID string) []string {
	entries, err := os.ReadDir(PushDir(paneID))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Pending reports how many pushes are queued for a pane.
func Pending(paneID string) int {
	return len(queuedNames(paneID))
}

// Drain claims every push queued for a pane and marks the stored messages
// read, since the caller is about to put them in front of the agent. It is
// cheap when nothing is queued — one failed ReadDir — because hooks call it
// after every tool use.
func Drain(paneID string) []Push {
	if paneID == "" {
		return nil
	}
	dir := PushDir(paneID)
	var pushes []Push
	for _, name := range queuedNames(paneID) {
		path := filepath.Join(dir, name)
		claimed := path + ".taken"
		if err := os.Rename(path, claimed); err != nil {
			continue // another consumer got there first
		}
		data, err := os.ReadFile(claimed)
		os.Remove(claimed)
		if err != nil {
			continue
		}
		var p Push
		if err := json.Unmarshal(data, &p); err != nil || p.Text == "" {
			continue
		}
		pushes = append(pushes, p)
		markRead(p.ToSession, p.ID)
	}
	return pushes
}

// JoinPushes renders drained pushes as one block of text.
func JoinPushes(pushes []Push) string {
	texts := make([]string, len(pushes))
	for i, p := range pushes {
		texts[i] = p.Text
	}
	return strings.Join(texts, "\n\n---\n\n")
}

// markRead moves a delivered message out of the "new" state so check_messages
// does not repeat what the agent was already shown.
func markRead(toSession, id string) {
	msg, err := ReadMessage(toSession, id)
	if err != nil || (msg.Status != StatusPending && msg.Status != StatusDelivered) {
		return
	}
	msg.Status = StatusRead
	WriteMessage(msg)
}

// ListenerAlive reports whether an in-process listener owns the pane.
func ListenerAlive(paneID string) bool {
	if paneID == "" {
		return false
	}
	data, err := os.ReadFile(ListenerPath(paneID))
	if err != nil {
		return false
	}
	var l listener
	if err := json.Unmarshal(data, &l); err != nil {
		return false
	}
	return processAlive(l.PID)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Seams for tests: delivery must be exercisable without tmux or a detached
// process.
var (
	pasteToPane  = tmuxPaste
	spawnWatcher = startWatcher
)

// deliver routes a message to its recipient and reports what happened, in
// words the sending agent can act on. The message must already be stored.
func deliver(msg Message, target models.Session) (delivered bool, how string) {
	p := Push{ID: msg.ID, FromSession: msg.FromSession, ToSession: msg.ToSession, Text: renderPush(msg)}

	if ListenerAlive(target.PaneID) {
		if err := Enqueue(target.PaneID, p); err == nil {
			return true, "delivered into their conversation"
		}
	}
	if target.Status == models.StatusIdle && target.PaneID != "" {
		if err := pasteToPane(target.PaneID, p.Text); err == nil {
			markRead(msg.ToSession, msg.ID)
			return true, "delivered into their conversation"
		}
	}
	if err := Enqueue(target.PaneID, p); err != nil {
		return false, fmt.Sprintf("stored in their inbox only (%v); they will see it when they call check_messages", err)
	}
	spawnWatcher(target.PaneID)
	return false, fmt.Sprintf("queued because they are %s; it reaches them at their next tool call or as soon as they finish",
		strings.ToLower(models.StatusLabel(target.Status)))
}

// renderPush is the text a recipient sees. It carries everything needed to
// answer, so the recipient can reply in the same turn it reads the message.
func renderPush(m Message) string {
	var b strings.Builder
	if m.InReplyTo != "" {
		fmt.Fprintf(&b, "[nagare] Reply from %s to your message %s:\n\n", m.FromSession, m.InReplyTo)
	} else {
		fmt.Fprintf(&b, "[nagare] Message from %s:\n\n", m.FromSession)
	}
	b.WriteString(strings.TrimSpace(m.Content))
	b.WriteString("\n\n")
	if m.ExpectsReply {
		fmt.Fprintf(&b, "(%s is waiting for your answer. Answer with the nagare reply tool: message_id=%q.)", m.FromSession, m.ID)
	} else {
		fmt.Fprintf(&b, "(No reply needed. To answer, use the nagare reply tool: message_id=%q.)", m.ID)
	}
	return b.String()
}

// tmuxPaste types text into a pane as a bracketed paste, then submits it.
// Bracketed paste keeps a multi-line message in one prompt — sent as keys,
// every newline would submit a fragment. Enter goes separately, after a
// pause, because agent TUIs debounce input and treat an Enter arriving in the
// same burst as a newline inside the prompt.
func tmuxPaste(paneID, text string) error {
	buf := "nagare-" + NewMessageID()
	load := exec.Command("tmux", "load-buffer", "-b", buf, "-")
	load.Stdin = strings.NewReader(text)
	if out, err := load.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux load-buffer: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("tmux", "paste-buffer", "-p", "-d", "-b", buf, "-t", paneID).CombinedOutput(); err != nil {
		exec.Command("tmux", "delete-buffer", "-b", buf).Run()
		return fmt.Errorf("tmux paste-buffer: %v: %s", err, strings.TrimSpace(string(out)))
	}
	time.Sleep(150 * time.Millisecond)
	tmux.RunTmux("send-keys", "-t", paneID, "Enter")
	return nil
}

// startWatcher launches a detached "nagare-go deliver-watch" for the pane.
// It is detached so it outlives a short-lived caller such as the pi bridge.
func startWatcher(paneID string) {
	if paneID == "" {
		return
	}
	cmd := exec.Command(bin.FindSelf(), "deliver-watch", paneID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err == nil {
		cmd.Process.Release()
	}
}

// Watcher timing: how often the pane is checked, how long it must look idle
// before a paste (long enough for a Stop hook to have drained first), and how
// long a watcher waits for a busy agent before giving up.
const (
	watchInterval   = 400 * time.Millisecond
	watchIdleChecks = 2
	watchMaxWait    = 30 * time.Minute
)

// WatchPane waits for a pane to go idle and pastes whatever is still queued
// for it. It exits as soon as nothing is queued, a listener takes over, or the
// pane disappears. At most one watcher runs per pane.
func WatchPane(paneID string) {
	lock := filepath.Join(filepath.Dir(PushDir(paneID)), paneKey(paneID)+".watch")
	if !acquireLock(lock) {
		return
	}
	defer os.Remove(lock)

	deadline := time.Now().Add(watchMaxWait)
	idle := 0
	for time.Now().Before(deadline) {
		time.Sleep(watchInterval)
		if Pending(paneID) == 0 || ListenerAlive(paneID) {
			return
		}
		s, ok := sessionByPane(scan(), paneID)
		if !ok || s.Status == models.StatusDead {
			return // messages stay in the inbox for check_messages
		}
		if s.Status != models.StatusIdle {
			idle = 0
			continue
		}
		if idle++; idle < watchIdleChecks {
			continue
		}
		if pushes := Drain(paneID); len(pushes) > 0 {
			if err := pasteToPane(paneID, JoinPushes(pushes)); err != nil {
				// Put them back; check_messages still finds the stored copies.
				for _, p := range pushes {
					Enqueue(paneID, p)
				}
			}
		}
		return
	}
}

// acquireLock takes a pid lock file, reclaiming it from a dead owner.
func acquireLock(path string) bool {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			fmt.Fprintf(f, "%d", os.Getpid())
			f.Close()
			return true
		}
		data, _ := os.ReadFile(path)
		var pid int
		fmt.Sscanf(string(data), "%d", &pid)
		if processAlive(pid) {
			return false
		}
		os.Remove(path)
	}
	return false
}

func sessionByPane(sessions []models.Session, paneID string) (models.Session, bool) {
	for _, s := range sessions {
		if s.PaneID == paneID {
			return s, true
		}
	}
	return models.Session{}, false
}
