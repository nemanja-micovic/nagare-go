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
	FromPaneID   string  `json:"from_pane_id,omitempty"`
	FromAgentID  string  `json:"from_agent_id,omitempty"`
	ToSession    string  `json:"to_session"`
	ToPaneID     string  `json:"to_pane_id,omitempty"`
	ToAgentID    string  `json:"to_agent_id,omitempty"`
	Content      string  `json:"content"`
	ExpectsReply bool    `json:"expects_reply"`
	Status       string  `json:"status"`   // pending, delivered, read, or completed
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
func paneInboxDir(paneID string) string {
	return filepath.Join(MessagesDir(), ".panes", sanitizeName(paneID))
}

// MessagePath returns the file path for a message.
func MessagePath(toSession, msgID string) string {
	return filepath.Join(InboxDir(toSession), fmt.Sprintf("msg_%s.json", msgID))
}

// WriteMessage writes a message to the target's stable pane inbox when known,
// falling back to the legacy display-name inbox for older callers and records.
func WriteMessage(msg Message) error {
	dir := InboxDir(msg.ToSession)
	if msg.ToPaneID != "" {
		dir = paneInboxDir(msg.ToPaneID)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(filepath.Join(dir, fmt.Sprintf("msg_%s.json", msg.ID)), data, 0644)
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

// ReadStoredMessage reads a message from the stable inbox selected by its
// persisted identity.
func ReadStoredMessage(msg Message) (Message, error) {
	if msg.ToPaneID == "" {
		return ReadMessage(msg.ToSession, msg.ID)
	}
	data, err := os.ReadFile(filepath.Join(paneInboxDir(msg.ToPaneID), fmt.Sprintf("msg_%s.json", msg.ID)))
	if err != nil {
		return Message{}, err
	}
	var stored Message
	if err := json.Unmarshal(data, &stored); err != nil {
		return Message{}, err
	}
	return stored, nil
}

// ListInbox reads messages stored under a legacy display-name inbox.
func ListInbox(sessionName string) ([]Message, error) {
	return listInboxDir(InboxDir(sessionName))
}

// ListInboxFor reads the stable pane inbox plus any legacy display-name inbox.
// Pane identity survives display-name changes as panes are added or renamed.
func ListInboxFor(sessionName, paneID string) ([]Message, error) {
	legacy, err := ListInbox(sessionName)
	if err != nil {
		return nil, err
	}
	if paneID == "" {
		return legacy, nil
	}
	stable, err := listInboxDir(paneInboxDir(paneID))
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Message, len(legacy)+len(stable))
	for _, msg := range legacy {
		byID[msg.ID] = msg
	}
	for _, msg := range stable {
		byID[msg.ID] = msg
	}
	result := make([]Message, 0, len(byID))
	for _, msg := range byID {
		result = append(result, msg)
	}
	return result, nil
}

func listInboxDir(dir string) ([]Message, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var msgs []Message
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", filepath.Join(dir, entry.Name()), err)
		}
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decoding %s: %w", filepath.Join(dir, entry.Name()), err)
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// ListAllMessages reads legacy and stable pane inboxes.
func ListAllMessages() ([]Message, error) {
	baseDir := MessagesDir()
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var result []Message
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == ".panes" {
			panes, err := os.ReadDir(filepath.Join(baseDir, entry.Name()))
			if err != nil {
				return nil, err
			}
			for _, pane := range panes {
				if !pane.IsDir() {
					continue
				}
				messages, err := listInboxDir(filepath.Join(baseDir, entry.Name(), pane.Name()))
				if err != nil {
					return nil, err
				}
				result = append(result, messages...)
			}
			continue
		}
		messages, err := listInboxDir(filepath.Join(baseDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		result = append(result, messages...)
	}
	return result, nil
}

// NewMessageID generates a short unique message ID (hex, no hyphens).
func NewMessageID() string {
	id := uuid.New().String()
	return strings.ReplaceAll(id, "-", "")[:12]
}
