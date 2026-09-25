package picker

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/nemke/nagare-go/internal/log"
	"github.com/nemke/nagare-go/internal/mcp"
	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/theme"
)

// The mailbox is a full-screen view over every message agents have sent each
// other, opened with Ctrl+b. It answers three questions the agents' own
// conversations cannot: did a message arrive (and if not, where is it stuck),
// what was said, and how much old mail has piled up.

// mailState is mcp.State with the mailbox's labels, glyphs and colors.
type mailState mcp.State

const (
	mailQueued     = mailState(mcp.StateQueued)
	mailLost       = mailState(mcp.StateLost)
	mailNeedsReply = mailState(mcp.StateNeedsReply)
	mailDelivered  = mailState(mcp.StateDelivered)
	mailAnswered   = mailState(mcp.StateAnswered)
)

// staleQueue is how long a queued message may wait before it is a problem
// worth surfacing: the recipient has been busy, or stuck, for a long time.
const staleQueue = 10 * time.Minute

func (s mailState) label() string {
	return [...]string{"queued", "lost", "needs reply", "delivered", "answered"}[s]
}

// glyph is the one-cell status marker. Each state has its own shape as well as
// its own color, so the list still reads with color stripped.
func (s mailState) glyph() string {
	return [...]string{"◌", "✕", "?", "✓", "✔"}[s]
}

func (s mailState) color(c theme.Colors) color.Color {
	switch s {
	case mailQueued, mailNeedsReply:
		return c.Warning
	case mailLost:
		return c.Error
	case mailAnswered:
		return c.Success
	}
	return c.Subtle
}

// mailEntry is one message as the mailbox sees it.
type mailEntry struct {
	env   mcp.Envelope
	state mailState
	gone  bool // the recipient is not running under that name any more
	age   time.Duration
}

// open reports whether the message still expects something to happen.
func (e mailEntry) open() bool {
	return e.state == mailQueued || e.state == mailNeedsReply
}

// finished reports whether nothing more will happen to the message, which is
// what makes it safe to clean up.
func (e mailEntry) finished() bool {
	return e.state == mailDelivered || e.state == mailAnswered || e.state == mailLost
}

// problem reports whether the message probably went astray.
func (e mailEntry) problem() bool {
	return e.state == mailLost ||
		(e.gone && e.open()) ||
		(e.state == mailQueued && e.age > staleQueue)
}

// classify derives a message's state. live holds the names of running agents.
func classify(env mcp.Envelope, live map[string]bool, now time.Time) mailEntry {
	e := mailEntry{env: env, age: now.Sub(env.Sent()), state: mailState(env.State(now))}
	e.gone = !isLive(env.ToSession, live)
	return e
}

// isLive reports whether a recipient name still refers to a running agent.
// Display names drift — a second pane turns "web" into "web/codex" — so a name
// counts as live when it matches a running agent's name, its tmux session, or
// is one side of a "session/pane" name.
func isLive(name string, live map[string]bool) bool {
	if live[name] {
		return true
	}
	if i := strings.IndexByte(name, '/'); i > 0 && live[name[:i]] {
		return true
	}
	return false
}

// liveNames collects every name a running agent answers to.
func liveNames(sessions []models.Session) map[string]bool {
	live := map[string]bool{}
	for _, s := range sessions {
		if s.Status == models.StatusSaved || s.Status == models.StatusDead {
			continue
		}
		live[s.Name] = true
		live[s.SessionName] = true
		if i := strings.IndexByte(s.Name, '/'); i > 0 {
			live[s.Name[:i]] = true
		}
	}
	return live
}

// mailFilter is the tab strip across the top of the mailbox.
type mailFilter int

const (
	filterAll mailFilter = iota
	filterProblems
	filterUnanswered
	filterQueued
	filterDone
	filterCount
)

func (f mailFilter) label() string {
	return [...]string{"All", "Problems", "Unanswered", "Queued", "Done"}[f]
}

func (f mailFilter) match(e mailEntry) bool {
	switch f {
	case filterProblems:
		return e.problem()
	case filterUnanswered:
		return e.state == mailNeedsReply
	case filterQueued:
		return e.state == mailQueued
	case filterDone:
		return e.finished() && e.state != mailLost
	}
	return true
}

