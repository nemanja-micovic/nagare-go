package newsession

import (
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"
	"github.com/nemke/nagare-go/internal/session"
	"github.com/nemke/nagare-go/internal/theme"
)

// QuickModel is the quick-prototype form (name + agent only).
type QuickModel struct {
	form    *huh.Form
	state   *formState
	width   int
	height  int
	err     error
	done    bool
	stay    bool   // hand the session back instead of switching to it
	created string // tmux session the form created, once it has
}

// NewQuick creates a quick-prototype form model.
func NewQuick() QuickModel {
	s := &formState{agent: "claude"}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Prototype name").
				Placeholder("my-prototype").
				Value(&s.name).
				Validate(func(v string) error {
					if strings.TrimSpace(v) == "" {
						return errEmptyName
					}
					return nil
				}),

			huh.NewSelect[string]().
				Title("Agent").
				Options(huh.NewOptions("claude", "codex", "opencode", "pi", "omp")...).
				Value(&s.agent),
		),
	).WithTheme(formTheme()).WithShowHelp(true)

	return QuickModel{form: form, state: s}
}

func (m QuickModel) Init() tea.Cmd {
	return m.form.Init()
}

func (m QuickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		name := strings.TrimSpace(m.state.name)
		sessionName, err := session.Create(name, name, m.state.agent, false)
		if err != nil {
			m.err = err
			return m, nil
		}
		m.created = sessionName
		if !m.stay {
			session.SwitchToSession(sessionName)
		}
		return m, tea.Quit
	}

	return m, cmd
}

func (m QuickModel) View() tea.View {
	v := tea.NewView(clampFrame(m.view(), m.width, m.height))
	v.AltScreen = true
	return v
}

func (m QuickModel) view() string {
	if m.width == 0 {
		return "Loading..."
	}

	c := theme.Current().Colors
	title := lipgloss.NewStyle().Foreground(c.Primary).Bold(true).Render("Quick Prototype")

	body := m.form.View()
	if m.err != nil {
		body += "\n" + lipgloss.NewStyle().Foreground(c.Error).Render("Error: "+m.err.Error())
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

// Stay makes the form hand the new session back through Created rather than
// switching the terminal to it — for when nagare will open it itself.
func (m QuickModel) Stay() QuickModel {
	m.stay = true
	return m
}

// Created returns the tmux session the form created, or "" if it was cancelled.
func (m QuickModel) Created() string {
	return m.created
}
