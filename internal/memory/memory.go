// Package memory is what agents learn, kept so the next agent — or the same
// one after a restart, or a sibling in another worktree — does not learn it
// again.
//
// Memories are Markdown files, one per memory, under
// ~/.local/share/nagare/memory: files are the source of truth, so a human can
// read and edit them in any editor and parallel agents never contend for one
// file. Search is BM25 over those files, computed per query: a repository's
// memory is tens to hundreds of notes, which is milliseconds to scan, and
// scanning cannot go stale the way a cache shared by six agent processes and
// a human editor would.
//
// Memories are written explicitly, by agents calling `remember` (or a human
// saving a file). Precision beats recall here: a wrong memory makes every
// future session worse.
package memory

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nemke/nagare-go/internal/fsutil"
	"github.com/nemke/nagare-go/internal/git"
)

// Kinds a memory can be. The kind steers ranking and the session digest:
// conventions and preferences are always worth knowing, facts only when asked.
var Kinds = []string{"gotcha", "decision", "convention", "fact", "preference"}

// Memory is one note.
type Memory struct {
	ID         string
	Kind       string
	Scope      string // "project" or "global"
	Status     string // "active", "superseded", "archived"
	Tags       []string
	Paths      []string // repo-relative files it concerns
	Author     string
	Session    string
	Commit     string
	Created    time.Time
	Updated    time.Time
	LastUsed   time.Time
	Uses       int
	Pinned     bool
	Supersedes []string
	Body       string // Markdown; the first line is the title

	File string // where it lives; set when loaded or saved
}

// Title is the body's first line.
func (m Memory) Title() string {
	line, _, _ := strings.Cut(strings.TrimSpace(m.Body), "\n")
	return strings.TrimSpace(strings.TrimLeft(line, "# "))
}

// DefaultDir is the memory root.
func DefaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nagare", "memory")
}

// ProjectKey names a repository's memory directory: the main checkout's
// basename plus a short hash of its path, so every worktree of a repo shares
// one memory and two repos with the same name do not.
func ProjectKey(dir string) (key, root string) {
	root = git.MainRoot(dir)
	if root == "" {
		root = dir
	}
	root = filepath.Clean(root)
	sum := sha1.Sum([]byte(root))
	return filepath.Base(root) + "-" + hex.EncodeToString(sum[:4]), root
}

// Store reads and writes memories under a root directory.
type Store struct {
	Root string
}

// Open returns the store at the default location.
func Open() *Store {
	return &Store{Root: DefaultDir()}
}

func (s *Store) dir(scope, project string) string {
	if scope == "global" {
		return filepath.Join(s.Root, "global")
	}
	return filepath.Join(s.Root, "projects", project)
}

// newID is "m" plus five base-32 characters: short enough to type and cite.
func newID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	out := []byte{'m'}
	for _, c := range b {
		out = append(out, alphabet[int(c)%len(alphabet)])
	}
	return string(out)
}

// slug makes a filename fragment from a title.
func slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 48 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// Format renders a memory as a Markdown file with front matter.
func Format(m Memory) string {
	var b strings.Builder
	b.WriteString("---\n")
	field := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	list := func(k string, v []string) {
		if len(v) > 0 {
			fmt.Fprintf(&b, "%s: [%s]\n", k, strings.Join(v, ", "))
		}
	}
	ts := func(k string, t time.Time) {
		if !t.IsZero() {
			field(k, t.UTC().Format(time.RFC3339))
		}
	}
	field("id", m.ID)
	field("kind", m.Kind)
	field("scope", m.Scope)
	field("status", m.Status)
	list("tags", m.Tags)
	list("paths", m.Paths)
	field("author", m.Author)
	field("session", m.Session)
	field("commit", m.Commit)
	ts("created", m.Created)
	ts("updated", m.Updated)
	ts("last_used", m.LastUsed)
	if m.Uses > 0 {
		field("uses", strconv.Itoa(m.Uses))
	}
	if m.Pinned {
		field("pinned", "true")
	}
	list("supersedes", m.Supersedes)
	b.WriteString("---\n")
	b.WriteString(strings.TrimSpace(m.Body))
	b.WriteString("\n")
	return b.String()
}