// mailSort orders conversations (or, in the timeline, messages).
type mailSort int

const (
	sortNewest mailSort = iota
	sortOldest
	sortSender
	sortRecipient
	sortState
	sortCount
)

func (s mailSort) label() string {
	return [...]string{"newest", "oldest", "sender", "recipient", "status"}[s]
}

// cleanupAges are the choices the cleanup dialog cycles through.
var cleanupAges = []struct {
	label string
	age   time.Duration
}{
	{"1 day", 24 * time.Hour},
	{"7 days", 7 * 24 * time.Hour},
	{"30 days", 30 * 24 * time.Hour},
	{"any age", 0},
}

// mailConfirm is a pending destructive answer.
type mailConfirm struct {
	cleanup bool      // false: delete target only
	target  mailEntry // the message to delete
	ageIdx  int       // cleanup: index into cleanupAges
}

// mailGroup is one conversation: every message between the same two agents.
type mailGroup struct {
	a, b    string
	entries []int // indices into mailbox.shown, oldest first
	latest  time.Time
	open    int
	problem int
}

// mailRow is one line of the list: a conversation header or a message.
type mailRow struct {
	group *mailGroup // non-nil for a header
	entry int        // index into mailbox.shown, for a message row
	blank bool       // spacer between conversations
}

// mailbox is the view's state. It is held by pointer so the markdown cache and
// scroll position survive the picker's value-copied Model.
type mailbox struct {
	all     []mailEntry
	shown   []mailEntry // filtered, searched and in display order
	rows    []mailRow
	cursor  int // index into shown
	filter  mailFilter
	sort    mailSort
	threads bool // grouped by conversation, rather than one timeline
	search  textinput.Model
	scroll  int  // detail pane scroll, in rows
	reader  bool // detail pane full screen
	confirm *mailConfirm
	note    string // result of the last action
	err     string
	md      *markdownCache
	now     func() time.Time
	// agentIndex numbers every agent that appears in the mail, in name order,
	// to give each a distinct color.
	agentIndex map[string]int
}

func newMailbox() *mailbox {
	ti := textinput.New()
	ti.Placeholder = "search messages..."
	ti.Prompt = " > "
	ti.CharLimit = 64
	ti.Focus()
	return &mailbox{
		threads: true,
		search:  ti,
		md:      newMarkdownCache(),
		now:     time.Now,
	}
}

// load rereads every message from disk. The selection follows the message it
// was on, so a refresh while reading does not jump elsewhere.
func (b *mailbox) load(sessions []models.Session) {
	envs, err := mcp.AllMessages()
	if err != nil {
		b.err = fmt.Sprintf("reading messages: %v", err)
		log.Error("mailbox: %v", err)
	}
	live := liveNames(sessions)
	now := b.now()
	b.all = b.all[:0]
	names := map[string]bool{}
	for _, env := range envs {
		b.all = append(b.all, classify(env, live, now))
		names[env.FromSession], names[env.ToSession] = true, true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	b.agentIndex = make(map[string]int, len(sorted))
	for i, n := range sorted {
		b.agentIndex[n] = i
	}
	b.refresh()
}

// selected returns the message under the cursor.
func (b *mailbox) selected() (mailEntry, bool) {
	if b.cursor < 0 || b.cursor >= len(b.shown) {
		return mailEntry{}, false
	}
	return b.shown[b.cursor], true
}

// refresh recomputes what is shown from the current filter, search and sort,
// keeping the cursor on the same message when it is still visible.
func (b *mailbox) refresh() {
	prevID := ""
	if e, ok := b.selected(); ok {
		prevID = e.env.ID
	}

	query := strings.ToLower(strings.TrimSpace(b.search.Value()))
	var picked []mailEntry
	for _, e := range b.all {
		if b.filter.match(e) && matchesQuery(e, query) {
			picked = append(picked, e)
		}
	}

	if b.threads {
		b.shown, b.rows = groupConversations(picked, b.sort)
	} else {
		sortEntries(picked, b.sort)
		b.shown = picked
		b.rows = make([]mailRow, len(picked))
		for i := range picked {
			b.rows[i] = mailRow{entry: i}
		}
	}

	b.cursor = 0
	kept := false
	for i, e := range b.shown {
		if e.env.ID == prevID {
			b.cursor, kept = i, true
			break
		}
	}
	// The periodic reload must not throw away the reader's place.
	if !kept {
		b.scroll = 0
	}
}

// matchesQuery is a plain substring match over everything a person might
// search a message by. Message text is prose, where fuzzy matching finds
// nonsense; a substring is what a search box over prose is expected to do.
func matchesQuery(e mailEntry, query string) bool {
	if query == "" {
		return true
	}
	fields := []string{e.env.FromSession, e.env.ToSession, e.env.Content, e.env.ID, e.state.label()}
	if e.env.Response != nil {
		fields = append(fields, *e.env.Response)
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), query) {
			return true
		}
	}
	return false
}

