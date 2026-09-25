package notifs

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/nemanja-micovic/nagare-go/internal/config"
	"github.com/nemanja-micovic/nagare-go/internal/notifications"
	"github.com/nemanja-micovic/nagare-go/internal/session"
	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

// Model is the Bubble Tea model for the notification center TUI.
type Model struct {
	store       *notifications.Store
	items       []notifications.Notification
	cursor      int
	tab         int // 0 = notifications, 1 = settings
	width       int
	height      int
	cfg         config.NagareConfig
	settingsCur int // cursor in settings tab
	editInput   textinput.Model
	editMode    bool
	editField   string
}

// New creates a new notification center model.
func New() Model {
	cfg, _ := config.Load()
	store := notifications.NewStore(notifications.DefaultStorePath())

	ti := textinput.New()
	ti.CharLimit = 10

	return Model{
		store:     store,
		items:     store.ListAll(),
		cfg:       cfg,
		editInput: ti,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Edit mode for int settings
	if m.editMode {
		switch key {
		case "esc":
			m.editMode = false
			m.editInput.SetValue("")
			return m, nil
		case "enter":
			m.saveIntSetting()
			m.editMode = false
			m.editInput.SetValue("")
			return m, nil
		default:
			var cmd tea.Cmd
			m.editInput, cmd = m.editInput.Update(msg)
			return m, cmd
		}
	}

	switch key {
	case "esc", "q":
		return m, tea.Quit
	case "1":
		m.tab = 0
		m.cursor = 0
		return m, nil
	case "2":
		m.tab = 1
		m.cursor = 0
		return m, nil
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down":
		maxItems := m.maxItemsForTab()
		if m.cursor < maxItems-1 {
			m.cursor++
		}
		return m, nil
	case "d":
		if m.tab == 0 && len(m.items) > 0 && m.cursor < len(m.items) {
			m.store.Dismiss(m.items[m.cursor].ID)
			m.items = m.store.ListAll()
			if m.cursor >= len(m.items) && m.cursor > 0 {
				m.cursor--
			}
		}
		return m, nil
	case "D":
		if m.tab == 0 {
			m.store.DismissAll()
			m.items = m.store.ListAll()
			m.cursor = 0
		}
		return m, nil
	case "enter":
		if m.tab == 0 {
			return m.handleNotificationEnter()
		}
		return m.handleSettingsEnter()
	}

	return m, nil
}

func (m Model) maxItemsForTab() int {
	if m.tab == 0 {
		return len(m.items)
	}
	return 14 // 12 settings + 2 separators
}

func (m Model) handleNotificationEnter() (tea.Model, tea.Cmd) {
	if len(m.items) == 0 || m.cursor >= len(m.items) {
		return m, nil
	}

	item := m.items[m.cursor]
	m.store.MarkRead(item.ID)
	m.items = m.store.ListAll()

	// Jump to session
	session.SwitchToSession(item.SessionName)
	return m, tea.Quit
}

func (m Model) handleSettingsEnter() (tea.Model, tea.Cmd) {
	item := m.getSettingsItem(m.cursor)
	if item == nil {
		return m, nil
	}

	if item.isBool {
		// Toggle bool setting
		*item.boolPtr = !*item.boolPtr
		m.saveConfig()
	} else {
		// Start editing int
		m.editMode = true
		m.editField = item.field
		m.editInput.SetValue(fmt.Sprintf("%d", *item.intPtr))
		m.editInput.Focus()
	}

	return m, nil
}

type settingsItem struct {
	label   string
	isBool  bool
	boolPtr *bool
	intPtr  *int
	field   string
}

func (m Model) getSettingsItem(idx int) *settingsItem {
	cfg := &m.cfg
	ni := &cfg.Notifications.NeedsInput
	tc := &cfg.Notifications.TaskComplete

	items := []settingsItem{
		{"Notifications enabled", true, &cfg.Notifications.Enabled, nil, "enabled"},
		{"", false, nil, nil, ""}, // separator
		{"Toast notification", true, &ni.Toast, nil, "needs_input.toast"},
		{"Bell", true, &ni.Bell, nil, "needs_input.bell"},
		{"OS notification", true, &ni.OsNotify, nil, "needs_input.os_notify"},
		{"Popup notification", true, &ni.Popup, nil, "needs_input.popup"},
		{"Popup timeout", false, nil, &ni.PopupTimeout, "needs_input.popup_timeout"},
		{"", false, nil, nil, ""}, // separator
		{"Toast notification", true, &tc.Toast, nil, "task_complete.toast"},
		{"Bell", true, &tc.Bell, nil, "task_complete.bell"},
		{"OS notification", true, &tc.OsNotify, nil, "task_complete.os_notify"},
		{"Popup notification", true, &tc.Popup, nil, "task_complete.popup"},
		{"Popup timeout", false, nil, &tc.PopupTimeout, "task_complete.popup_timeout"},
		{"Min working seconds", false, nil, &tc.MinWorkingSeconds, "task_complete.min_working_seconds"},
	}

	if idx < 0 || idx >= len(items) {
		return nil
	}
	return &items[idx]
}

func (m *Model) saveIntSetting() {
	val, err := strconv.Atoi(strings.TrimSpace(m.editInput.Value()))
	if err != nil || val < 0 {
		return
	}

	item := m.getSettingsItem(m.cursor)
	if item != nil && item.intPtr != nil {
		*item.intPtr = val
		m.saveConfig()
	}
}

func (m Model) saveConfig() {
	if err := config.Save(m.cfg); err != nil {
		// Log error silently
		_ = err
	}
}

func (m Model) View() tea.View {
	content := m.view()
	// lipgloss Height only pads up to its target, never truncates, so a long
	// notification list can render past the last row — which scrolls the alt
	// screen and smears the UI. Clamp the assembled frame.
	if m.width > 0 && m.height > 0 {
		content = lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(content)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m Model) view() string {
	if m.width == 0 {
		return "Loading..."
	}

	c := theme.Current().Colors

	var b strings.Builder

	// Measure the chrome rather than assuming it is three rows: the hint bar
	// wraps to two on a narrow terminal, and an assumed figure left the content
	// over-budget with the clamp in View silently eating the bar.
	tabBar := m.renderTabBar()
	hintBar := m.renderHintBar()
	contentHeight := m.height - lipgloss.Height(tabBar) - lipgloss.Height(hintBar)
	if contentHeight < 1 {
		contentHeight = 1
	}

	b.WriteString(tabBar)
	b.WriteString("\n")
	if m.tab == 0 {
		b.WriteString(m.renderNotifications(m.width, contentHeight))
	} else {
		b.WriteString(m.renderSettings(m.width, contentHeight))
	}
	b.WriteString("\n")
	b.WriteString(hintBar)

	return lipgloss.NewStyle().
		Background(c.Background).
		Foreground(c.Foreground).
		Width(m.width).
		Height(m.height).
		Render(b.String())
}

func (m Model) renderTabBar() string {
	c := theme.Current().Colors

	tab1Style := lipgloss.NewStyle().
		Foreground(c.Foreground).
		Background(c.Background)
	tab2Style := lipgloss.NewStyle().
		Foreground(c.Foreground).
		Background(c.Background)

	if m.tab == 0 {
		tab1Style = tab1Style.Foreground(c.Background).Background(c.Primary).Bold(true)
	} else {
		tab2Style = tab2Style.Foreground(c.Background).Background(c.Primary).Bold(true)
	}

	tab1 := tab1Style.Padding(0, 2).Render("[1] Notifications")
	tab2 := tab2Style.Padding(0, 2).Render("[2] Settings")

	return lipgloss.JoinHorizontal(lipgloss.Top, tab1, "  ", tab2)
}

// rowWindow picks which blocks to show so the block at cursor stays visible and
// the total rendered height stays within height rows.
//
// Windowing has to be measured in rows and not counted in items. A notification
// is two rows — three when its message wraps — so treating the row budget as an
// item count rendered about twice the rows it had, and the clamp in View hid that
// by cutting the hint bar off the bottom of the screen.
//
// It grows upward first, which pins the cursor to the bottom edge when scrolling
// down — the same behaviour as the picker's list.
func rowWindow(heights []int, cursor, height int) (start, end int) {
	if len(heights) == 0 {
		return 0, 0
	}
	cursor = min(max(cursor, 0), len(heights)-1)

	used := heights[cursor]
	start, end = cursor, cursor+1
	for start > 0 && used+heights[start-1] <= height {
		start--
		used += heights[start]
	}
	for end < len(heights) && used+heights[end] <= height {
		used += heights[end]
		end++
	}
	return start, end
}

func (m Model) renderNotifications(width, height int) string {
	if len(m.items) == 0 {
		return lipgloss.NewStyle().
			Foreground(theme.Current().Colors.Muted).
			Render("  No notifications")
	}

	blocks := make([]string, len(m.items))
	heights := make([]int, len(m.items))
	for i, item := range m.items {
		blocks[i] = m.renderNotificationItem(item, i == m.cursor)
		heights[i] = lipgloss.Height(lipgloss.NewStyle().Width(width).Render(blocks[i]))
	}

	start, end := rowWindow(heights, m.cursor, height)
	return strings.Join(blocks[start:end], "\n")
}

func (m Model) renderNotificationItem(item notifications.Notification, selected bool) string {
	c := theme.Current().Colors

	// Read dot
	readDot := " "
	if !item.Read {
		readDot = lipgloss.NewStyle().Foreground(c.Primary).Render("●")
	}

	// Icon
	icon := "⏳"
	if strings.Contains(item.Message, "finished") || strings.Contains(item.Message, "✅") {
		icon = "✅"
	}

	// Timestamp (first 19 chars: YYYY-MM-DD HH:MM:SS)
	ts := item.Timestamp
	if len(ts) > 19 {
		ts = ts[:19]
	}

	// Format lines
	line1 := fmt.Sprintf(" %s %s %s  %s", readDot, icon, item.SessionName, item.Message)
	line2 := fmt.Sprintf("    %s", lipgloss.NewStyle().Foreground(c.Muted).Render(ts))

	if selected {
		// Highlight both lines
		line1 = lipgloss.NewStyle().
			Background(c.Primary).
			Foreground(c.Background).
			Bold(true).
			Render(line1)
		line2 = lipgloss.NewStyle().
			Background(c.Primary).
			Foreground(c.Background).
			Render(line2)
	}

	return line1 + "\n" + line2
}

// renderSettings lays the settings list out in at most height rows, scrolled to
// keep the selected item visible.
//
// It used to ignore height entirely and always emit its full ~19 rows, which
// overflowed any terminal shorter than that — and since the frame clamp trims
// from the bottom, the row it removed was the hint bar.
func (m Model) renderSettings(width, height int) string {
	c := theme.Current().Colors
	sectionHeader := lipgloss.NewStyle().Foreground(c.Accent).Bold(true)

	// Rows rather than lines: a long label on a narrow terminal wraps, and a
	// window over unwrapped lines would be over budget again. Wrapping here also
	// extends the selected item's highlight across the full row, as list rows do
	// elsewhere.
	wrap := lipgloss.NewStyle().Width(width)
	var rows []string
	cursorRow := 0
	push := func(s string, isCursor bool) {
		if isCursor {
			cursorRow = len(rows)
		}
		rows = append(rows, strings.Split(wrap.Render(s), "\n")...)
	}

	push(sectionHeader.Render("  Master"), false)

	for i := 0; i < 14; i++ {
		item := m.getSettingsItem(i)
		if item == nil {
			continue
		}

		// Separators + section headers
		if item.label == "" {
			push("", false)
			if i == 1 {
				push(sectionHeader.Render("  Needs Input"), false)
			}
			if i == 7 {
				push(sectionHeader.Render("  Task Complete"), false)
			}
			continue
		}

		push(m.renderSettingsItem(item, i == m.cursor), i == m.cursor)
	}

	// Add edit input if in edit mode
	if m.editMode {
		push("", false)
		push(lipgloss.NewStyle().Foreground(c.Primary).
			Render("  Edit: "+m.editInput.View()), false)
	}

	start := 0
	if cursorRow >= height {
		start = cursorRow - height + 1
	}
	end := min(start+height, len(rows))
	return strings.Join(rows[start:end], "\n")
}

func (m Model) renderSettingsItem(item *settingsItem, selected bool) string {
	c := theme.Current().Colors

	var line string
	if item.isBool {
		check := "[ ]"
		if *item.boolPtr {
			check = "[x]"
		}
		line = fmt.Sprintf("  %s %s", check, item.label)
	} else {
		line = fmt.Sprintf("  %s: %d", item.label, *item.intPtr)
	}

	if selected {
		line = lipgloss.NewStyle().
			Background(c.Primary).
			Foreground(c.Background).
			Bold(true).
			Render("  " + line)
	}

	return line
}

func (m Model) renderHintBar() string {
	c := theme.Current().Colors
	key := lipgloss.NewStyle().Foreground(c.Accent).Bold(true)
	sep := lipgloss.NewStyle().Foreground(c.Muted).Render(" │ ")

	var parts []string
	if m.tab == 0 {
		parts = append(parts, key.Render("Enter")+" Jump")
		parts = append(parts, key.Render("d")+" Dismiss")
		parts = append(parts, key.Render("D")+" Dismiss All")
	} else {
		parts = append(parts, key.Render("Enter")+" Toggle/Edit")
	}
	parts = append(parts, key.Render("1/2")+" Tab")

	// The way out is reserved space, never trimmed. The bar has to fit one row —
	// it wrapped to two on a narrow terminal, pushing the frame over its height
	// and costing the content a row it had already budgeted — but trimming from
	// the end took "Esc Quit" with it, leaving no visible way to leave.
	quit := key.Render("Esc") + " Quit"
	budget := m.width - 2 - lipgloss.Width(quit) - lipgloss.Width(sep)
	for len(parts) > 0 && lipgloss.Width(strings.Join(parts, sep)) > budget {
		parts = parts[:len(parts)-1]
	}
	bar := quit
	if len(parts) > 0 {
		bar = strings.Join(append(parts, quit), sep)
	}

	return lipgloss.NewStyle().
		Foreground(c.Muted).
		Background(c.Background).
		Width(m.width).
		MaxHeight(1).
		Padding(0, 1).
		Render(bar)
}
