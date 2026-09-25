package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/nemke/nagare-go/internal/fsutil"
)

// Message status constants.
const (
	StatusPending   = "pending"
	StatusDelivered = "delivered"
	StatusRead      = "read"
	StatusCompleted = "completed"
)

// AgentInfo describes a discovered agent for MCP listing.
type AgentInfo struct {
	SessionName string
	AgentType   string
	Status      string
	Path        string
}

// Message is an inter-agent message stored as a JSON file.
type Message struct {
	ID           string  `json:"id"`
	FromSession  string  `json:"from_session"`
	ToSession    string  `json:"to_session"`
	Content      string  `json:"content"`
	ExpectsReply bool    `json:"expects_reply"`
	Status       string  `json:"status"`   // "pending", "delivered", "completed"
	Response     *string `json:"response"` // nil until reply
	CreatedAt    string  `json:"created_at"`
	RespondedAt  *string `json:"responded_at"`          // nil until reply
	FromPane     string  `json:"from_pane,omitempty"`   // sender's tmux pane, for pushing the reply back
	InReplyTo    string  `json:"in_reply_to,omitempty"` // set on a reply pushed back to the original sender
	AutoReply    bool    `json:"auto_reply,omitempty"`  // response taken from the recipient's final message
}

// MessagesDir returns the base messages directory.
func MessagesDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nagare", "messages")
}

// sanitizeName replaces filesystem-unsafe characters in session names so they
// can be used as directory components under MessagesDir.
func sanitizeName(name string) string {
	return strings.ReplaceAll(name, "/", "__")
}

// InboxDir returns a session's inbox directory.
func InboxDir(sessionName string) string {
	return filepath.Join(MessagesDir(), sanitizeName(sessionName))
}

// MessagePath returns the file path for a message.
func MessagePath(toSession, msgID string) string {
	return filepath.Join(InboxDir(toSession), fmt.Sprintf("msg_%s.json", msgID))
}

// waitPath marks a message whose sender is blocked in send_message_and_wait.
// Its presence tells reply() the answer will be collected, so it need not be
// pushed back as a separate message.
func waitPath(toSession, msgID string) string {
	return filepath.Join(InboxDir(toSession), fmt.Sprintf("msg_%s.wait", msgID))
}

// WriteMessage writes a message to the target's inbox.
func WriteMessage(msg Message) error {
	dir := InboxDir(msg.ToSession)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(MessagePath(msg.ToSession, msg.ID), data, 0644)
}

// statusRank orders the statuses a message moves through. A message only ever
// moves forward: a late delivery report must not turn an answered message
// back into an unread one.
func statusRank(status string) int {
	switch status {
	case StatusPending:
		return 0
	case StatusDelivered:
		return 1
	case StatusRead:
		return 2
	case StatusCompleted:
		return 3
	}
	return -1
}

// UpdateMessage changes a stored message under its inbox's lock. fn edits the
// message as it is on disk now and returns false to leave it untouched.
//
// Every change after creation goes through here. Deliveries, check_messages,
// replies and automatic replies run in different processes and can touch one
// message at the same moment; a plain read-modify-write let a delivery report
// that read the file before a reply was written save over the reply, so an
// answered message came back as merely delivered, its answer gone. The lock
// closes that, and the forward-only rule is enforced here once, whatever fn
// does: the status never moves back and a response is never dropped.
func UpdateMessage(toSession, msgID string, fn func(*Message) bool) (Message, error) {
	var out Message
	err := withFileLock(filepath.Join(InboxDir(toSession), ".lock"), func() error {
		cur, err := ReadMessage(toSession, msgID)
		if err != nil {
			return err
		}
		next := cur
		if !fn(&next) {
			out = cur
			return nil
		}
		if statusRank(next.Status) < statusRank(cur.Status) {
			next.Status = cur.Status
		}
		if cur.Response != nil && next.Response == nil {
			next.Response, next.RespondedAt, next.AutoReply = cur.Response, cur.RespondedAt, cur.AutoReply
		}
		out = next
		return WriteMessage(next)
	})
	return out, err
}

// withFileLock runs fn holding an exclusive flock on path, creating it if
// needed. flock locks belong to the open file, so this excludes other
// goroutines as well as other processes.
func withFileLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// ReadMessage reads a message from disk.
func ReadMessage(toSession, msgID string) (Message, error) {
	data, err := os.ReadFile(MessagePath(toSession, msgID))
	if err != nil {
		return Message{}, err
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return Message{}, err
	}
	return msg, nil
}

// FindMessage locates a message by id in any inbox. Ids are unique, and a
// lookup by id survives a display-name change — tmux renaming a window, or a
// second agent joining the session — which moves the inbox a name maps to.
func FindMessage(msgID string) (Message, error) {
	dirs, err := os.ReadDir(MessagesDir())
	if err != nil {
		return Message{}, err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(MessagesDir(), d.Name(), fmt.Sprintf("msg_%s.json", msgID)))
		if err != nil {
			continue
		}
		var msg Message
		if err := json.Unmarshal(data, &msg); err == nil {
			return msg, nil
		}
	}
	return Message{}, os.ErrNotExist
}

// ListInbox reads all messages in a session's inbox.
func ListInbox(sessionName string) ([]Message, error) {
	dir := InboxDir(sessionName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var msgs []Message
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// NewMessageID generates a short unique message ID (hex, no hyphens).
func NewMessageID() string {
	id := uuid.New().String()
	return strings.ReplaceAll(id, "-", "")[:12]
}