// Parse reads a memory file. Unknown keys are ignored, so a human can add
// their own.
func Parse(text string) (Memory, error) {
	var m Memory
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return m, errors.New("no front matter")
	}
	head, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return m, errors.New("unterminated front matter")
	}
	m.Body = strings.TrimSpace(strings.TrimPrefix(body, "\n"))
	for _, line := range strings.Split(head, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "id":
			m.ID = v
		case "kind":
			m.Kind = v
		case "scope":
			m.Scope = v
		case "status":
			m.Status = v
		case "tags":
			m.Tags = parseList(v)
		case "paths":
			m.Paths = parseList(v)
		case "author":
			m.Author = v
		case "session":
			m.Session = v
		case "commit":
			m.Commit = v
		case "created":
			m.Created, _ = time.Parse(time.RFC3339, v)
		case "updated":
			m.Updated, _ = time.Parse(time.RFC3339, v)
		case "last_used":
			m.LastUsed, _ = time.Parse(time.RFC3339, v)
		case "uses":
			m.Uses, _ = strconv.Atoi(v)
		case "pinned":
			m.Pinned = v == "true"
		case "supersedes":
			m.Supersedes = parseList(v)
		}
	}
	if m.ID == "" {
		return m, errors.New("no id")
	}
	if m.Status == "" {
		m.Status = "active"
	}
	if m.Kind == "" {
		m.Kind = "fact"
	}
	return m, nil
}

func parseList(v string) []string {
	v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.Trim(strings.TrimSpace(p), `"'`); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Load returns the memories of a project plus the global ones. archived
// includes superseded and archived memories too.
func (s *Store) Load(project string, archived bool) []Memory {
	var out []Memory
	for _, dir := range []string{s.dir("project", project), s.dir("global", "")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			m, err := Parse(string(data))
			if err != nil {
				continue
			}
			m.File = path
			if m.Scope == "" {
				m.Scope = "project"
				if dir == s.dir("global", "") {
					m.Scope = "global"
				}
			}
			if archived || m.Status == "active" {
				out = append(out, m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// Find returns the memories with the given ids.
func (s *Store) Find(project string, ids []string) []Memory {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[strings.TrimSpace(id)] = true
	}
	var out []Memory
	for _, m := range s.Load(project, true) {
		if want[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

// Save writes a memory to its file atomically, choosing a file for a new one.
// root is the repository the project key stands for, recorded beside the
// memories for humans browsing the tree.
func (s *Store) Save(project, root string, m *Memory) error {
	if m.File == "" {
		dir := s.dir(m.Scope, project)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		name := m.ID
		if sl := slug(m.Title()); sl != "" {
			name += "-" + sl
		}
		m.File = filepath.Join(dir, name+".md")
		// Record which repository a project directory belongs to, for
		// humans browsing the tree.
		if m.Scope != "global" && root != "" {
			if _, err := os.Stat(filepath.Join(dir, ".root")); err != nil {
				_ = fsutil.AtomicWrite(filepath.Join(dir, ".root"), []byte(root+"\n"), 0o644)
			}
		}
	}
	return fsutil.AtomicWrite(m.File, []byte(Format(*m)), 0o644)
}

// archive moves a memory's current file content aside before it changes.
func (s *Store) archive(m Memory) {
	if m.File == "" {
		return
	}
	dir := filepath.Join(filepath.Dir(m.File), "archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := os.ReadFile(m.File)
	if err != nil {
		return
	}
	name := fmt.Sprintf("%s.%d.md", m.ID, time.Now().UnixNano())
	_ = fsutil.AtomicWrite(filepath.Join(dir, name), data, 0o644)
}

// headCommit is HEAD of dir, for knowing later whether a memory's files have
// moved on since it was written.
func headCommit(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
