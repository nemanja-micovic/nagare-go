package state

import (
	"encoding/json"
	"github.com/nemke/nagare-go/internal/paths"
	"os"
	"path/filepath"
	"time"

	"github.com/nemke/nagare-go/internal/fsutil"
)

// RegisteredSession is a session tracked in the registry.
type RegisteredSession struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Agent        string `json:"agent"`
	LastAccessed string `json:"last_accessed"`
	Starred      bool   `json:"starred"`
}

// Registry manages the persistent session registry.
type Registry struct {
	path     string
	sessions []RegisteredSession
}

// DefaultRegistryPath returns the default registry file path.
func DefaultRegistryPath() string {
	return filepath.Join(paths.Data(), "sessions.json")
}

// NewRegistry loads or creates a registry at the given path.
func NewRegistry(path string) *Registry {
	r := &Registry{path: path}
	r.load()
	return r
}

func (r *Registry) load() {
	data, err := os.ReadFile(r.path)
	if err != nil {
		r.sessions = nil
		return
	}
	_ = json.Unmarshal(data, &r.sessions)
}

func (r *Registry) save() error {
	data, err := json.MarshalIndent(r.sessions, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0755); err != nil {
		return err
	}
	return fsutil.AtomicWrite(r.path, data, 0644)
}

// ListAll returns all registered sessions.
func (r *Registry) ListAll() []RegisteredSession {
	return r.sessions
}

// Find returns a session by name, or nil.
func (r *Registry) Find(name string) *RegisteredSession {
	for i := range r.sessions {
		if r.sessions[i].Name == name {
			return &r.sessions[i]
		}
	}
	return nil
}

// FindByPath returns a session by path, or nil.
func (r *Registry) FindByPath(path string) *RegisteredSession {
	for i := range r.sessions {
		if r.sessions[i].Path == path {
			return &r.sessions[i]
		}
	}
	return nil
}

// Register adds or updates a session. Saves to disk.
func (r *Registry) Register(name, path, agent string) {
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range r.sessions {
		if r.sessions[i].Name == name {
			r.sessions[i].Path = path
			r.sessions[i].Agent = agent
			r.sessions[i].LastAccessed = now
			r.save()
			return
		}
	}
	r.sessions = append(r.sessions, RegisteredSession{
		Name:         name,
		Path:         path,
		Agent:        agent,
		LastAccessed: now,
	})
	r.save()
}

// Remove deletes a session by name. Saves to disk.
func (r *Registry) Remove(name string) {
	for i := range r.sessions {
		if r.sessions[i].Name == name {
			r.sessions = append(r.sessions[:i], r.sessions[i+1:]...)
			r.save()
			return
		}
	}
}

// ToggleStar toggles the starred flag. Saves to disk. Returns new state.
func (r *Registry) ToggleStar(name string) bool {
	for i := range r.sessions {
		if r.sessions[i].Name == name {
			r.sessions[i].Starred = !r.sessions[i].Starred
			r.save()
			return r.sessions[i].Starred
		}
	}
	return false
}

// Touch updates the last_accessed timestamp. Saves to disk.
func (r *Registry) Touch(name string) {
	for i := range r.sessions {
		if r.sessions[i].Name == name {
			r.sessions[i].LastAccessed = time.Now().UTC().Format(time.RFC3339)
			r.save()
			return
		}
	}
}
