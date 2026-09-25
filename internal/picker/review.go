package picker

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemke/nagare-go/internal/git"
	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/theme"
)

// The review panel answers the question every agent session ends with: what did
// it actually change? It lists the working tree's uncommitted files with line
// counts, and shows git's own coloured diff for the selected one — without
// leaving nagare, and without the agent having to be asked.

type reviewState struct {
	open    bool
	dir     string
	title   string
	files   []git.Change
	sel     int
	diff    []string // the selected file's diff, one entry per line
	diffFor string   // the path diff belongs to
	scroll  int
	loading bool
	err     string
	gen     int // bumped per load, so a slow reply for an old selection is dropped
}

type (
	reviewFilesMsg struct {
		gen   int
		files []git.Change
		err   error
	}
	reviewDiffMsg struct {
		gen  int
		path string
		diff string
		err  error
	}
	reviewEditDoneMsg struct{}
)

const (
	keyReview      = "ctrl+d" // in the list
	keyFocusReview = "alt+d"  // in focus mode
)

// openReview opens the review panel on a session's working tree.
func (m Model) openReview(s models.Session) (Model, tea.Cmd) {
	if s.Path == "" || s.Status == models.StatusSaved {
		m.statusNote = "nothing to review: the session is not running"
		return m, nil
	}
	title := childLabel(s)
	if repo := groupKeyOf(s); repo != "" && repo != title {
		title = repo + " / " + title
	}
	if s.Details.GitBranch != "" {
		title += " · " + s.Details.GitBranch
	}
	m.review = reviewState{open: true, dir: s.Path, title: title, loading: true, gen: m.review.gen + 1}
	return m, loadReviewFiles(m.review.dir, m.review.gen)
}

func loadReviewFiles(dir string, gen int) tea.Cmd {
	return func() tea.Msg {
		files, err := git.Changes(dir)
		return reviewFilesMsg{gen: gen, files: files, err: err}
	}
}

func loadReviewDiff(dir string, c git.Change, gen int) tea.Cmd {
	return func() tea.Msg {
		d, err := git.FileDiff(dir, c)
		return reviewDiffMsg{gen: gen, path: c.Path, diff: d, err: err}
	}
}

// updateReview handles the review panel's messages.
func (m Model) updateReview(msg tea.Msg) (Model, tea.Cmd) {
	r := &m.review
	switch msg := msg.(type) {
	case reviewFilesMsg:
		if !r.open || msg.gen != r.gen {
			return m, nil
		}
		r.loading = false
		if msg.err != nil {
			r.err = "not a git working tree"
			return m, nil
		}
		// Keep the selection on the same file across a refresh.
		prev := ""
		if r.sel < len(r.files) {
			prev = r.files[r.sel].Path
		}
		r.files, r.sel = msg.files, 0
		for i, f := range r.files {
			if f.Path == prev {
				r.sel = i
			}
		}
		return m, m.reviewSelect(r.sel)
	case reviewDiffMsg:
		if !r.open || msg.gen != r.gen || r.sel >= len(r.files) || r.files[r.sel].Path != msg.path {
			return m, nil
		}
		r.diffFor = msg.path
		if msg.err != nil {
			r.diff = []string{mutedStyle().Render("could not read the diff: " + msg.err.Error())}
			return m, nil
		}
		r.diff = cleanDiff(msg.diff)
		return m, nil
	case reviewEditDoneMsg:
		if !r.open {
			return m, nil
		}
		r.gen++
		return m, loadReviewFiles(r.dir, r.gen)
	}
	return m, nil
}

// reviewSelect selects file i and loads its diff.
func (m *Model) reviewSelect(i int) tea.Cmd {
	r := &m.review
	if len(r.files) == 0 {
		r.diff, r.diffFor = nil, ""
		return nil
	}
	i = min(max(i, 0), len(r.files)-1)
	if i == r.sel && r.diffFor == r.files[i].Path {
		return nil
	}
	r.sel, r.scroll = i, 0
	r.gen++
	return loadReviewDiff(r.dir, r.files[i], r.gen)
}

