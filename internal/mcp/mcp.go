package mcp

import (
	"encoding/json"
	"fmt"
	"github.com/nemke/nagare-go/internal/paths"
	"os"
	"path/filepath"
	"strings"

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
	RespondedAt  *string `json:"responded_at"` // nil until reply
}

// MessagesDir returns the base messages directory.
func MessagesDir() string {
	return filepath.Join(paths.Data(), "messages")
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
