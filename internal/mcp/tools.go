package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/state"
	"github.com/nemke/nagare-go/internal/tmux"
)

// scanAll returns all agent sessions from tmux.
func scanAll() []models.Session {
	dir := state.DefaultStatesDir()
	return tmux.ScanSessions(state.LoadStatesByPaneID(dir), state.LoadAllStates(dir))
}

// scan is scanAll behind a short-lived cache. One tool call needs the scan
// twice — to resolve the caller and the target — and a scan spawns tmux and
// git, so sharing it keeps a send fast. The TTL is below the delivery
// watcher's poll interval, so the watcher always sees fresh status.
var scan = cachedScan

const scanTTL = 250 * time.Millisecond

var scanCache struct {
	sync.Mutex
	at       time.Time
	sessions []models.Session
}

func cachedScan() []models.Session {
	scanCache.Lock()
	defer scanCache.Unlock()
	if time.Since(scanCache.at) < scanTTL {
		return scanCache.sessions
	}
	scanCache.sessions = scanAll()
	scanCache.at = time.Now()
	return scanCache.sessions
}

// ListAgentsHandler scans tmux for agent sessions and returns formatted list.
func ListAgentsHandler(mySession string) string {
	return roster(scan(), mySession)
}

const noAgents = "No other agents found."

// roster lists every agent except the caller, one per line.
func roster(sessions []models.Session, mySession string) string {
	var lines []string
	for _, s := range sessions {
		if s.Name == mySession {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s (%s) [%s] %s",
			s.Name, models.AgentLabel(s.AgentType),
			models.StatusLabel(s.Status), s.Path))
	}
	if len(lines) == 0 {
		return noAgents
	}
	return strings.Join(lines, "\n")
}

// SendMessageInput is the input for send_message tool.
type SendMessageInput struct {
	Target  string `json:"target" jsonschema:"who to message: a session name, or a repo, worktree, or agent type (claude, codex, pi, opencode...) if that is unique"`
	Message string `json:"message" jsonschema:"message to send"`
}

// SendMessageHandler stores a message and pushes it into the target's
// conversation. The target may be busy: the message is then queued and
// delivered at its next tool call or turn end.
func SendMessageHandler(mySession string, input SendMessageInput) string {
	msg, target, errText := prepare(mySession, input.Target, input.Message, false)
	if errText != "" {
		return errText
	}
	_, how := deliver(msg, target)
	return fmt.Sprintf("Sent to %s (message %s): %s. Their reply, if any, will arrive in your conversation automatically.",
		target.Name, msg.ID, how)
}

// SendMessageAndWaitInput is the input for send_message_and_wait tool.
type SendMessageAndWaitInput struct {
	Target  string `json:"target" jsonschema:"who to message: a session name, or a repo, worktree, or agent type (claude, codex, pi, opencode...) if that is unique"`
	Message string `json:"message" jsonschema:"message to send"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"timeout in seconds (default 300)"`
}

// waitPoll is how often a waiting sender checks for the answer. Reading one
// small file this often costs nothing measurable and keeps a reply's latency
// down to the recipient's own speed.
const waitPoll = 100 * time.Millisecond

// SendMessageAndWaitHandler sends a message and blocks until it is answered.
func SendMessageAndWaitHandler(ctx context.Context, mySession string, input SendMessageAndWaitInput) string {
	timeout := input.Timeout
	if timeout <= 0 {
		timeout = 300
	}

	msg, target, errText := prepare(mySession, input.Target, input.Message, true)
	if errText != "" {
		return errText
	}

	// Mark the wait before delivering, so a recipient that answers instantly
	// already sees that its answer will be collected here.
	marker := waitPath(msg.ToSession, msg.ID)
	os.WriteFile(marker, []byte(time.Now().Add(time.Duration(timeout)*time.Second).UTC().Format(time.RFC3339)), 0644)
	_, how := deliver(msg, target)

	response := func() (string, bool) {
		updated, err := ReadMessage(msg.ToSession, msg.ID)
		if err == nil && updated.Status == StatusCompleted && updated.Response != nil {
			return *updated.Response, true
		}
		return "", false
	}

	ticker := time.NewTicker(waitPoll)
	defer ticker.Stop()
	deadline := time.After(time.Duration(timeout) * time.Second)
	for {
		select {
		case <-ctx.Done():
			os.Remove(marker)
			return "Cancelled: client disconnected."
		case <-deadline:
			// Remove the marker first, then look once more: a reply that saw
			// the marker was written before it and is caught here; one that
			// did not is pushed to this conversation instead.
			os.Remove(marker)
			if r, ok := response(); ok {
				return fmt.Sprintf("Reply from %s:\n%s", target.Name, r)
			}
			return fmt.Sprintf("No reply from %s within %d seconds (message was %s). Their reply will still arrive in your conversation when they send it.",
				target.Name, timeout, how)
		case <-ticker.C:
			if r, ok := response(); ok {
				os.Remove(marker)
				return fmt.Sprintf("Reply from %s:\n%s", target.Name, r)
			}
		}
	}
}