// cleanDiff drops git's per-file preamble — the file is already named in the
// list — and splits the rest into lines.
func cleanDiff(d string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(d, "\n"), "\n") {
		plain := ansi.Strip(line)
		switch {
		case strings.HasPrefix(plain, "diff --git"), strings.HasPrefix(plain, "index "),
			strings.HasPrefix(plain, "--- "), strings.HasPrefix(plain, "+++ "),
			strings.HasPrefix(plain, "new file mode"), strings.HasPrefix(plain, "deleted file mode"),
			strings.HasPrefix(plain, "similarity index"), strings.HasPrefix(plain, "old mode"),
			strings.HasPrefix(plain, "new mode"):
			continue
		}
		out = append(out, strings.ReplaceAll(line, "\t", "    "))
	}
	return out
}

// handleReviewKey drives the open review panel.
func (m Model) handleReviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &m.review
	page := max(m.reviewBodyHeight()-2, 1)
	switch msg.String() {
	case keyEscape, "q", keyReview, keyFocusReview:
		r.open = false
		return m, nil
	case keyUp, "k":
		return m, m.reviewSelect(r.sel - 1)
	case keyDown, "j":
		return m, m.reviewSelect(r.sel + 1)
	case "pgdown", "space", "ctrl+f":
		r.scroll = min(r.scroll+page, max(len(r.diff)-1, 0))
	case "pgup", "ctrl+b":
		r.scroll = max(r.scroll-page, 0)
	case "shift+down", "J":
		r.scroll = min(r.scroll+1, max(len(r.diff)-1, 0))
	case "shift+up", "K":
		r.scroll = max(r.scroll-1, 0)
	case "home", "g":
		r.scroll = 0
	case "end", "G":
		r.scroll = max(len(r.diff)-page, 0)
	case "r":
		r.loading = true
		r.gen++
		return m, loadReviewFiles(r.dir, r.gen)
	case keyEnter, "e":
		if r.sel < len(r.files) {
			c := exec.Command(resolveEditor(), filepath.Join(r.dir, r.files[r.sel].Path))
			return m, tea.ExecProcess(c, func(error) tea.Msg { return reviewEditDoneMsg{} })
		}
	}
	return m, nil
}

// reviewSize is the review panel's outer size: nearly the whole frame, since a
// diff wants every column it can get.
func (m Model) reviewSize() (int, int) {
	return max(min(m.width-4, 200), 20), max(m.height-4, 8)
}

// reviewBodyHeight is how many rows the file list and diff get: the panel less
// its border, padding, header, rule and footer.
func (m Model) reviewBodyHeight() int {
	_, h := m.reviewSize()
	return max(h-2-2-3, 1)
}

// renderReviewOverlay draws the review panel.
func (m Model) renderReviewOverlay() string {
	c := theme.Current().Colors
	r := m.review
	w, h := m.reviewSize()
	iw := w - 2 - 4 // border, then two columns of padding each side
	bodyH := m.reviewBodyHeight()
	bg := lipgloss.NewStyle().Background(c.Overlay)

	// Header: what is being reviewed, and how much changed.
	title := bg.Foreground(c.Primary).Bold(true).Render("Review") +
		bg.Foreground(c.Muted).Render("  ") + bg.Foreground(c.Foreground).Render(r.title)
	added, removed := 0, 0
	for _, f := range r.files {
		added += f.Added
		removed += f.Removed
	}
	noun := "files"
	if len(r.files) == 1 {
		noun = "file"
	}
	stats := bg.Foreground(c.Subtle).Render(fmt.Sprintf("%d %s  ", len(r.files), noun)) +
		bg.Foreground(c.Success).Render(fmt.Sprintf("+%d", added)) +
		bg.Foreground(c.Muted).Render(" ") +
		bg.Foreground(c.Error).Render(fmt.Sprintf("−%d", removed))
	if lipgloss.Width(title)+lipgloss.Width(stats)+2 > iw {
		title = ansi.Truncate(title, max(iw-lipgloss.Width(stats)-2, 1), ellipsis)
	}
	header := title + bg.Render(strings.Repeat(" ", max(iw-lipgloss.Width(title)-lipgloss.Width(stats), 1))) + stats

	var body []string
	switch {
	case r.loading && len(r.files) == 0:
		body = []string{mutedStyle().Render("reading changes…")}
	case r.err != "":
		body = []string{lipgloss.NewStyle().Foreground(c.Error).Render(r.err)}
	case len(r.files) == 0:
		body = []string{
			bg.Foreground(c.Success).Render("✓ ") + bg.Foreground(c.Foreground).Render("No uncommitted changes."),
			"",
			mutedStyle().Render("The working tree matches HEAD: anything this agent did is committed."),
		}
	default:
		body = m.reviewColumns(iw, bodyH)
	}
	for len(body) < bodyH {
		body = append(body, "")
	}
	body = body[:bodyH]

	keys := []hint{{"↑/↓", "File"}, {"PgUp/PgDn", "Scroll"}, {"Enter", "Open in editor"}, {"r", "Refresh"}, {"Esc", "Close"}}
	var parts []string
	for _, k := range keys {
		parts = append(parts, bg.Foreground(c.Accent).Bold(true).Render(k.key)+bg.Foreground(c.Muted).Render(" "+k.label))
	}
	footer := strings.Join(parts, bg.Foreground(c.Border).Render("  ·  "))

	content := header + "\n" + fadingRule(iw, c.GradientFrom, c.Overlay) + "\n" +
		strings.Join(body, "\n") + "\n\n" + footer
	return fitBox(dialogStyle().Padding(1, 2), w, h).Render(onPlane(content, c.Overlay))
}

