package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Envelope is a stored message together with the facts about it that are not
// in the message file itself: where it is filed, whether a copy is still
// queued for push delivery, and whether its sender is blocked waiting on it.
// It is what the mailbox view shows.
type Envelope struct {
	Message
	Inbox   string // inbox directory name (the sanitized recipient name at send time)
	Path    string // the message file
	Size    int64  // bytes on disk
	Queued  bool   // a push copy is still waiting to be delivered
	Waiting bool   // the sender is blocked in send_message_and_wait
}

// Sent parses CreatedAt, the zero time when it is missing or malformed.
func (e Envelope) Sent() time.Time {
	t, _ := time.Parse(time.RFC3339, e.CreatedAt)
	return t
}

// Answered parses RespondedAt, the zero time when there is no response.
func (e Envelope) Answered() time.Time {
	if e.RespondedAt == nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, *e.RespondedAt)
	return t
}

// AllMessages reads every message in every inbox. Unreadable files are
// skipped rather than failing the whole listing: a mailbox with one corrupt
// file should still show the rest.
func AllMessages() ([]Envelope, error) {
	base := MessagesDir()
	dirs, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	queued := queuedIDs()
	now := time.Now()

	var out []Envelope
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(base, d.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasPrefix(name, "msg_") || !strings.HasSuffix(name, ".json") {
				continue
			}
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var msg Message
			if err := json.Unmarshal(data, &msg); err != nil || msg.ID == "" {
				continue
			}
			out = append(out, Envelope{
				Message: msg,
				Inbox:   d.Name(),
				Path:    path,
				Size:    int64(len(data)),
				Queued:  queued[msg.ID],
				Waiting: waiting(filepath.Join(dir, "msg_"+msg.ID+".wait"), now),
			})
		}
	}
	return out, nil
}

// queuedIDs returns the ids of every message with a push copy still queued,
// in any pane's push directory.
func queuedIDs() map[string]bool {
	ids := map[string]bool{}
	panes, err := os.ReadDir(filepath.Join(dataDir(), "push"))
	if err != nil {
		return ids
	}
	for _, p := range panes {
		if !p.IsDir() {
			continue
		}
		for _, name := range queuedNames(p.Name()) {
			if id := pushID(name); id != "" {
				ids[id] = true
			}
		}
	}
	return ids
}

// pushID extracts the message id from a push file name, "<nanos>_<id>.json".
func pushID(name string) string {
	name = strings.TrimSuffix(name, ".json")
	if i := strings.IndexByte(name, '_'); i >= 0 {
		return name[i+1:]
	}
	return ""
}

// waiting reports whether a wait marker exists and its deadline has not
// passed. A marker left behind by a sender that crashed expires on its own.
func waiting(marker string, now time.Time) bool {
	data, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	deadline, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	return err == nil && now.Before(deadline)
}

// DeleteMessage removes a message together with everything that refers to it:
// its wait marker and any push copy still queued, so a deleted message can
// never be delivered afterwards. The inbox directory goes too once it is empty.
func DeleteMessage(e Envelope) error {
	if err := os.Remove(e.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(e.Path)
	os.Remove(filepath.Join(dir, "msg_"+e.ID+".wait"))
	unqueue(e.ID)
	if rest, err := os.ReadDir(dir); err == nil && len(rest) == 0 {
		os.Remove(dir)
	}
	return nil
}

// unqueue deletes any queued push copies of a message.
func unqueue(id string) {
	base := filepath.Join(dataDir(), "push")
	panes, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, p := range panes {
		if !p.IsDir() {
			continue
		}
		for _, name := range queuedNames(p.Name()) {
			if pushID(name) == id {
				os.Remove(filepath.Join(base, p.Name(), name))
			}
		}
	}
}
