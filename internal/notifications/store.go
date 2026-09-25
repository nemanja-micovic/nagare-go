package notifications

import (
	"encoding/json"
	"github.com/nemke/nagare-go/internal/paths"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/nemke/nagare-go/internal/fsutil"
)

// Notification is a stored notification entry.
type Notification struct {
	ID          string `json:"id"`
	SessionName string `json:"session_name"`
	Message     string `json:"message"`
	Timestamp   string `json:"timestamp"`
	Read        bool   `json:"read"`
}

// Store manages persistent notifications.
type Store struct {
	path          string
	notifications []Notification
}

// DefaultStorePath returns the default notification store path.
func DefaultStorePath() string {
	return filepath.Join(paths.Data(), "notifications.json")
}

// NewStore loads or creates a notification store.
func NewStore(path string) *Store {
	s := &Store{path: path}
	s.load()
	return s
}

func (s *Store) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		s.notifications = nil
		return
	}
	_ = json.Unmarshal(data, &s.notifications)
}

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.notifications, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	return fsutil.AtomicWrite(s.path, data, 0644)
}

const maxNotifications = 200

// Add appends a new notification, trimming old entries beyond the cap.
func (s *Store) Add(sessionName, message string) {
	s.notifications = append(s.notifications, Notification{
		ID:          uuid.New().String(),
		SessionName: sessionName,
		Message:     message,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Read:        false,
	})
	if len(s.notifications) > maxNotifications {
		s.notifications = s.notifications[len(s.notifications)-maxNotifications:]
	}
	s.save()
}

// ListAll returns notifications in reverse chronological order (newest first).
func (s *Store) ListAll() []Notification {
	sorted := make([]Notification, len(s.notifications))
	copy(sorted, s.notifications)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp > sorted[j].Timestamp
	})
	return sorted
}

// MarkRead marks a notification as read by ID.
func (s *Store) MarkRead(id string) {
	for i := range s.notifications {
		if s.notifications[i].ID == id {
			s.notifications[i].Read = true
			s.save()
			return
		}
	}
}

// Dismiss removes a notification by ID.
func (s *Store) Dismiss(id string) {
	for i := range s.notifications {
		if s.notifications[i].ID == id {
			s.notifications = append(s.notifications[:i], s.notifications[i+1:]...)
			s.save()
			return
		}
	}
}

// DismissAll removes all notifications.
func (s *Store) DismissAll() {
	s.notifications = nil
	s.save()
}

// UnreadCount returns the number of unread notifications.
func (s *Store) UnreadCount() int {
	count := 0
	for _, n := range s.notifications {
		if !n.Read {
			count++
		}
	}
	return count
}