// reviewColumns lays out the file list beside the diff.
func (m Model) reviewColumns(iw, bodyH int) []string {
	c := theme.Current().Colors
	r := m.review
	listW := min(max(iw/3, 24), 44)
	diffW := max(iw-listW-3, 10)
	bg := lipgloss.NewStyle().Background(c.Overlay)
	sep := bg.Foreground(c.Border).Render(" │ ")

	// The list scrolls to keep the selection in view.
	start := 0
	if r.sel >= bodyH {
		start = r.sel - bodyH + 1
	}
	var list []string
	for i := start; i < len(r.files) && len(list) < bodyH; i++ {
		list = append(list, m.reviewFileRow(r.files[i], i == r.sel, listW))
	}

	var diff []string
	if r.diffFor != "" {
		end := min(r.scroll+bodyH, len(r.diff))
		for _, line := range r.diff[min(r.scroll, end):end] {
			if ansi.StringWidth(line) > diffW {
				line = ansi.Truncate(line, diffW, "")
			}
			diff = append(diff, line)
		}
	} else {
		diff = []string{mutedStyle().Render("loading diff…")}
	}

	out := make([]string, bodyH)
	for y := range bodyH {
		left := ""
		if y < len(list) {
			left = list[y]
		}
		left += bg.Render(strings.Repeat(" ", max(listW-lipgloss.Width(left), 0)))
		right := ""
		if y < len(diff) {
			right = diff[y]
		}
		out[y] = left + sep + right
	}
	return out
}

// reviewFileRow is one file in the list: a status glyph, the path — shortened
// from the left, since the file name is the part worth keeping — and its
// line counts.
func (m Model) reviewFileRow(f git.Change, selected bool, width int) string {
	c := theme.Current().Colors
	rowBg := c.Overlay
	if selected {
		rowBg = c.SelBg
	}
	bg := lipgloss.NewStyle().Background(rowBg)

	glyph, glyphColor := "M", c.Warning
	switch {
	case f.Untracked || f.Status[0] == 'A':
		glyph, glyphColor = "A", c.Success
	case f.Status[0] == 'D' || f.Status[1] == 'D':
		glyph, glyphColor = "D", c.Error
	case f.Status[0] == 'R':
		glyph, glyphColor = "R", c.Secondary
	}
	counts := ""
	switch {
	case f.Binary:
		counts = bg.Foreground(c.Muted).Render("bin")
	default:
		counts = bg.Foreground(c.Success).Render(fmt.Sprintf("+%d", f.Added))
		if f.Removed > 0 {
			counts += bg.Foreground(c.Error).Render(fmt.Sprintf(" −%d", f.Removed))
		}
	}
	pathW := max(width-2-lipgloss.Width(counts)-1, 4)
	path := f.Path
	if lipgloss.Width(path) > pathW {
		runes := []rune(path)
		for lipgloss.Width(string(runes)) > pathW-1 && len(runes) > 1 {
			runes = runes[1:]
		}
		path = ellipsis + string(runes)
	}
	nameStyle := bg.Foreground(c.Foreground)
	if selected {
		nameStyle = nameStyle.Bold(true)
	}
	row := bg.Foreground(glyphColor).Bold(true).Render(glyph) + bg.Render(" ") + nameStyle.Render(path)
	gap := max(width-lipgloss.Width(row)-lipgloss.Width(counts), 1)
	return row + bg.Render(strings.Repeat(" ", gap)) + counts
}