// sortEntries orders a flat list of messages.
func sortEntries(entries []mailEntry, by mailSort) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		switch by {
		case sortOldest:
			if !a.env.Sent().Equal(b.env.Sent()) {
				return a.env.Sent().Before(b.env.Sent())
			}
			// Timestamps from before sub-second precision tie within a
			// second; a reply still has to follow what it answers.
			return b.env.InReplyTo == a.env.ID
		case sortSender:
			if a.env.FromSession != b.env.FromSession {
				return a.env.FromSession < b.env.FromSession
			}
		case sortRecipient:
			if a.env.ToSession != b.env.ToSession {
				return a.env.ToSession < b.env.ToSession
			}
		case sortState:
			if a.state != b.state {
				return a.state < b.state
			}
		}
		return a.env.Sent().After(b.env.Sent())
	})
}

// pairKey names a conversation independently of direction, so a message and
// its reply land in the same one.
func pairKey(a, b string) (string, string) {
	if b < a {
		return b, a
	}
	return a, b
}

// groupConversations buckets messages by the pair of agents involved. Within a
// conversation messages run oldest first, as a chat does; the sort mode orders
// the conversations themselves.
func groupConversations(entries []mailEntry, by mailSort) ([]mailEntry, []mailRow) {
	byPair := map[[2]string][]mailEntry{}
	var order [][2]string
	for _, e := range entries {
		a, b := pairKey(e.env.FromSession, e.env.ToSession)
		key := [2]string{a, b}
		if _, ok := byPair[key]; !ok {
			order = append(order, key)
		}
		byPair[key] = append(byPair[key], e)
	}

	groups := make([]*mailGroup, 0, len(order))
	msgs := map[*mailGroup][]mailEntry{}
	for _, key := range order {
		list := byPair[key]
		sortEntries(list, sortOldest)
		g := &mailGroup{a: key[0], b: key[1]}
		for _, e := range list {
			if t := e.env.Sent(); t.After(g.latest) {
				g.latest = t
			}
			if e.open() {
				g.open++
			}
			if e.problem() {
				g.problem++
			}
		}
		groups = append(groups, g)
		msgs[g] = list
	}

	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		switch by {
		case sortOldest:
			return a.latest.Before(b.latest)
		case sortSender, sortRecipient:
			if a.a != b.a {
				return a.a < b.a
			}
			return a.b < b.b
		case sortState:
			// Conversations with something wrong first, then those still open.
			if a.problem != b.problem {
				return a.problem > b.problem
			}
			if a.open != b.open {
				return a.open > b.open
			}
		}
		return a.latest.After(b.latest)
	})

	var shown []mailEntry
	var rows []mailRow
	for gi, g := range groups {
		if gi > 0 {
			rows = append(rows, mailRow{blank: true})
		}
		rows = append(rows, mailRow{group: g})
		for _, e := range msgs[g] {
			g.entries = append(g.entries, len(shown))
			rows = append(rows, mailRow{entry: len(shown)})
			shown = append(shown, e)
		}
	}
	return shown, rows
}

// counts tallies messages per filter tab, over everything loaded.
func (b *mailbox) counts() [filterCount]int {
	var n [filterCount]int
	for _, e := range b.all {
		for f := mailFilter(0); f < filterCount; f++ {
			if f.match(e) {
				n[f]++
			}
		}
	}
	return n
}