// prepare resolves the target and stores the message. On failure it returns
// the text to hand back to the calling agent — including the roster when the
// target was not found, so the agent can retry without calling list_agents.
func prepare(mySession, target, content string, expectsReply bool) (Message, models.Session, string) {
	if strings.TrimSpace(content) == "" {
		return Message{}, models.Session{}, "Error: message is empty."
	}
	sessions := scan()
	var others []models.Session
	for _, s := range sessions {
		if s.Name != mySession {
			others = append(others, s)
		}
	}
	session, err := resolveSession(target, others)
	if err != nil {
		return Message{}, models.Session{}, fmt.Sprintf("Error: %v\nAgents you can message:\n%s", err, roster(sessions, mySession))
	}
	if session.Status == models.StatusDead {
		return Message{}, models.Session{}, fmt.Sprintf("Error: %s has exited.", session.Name)
	}
	msg := Message{
		ID:           NewMessageID(),
		FromSession:  mySession,
		FromPane:     os.Getenv("TMUX_PANE"),
		ToSession:    session.Name,
		Content:      content,
		ExpectsReply: expectsReply,
		Status:       StatusPending,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if err := WriteMessage(msg); err != nil {
		return Message{}, models.Session{}, fmt.Sprintf("Error writing message: %v", err)
	}
	return msg, session, ""
}

// CheckMessagesHandler returns pending incoming messages + completed outgoing responses.
func CheckMessagesHandler(mySession string) string {
	// Anything still queued for push is about to be shown here, so claim it
	// rather than have it injected a second time.
	Drain(os.Getenv("TMUX_PANE"))

	var parts []string

	// Incoming messages (my inbox)
	inbox, _ := ListInbox(mySession)

	// Unread: pending or delivered (not yet seen)
	var unread []Message
	for _, m := range inbox {
		if m.Status == StatusPending || m.Status == StatusDelivered {
			unread = append(unread, m)
		}
	}
	if len(unread) > 0 {
		parts = append(parts, fmt.Sprintf("=== New Messages (%d) ===", len(unread)))
		for i, m := range unread {
			actionNote := "[INFORMATIONAL — no reply needed]"
			if m.ExpectsReply {
				actionNote = fmt.Sprintf("[REPLY NEEDED — use reply('%s', 'your response')]", m.ID)
			}
			parts = append(parts, fmt.Sprintf("From: %s (sent %s)\n%s\nMessage ID: %s\n%s",
				m.FromSession, m.CreatedAt, actionNote, m.ID, m.Content))

			// Mark as read
			unread[i].Status = StatusRead
			WriteMessage(unread[i])
		}
	}

	// Awaiting reply: read but still needs a response
	var awaitingReply []Message
	for _, m := range inbox {
		if m.Status == StatusRead && m.ExpectsReply {
			awaitingReply = append(awaitingReply, m)
		}
	}
	if len(awaitingReply) > 0 {
		parts = append(parts, fmt.Sprintf("=== Awaiting Your Reply (%d) ===", len(awaitingReply)))
		for _, m := range awaitingReply {
			parts = append(parts, fmt.Sprintf("From: %s (sent %s) [REPLY NEEDED - use reply('%s', 'your response')]\nMessage ID: %s\n%s",
				m.FromSession, m.CreatedAt, m.ID, m.ID, m.Content))
		}
	}

	// Completed outgoing responses (scan other inboxes for messages FROM me)
	baseDir := MessagesDir()
	dirs, _ := os.ReadDir(baseDir)
	var responses []Message
	for _, d := range dirs {
		if !d.IsDir() || d.Name() == sanitizeName(mySession) {
			continue
		}
		msgs, _ := ListInbox(d.Name())
		for _, m := range msgs {
			if m.FromSession == mySession && m.Status == StatusCompleted && m.Response != nil {
				responses = append(responses, m)
			}
		}
	}
	if len(responses) > 0 {
		parts = append(parts, "=== Responses to Your Messages ===")
		for _, m := range responses {
			parts = append(parts, fmt.Sprintf("Response from %s:\n%s", m.ToSession, *m.Response))
		}
	}

	if len(parts) == 0 {
		return "No pending messages or responses."
	}
	return strings.Join(parts, "\n\n")
}

// ReplyInput is the input for reply tool.
type ReplyInput struct {
	MessageID string `json:"message_id" jsonschema:"ID of the message to reply to"`
	Content   string `json:"content" jsonschema:"reply content"`
}

// ReplyHandler answers a message. A sender blocked in send_message_and_wait
// collects the answer itself; any other sender has the answer pushed into its
// conversation as a new message, which it can reply to in turn — so two
// agents can hold a conversation without either one polling.
func ReplyHandler(mySession string, input ReplyInput) string {
	if strings.TrimSpace(input.Content) == "" {
		return "Error: reply is empty."
	}
	found, err := FindMessage(strings.TrimSpace(input.MessageID))
	if err != nil {
		return fmt.Sprintf("Error: message %s not found.", input.MessageID)
	}
	orig := &found

	now := time.Now().UTC().Format(time.RFC3339)
	orig.Status = StatusCompleted
	orig.Response = &input.Content
	orig.RespondedAt = &now
	if err := WriteMessage(*orig); err != nil {
		return fmt.Sprintf("Error saving reply: %v", err)
	}

	// Written before this check, so a waiter that removes its marker after
	// the check still finds the response on its final read.
	if _, err := os.Stat(waitPath(orig.ToSession, orig.ID)); err == nil {
		return fmt.Sprintf("Reply sent to %s.", orig.FromSession)
	}

	sender, ok := findSender(scan(), *orig)
	if !ok {
		return fmt.Sprintf("Reply saved for %s, who is no longer running; they will see it via check_messages.", orig.FromSession)
	}
	back := Message{
		ID:          NewMessageID(),
		FromSession: mySession,
		FromPane:    os.Getenv("TMUX_PANE"),
		ToSession:   sender.Name,
		Content:     input.Content,
		InReplyTo:   orig.ID,
		Status:      StatusPending,
		CreatedAt:   now,
	}
	if err := WriteMessage(back); err != nil {
		return fmt.Sprintf("Reply saved, but could not be pushed to %s: %v", sender.Name, err)
	}
	_, how := deliver(back, sender)
	return fmt.Sprintf("Reply sent to %s: %s.", sender.Name, how)
}

// findSender locates the session that sent a message: by pane when it was
// recorded, since display names can change while an agent runs, else by name.
func findSender(sessions []models.Session, m Message) (models.Session, bool) {
	if m.FromPane != "" {
		if s, ok := sessionByPane(sessions, m.FromPane); ok {
			return s, true
		}
	}
	for _, s := range sessions {
		if s.Name == m.FromSession {
			return s, true
		}
	}
	return models.Session{}, false
}

// Helper functions

// paneTargetFor builds a tmux pane target for a discovered session. It uses
// SessionName (the real tmux session) rather than Name (the display name,
// which can contain "/" for multi-pane disambiguation and is not a valid
// tmux target).
func paneTargetFor(s models.Session) string {
	return tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex)
}

