package newsession

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"
	"github.com/nemke/nagare-go/internal/config"
	"github.com/nemke/nagare-go/internal/session"
	"github.com/nemke/nagare-go/internal/state"
	"github.com/nemke/nagare-go/internal/theme"
)

const customPathSentinel = "__custom__"

type formState struct {
	path       string // sentinel or one of the preset options
	customPath string // used only when path == customPathSentinel
	name       string
	agent      string
	resume     bool
}

// Model is the full new-session form.
type Model struct {
	form    *huh.Form
	state   *formState
	width   int
	height  int
	err     error
	done    bool
	stay    bool   // hand the session back instead of switching to it
	created string // tmux session the form created, once it has
}

// New creates a new session form model.
func New() Model {
	s := &formState{
		agent:  "claude",
		resume: true,
	}

	pathOptions := buildPathOptions()

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Project directory").
				Description("Type to fuzzy-filter · Ctrl+n/Ctrl+p or ↑/↓ to navigate").
				Options(pathOptions...).
				Filtering(true).
				Height(10).
				Value(&s.path),
		),
		// Shown only when the user picks "Custom…" from the list above.
		huh.NewGroup(
			huh.NewInput().
				Title("Custom path").
				Description("Type a full path (supports ~)").
				Placeholder("~/Projects/my-project").
				Value(&s.customPath),
		).WithHideFunc(func() bool { return s.path != customPathSentinel }),
		huh.NewGroup(
			huh.NewInput().
				Title("Session name").
				Description("Leave empty to use the directory name").
				Placeholder("my-project").
				Value(&s.name),

			huh.NewSelect[string]().
				Title("Agent").
				Options(huh.NewOptions("claude", "codex", "opencode", "gemini", "crush", "pi", "omp")...).
				Value(&s.agent),

			huh.NewConfirm().
				Title("Continue previous session?").
				Affirmative("Continue").
				Negative("Fresh").
				Value(&s.resume),
		),
	).WithTheme(formTheme()).WithShowHelp(true)

	return Model{form: form, state: s}
}

// buildPathOptions collects candidate project directories from:
//  1. The session registry, sorted by most recent access.
//  2. Immediate subdirectories of the configured quick-project path.
//
// A sentinel "Custom…" option is appended so users can still enter an
// arbitrary path that isn't in the list.
func buildPathOptions() []huh.Option[string] {
	seen := make(map[string]bool)
	var paths []string

	// Recent projects from registry.
	reg := state.NewRegistry(state.DefaultRegistryPath())
	recents := reg.ListAll()
	sort.Slice(recents, func(i, j int) bool {
		return recents[i].LastAccessed > recents[j].LastAccessed
	})
	for _, r := range recents {
		p := session.ExpandPath(r.Path)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}

	// Subdirectories of the quick-project root.
	cfg, _ := config.Load()
	root := session.ExpandPath(cfg.Picker.QuickProjectPath)
	if entries, err := os.ReadDir(root); err == nil {
		var subs []string
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(root, e.Name())
			if seen[p] {
				continue
			}
			seen[p] = true
			subs = append(subs, p)
		}
		sort.Strings(subs)
		paths = append(paths, subs...)
	}

	opts := make([]huh.Option[string], 0, len(paths)+1)
	for _, p := range paths {
		opts = append(opts, huh.NewOption(prettyPath(p), p))
	}
	opts = append(opts, huh.NewOption("Custom path…", customPathSentinel))
	return opts
}

// prettyPath renders a path with $HOME collapsed to "~" for display.
func prettyPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

func (m Model) Init() tea.Cmd {
	return m.form.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Tell the form how much room it has. huh lays out at its own default
		// width otherwise — about 98 cells — so on any narrower terminal the box
		// was wider than the screen and every row wrapped. lipgloss.Place pads to
		// the frame but never truncates, so nothing downstream caught it.
		m.form = m.form.WithWidth(formWidth(msg.Width)).WithHeight(formHeight(msg.Height))
	case tea.KeyMsg:
		if msg.String() == "esc" {
			return m, tea.Quit
		}
	}

	next, cmd := m.form.Update(msg)
	if f, ok := next.(*huh.Form); ok {
		m.form = f
	}

	if m.form.State == huh.StateCompleted && !m.done {
		m.done = true
		path := m.state.path
		if path == customPathSentinel {
			path = strings.TrimSpace(m.state.customPath)
		}
		name, err := session.Create(path, m.state.name, m.state.agent, m.state.resume)
		if err != nil {
			m.err = err
			return m, nil
		}
		m.created = name
		if !m.stay {
			session.SwitchToSession(name)
		}
		return m, tea.Quit
	}

	return m, cmd
}

func (m Model) View() tea.View {
	v := tea.NewView(clampFrame(m.view(), m.width, m.height))
	v.AltScreen = true
	return v
}

func (m Model) view() string {
	if m.width == 0 {
		return "Loading..."
	}

	c := theme.Current().Colors
	title := lipgloss.NewStyle().Foreground(c.Primary).Bold(true).Render("New Session")

	body := m.form.View()
	if m.err != nil {
		body += "\n" + lipgloss.NewStyle().Foreground(c.Error).Render("Error: "+m.err.Error())
	}
	if rp := m.resolvedPath(); rp != "" && m.form.State != huh.StateCompleted {
		_, statErr := os.Stat(rp)
		badge := lipgloss.NewStyle().Foreground(c.Success).Render(" (exists)")
		if os.IsNotExist(statErr) {
			badge = lipgloss.NewStyle().Foreground(c.Warning).Render(" (will be created)")
		}
		body += fmt.Sprintf("\n→ %s%s",
			lipgloss.NewStyle().Foreground(c.Accent).Bold(true).Render(rp),
			badge)
	}

	box := lipgloss.NewStyle().
		Background(c.Background).
		Foreground(c.Foreground).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(c.Border).
		Padding(1, 2).
		Render(title + "\n\n" + body)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) resolvedPath() string {
	path := m.state.path
	if path == customPathSentinel {
		path = strings.TrimSpace(m.state.customPath)
	}
	if path == "" {
		return ""
	}
	if m.state.name != "" {
		path = filepath.Join(path, m.state.name)
	}
	return session.ExpandPath(session.ResolvePath(path))
}

// Stay makes the form hand the new session back through Created rather than
// switching the terminal to it — for when nagare will open it itself.
func (m Model) Stay() Model {
	m.stay = true
	return m
}

// Created returns the tmux session the form created, or "" if it was cancelled.
func (m Model) Created() string {
	return m.created
}