// cleanupCandidates lists the finished messages older than age (any age when
// age is zero). Open messages are never candidates: deleting a queued message
// or one still awaiting an answer would silently break a conversation.
func (b *mailbox) cleanupCandidates(age time.Duration) []mailEntry {
	var out []mailEntry
	for _, e := range b.all {
		if e.finished() && (age == 0 || e.age >= age) {
			out = append(out, e)
		}
	}
	return out
}

// --- Keys ---

// openMailbox shows the mailbox, loaded from disk.
func (m Model) openMailbox() Model {
	m.mail = newMailbox()
	m.mail.load(m.sessions)
	return m
}

func (m Model) handleMailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	b := m.mail
	key := msg.String()
	b.note, b.err = "", ""

	if b.confirm != nil {
		return m.handleMailConfirmKey(key)
	}

	// Scrolling the detail pane: a page is most of what is visible, leaving a
	// couple of rows of overlap to keep the reader's place.
	page := max(m.height/2, 3)

	switch key {
	case keyEscape:
		switch {
		case b.reader:
			b.reader = false
		case b.search.Value() != "":
			b.search.SetValue("")
			b.refresh()
		default:
			m.mail = nil
		}
		return m, nil
	case keyMailbox:
		m.mail = nil
		return m, nil
	case keyEnter:
		if _, ok := b.selected(); ok {
			b.reader = !b.reader
			b.scroll = 0
		}
		return m, nil
	case keyUp:
		if b.reader {
			b.scroll = max(b.scroll-1, 0)
		} else if b.cursor > 0 {
			b.cursor--
			b.scroll = 0
		}
		return m, nil
	case keyDown:
		if b.reader {
			b.scroll++
		} else if b.cursor < len(b.shown)-1 {
			b.cursor++
			b.scroll = 0
		}
		return m, nil
	case "pgdown":
		b.scroll += page
		return m, nil
	case "pgup":
		b.scroll = max(b.scroll-page, 0)
		return m, nil
	case "home":
		b.cursor, b.scroll = 0, 0
		return m, nil
	case "end":
		b.cursor, b.scroll = max(len(b.shown)-1, 0), 0
		return m, nil
	case "left":
		b.filter = (b.filter + filterCount - 1) % filterCount
		b.refresh()
		return m, nil
	case "right":
		b.filter = (b.filter + 1) % filterCount
		b.refresh()
		return m, nil
	case keyToggleView:
		b.threads = !b.threads
		b.refresh()
		return m, nil
	case keyCycleSort:
		b.sort = (b.sort + 1) % sortCount
		b.refresh()
		return m, nil
	case keyKillSession:
		if e, ok := b.selected(); ok {
			b.confirm = &mailConfirm{target: e}
		}
		return m, nil
	case keyMailCleanup:
		b.confirm = &mailConfirm{cleanup: true, ageIdx: 1}
		return m, nil
	}

	if b.reader {
		return m, nil // the reader has no search box to type into
	}
	var cmd tea.Cmd
	before := b.search.Value()
	b.search, cmd = b.search.Update(msg)
	if b.search.Value() != before {
		b.refresh()
	}
	return m, cmd
}

func (m Model) handleMailConfirmKey(key string) (tea.Model, tea.Cmd) {
	b := m.mail
	c := b.confirm
	switch key {
	case "y", "Y":
		b.confirm = nil
		var victims []mailEntry
		if c.cleanup {
			victims = b.cleanupCandidates(cleanupAges[c.ageIdx].age)
		} else {
			victims = []mailEntry{c.target}
		}
		deleted := 0
		for _, e := range victims {
			if err := mcp.DeleteMessage(e.env); err != nil {
				b.err = fmt.Sprintf("could not delete %s: %v", e.env.ID, err)
				log.Error("mailbox: delete %s: %v", e.env.ID, err)
				continue
			}
			deleted++
		}
		log.Info("mailbox: deleted %d message(s)", deleted)
		if b.err == "" {
			b.note = fmt.Sprintf("Deleted %d message%s", deleted, plural(deleted))
		}
		b.load(m.sessions)
	case "n", "N", keyEscape:
		b.confirm = nil
	case "left":
		if c.cleanup {
			c.ageIdx = (c.ageIdx + len(cleanupAges) - 1) % len(cleanupAges)
		}
	case "right":
		if c.cleanup {
			c.ageIdx = (c.ageIdx + 1) % len(cleanupAges)
		}
	}
	return m, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