// resolveSession finds a session by name, from strictest match to loosest,
// so an agent can name its target the way a person would. The first tier
// with any match decides:
//
//  1. exact display name
//  2. display name, ignoring case
//  3. "{name}/..." — one agent among a repo's panes
//  4. repo, worktree, tmux session, directory, or agent type ("codex")
//  5. substring of the display name
//
// Several matches in a tier are an ambiguity error listing the candidates.
func resolveSession(name string, sessions []models.Session) (models.Session, error) {
	query := strings.ToLower(strings.TrimSpace(name))
	if query == "" {
		return models.Session{}, fmt.Errorf("no target given")
	}
	tiers := []func(models.Session) bool{
		func(s models.Session) bool { return s.Name == name },
		func(s models.Session) bool { return strings.ToLower(s.Name) == query },
		func(s models.Session) bool { return strings.HasPrefix(strings.ToLower(s.Name), query+"/") },
		func(s models.Session) bool {
			for _, alias := range []string{
				s.Details.RepoName, s.Details.Worktree, s.SessionName,
				filepath.Base(s.Path), string(s.AgentType), models.AgentLabel(s.AgentType),
			} {
				if alias != "" && strings.ToLower(alias) == query {
					return true
				}
			}
			return false
		},
		func(s models.Session) bool { return strings.Contains(strings.ToLower(s.Name), query) },
	}
	for _, match := range tiers {
		var matches []models.Session
		for _, s := range sessions {
			if match(s) {
				matches = append(matches, s)
			}
		}
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0], nil
		default:
			names := make([]string, len(matches))
			for i, m := range matches {
				names[i] = m.Name
			}
			return models.Session{}, fmt.Errorf("'%s' is ambiguous — matches %s. Please specify one.",
				name, strings.Join(names, ", "))
		}
	}
	return models.Session{}, fmt.Errorf("session '%s' not found", name)
}

// resolveMySession determines the current session name.
func resolveMySession() string {
	return resolveMySessionFrom(scan())
}

// resolveMySessionFrom determines the current session name from a scan.
// Prefers TMUX_PANE (unambiguous when multiple agents share a cwd),
// falling back to cwd match against live tmux sessions, then the registry.
func resolveMySessionFrom(sessions []models.Session) string {
	paneID := os.Getenv("TMUX_PANE")
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}

	// 1. Match by pane_id (unambiguous when multiple agents share a cwd)
	if s, ok := sessionByPane(sessions, paneID); ok && paneID != "" {
		return s.Name
	}

	// 2. Fall back to cwd match
	if cwd != "" {
		for _, s := range sessions {
			if s.Path == cwd {
				return s.Name
			}
		}
	}

	// 3. Fall back to registry
	if cwd != "" {
		reg := state.NewRegistry(state.DefaultRegistryPath())
		if s := reg.FindByPath(cwd); s != nil {
			return s.Name
		}
	}
	if cwd == "" {
		return "unknown"
	}
	return filepath.Base(cwd)
}
