package picker

import (
	"encoding/json"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/nemke/nagare-go/internal/fsutil"
	"github.com/nemke/nagare-go/internal/log"
	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/paths"
)

// Layout restore: nagare reopens the tiles you last had open, as long as their
// agents are still running. nagare is meant to be the place work happens, so
// starting it should put you back where you were — not in front of a list you
// then navigate back to the same spot.
//
// The layout is remembered whenever focus mode closes: leaving it for the list,
// or quitting while in it. Focus mode has no quit key of its own (Esc belongs
// to the agent), so "quit from the list forgets the layout" would forget it
// every time. Closing the last tile with Alt+x is the deliberate "done" that
// forgets it.

type savedLayout struct {
	Tiles  []savedTile `json:"tiles"`
	Active int         `json:"active"`
	Zoom   bool        `json:"zoom"`
}

// savedTile names an agent by pane id, plus enough to recognise a stale match:
// tmux reuses pane ids after a server restart, so an id alone could reopen a
// different agent that happens to hold it now.
type savedTile struct {
	Pane    string `json:"pane"`
	Session string `json:"session"`
	Agent   string `json:"agent"`
}

func layoutPath() string { return filepath.Join(paths.Data(), "layout.json") }

// saveLayout remembers the focus layout. A shell tile is saved as its agent:
// the shell is a moment, the agent is the work.
func (m Model) saveLayout() {
	if !m.restoreEnabled || !m.focus.on || m.focus.n == 0 {
		return
	}
	l := savedLayout{Active: m.focus.active, Zoom: m.focus.zoom}
	for i := range m.focus.n {
		s, ok := m.sessionFor(m.focus.tiles[i].key)
		if !ok || s.PaneID == "" {
			continue
		}
		l.Tiles = append(l.Tiles, savedTile{Pane: s.PaneID, Session: s.SessionName, Agent: string(s.AgentType)})
	}
	if len(l.Tiles) == 0 {
		return
	}
	data, err := json.Marshal(l)
	if err != nil {
		return
	}
	if err := fsutil.AtomicWrite(layoutPath(), data, 0o644); err != nil {
		log.Error("save layout: %v", err)
	}
}

// forgetLayout drops the remembered layout.
func (m Model) forgetLayout() {
	if m.restoreEnabled {
		os.Remove(layoutPath())
	}
}

// loadLayoutIf reads the saved layout when restoring is enabled.
func loadLayoutIf(enabled bool) *savedLayout {
	if !enabled {
		return nil
	}
	return loadLayout()
}

// loadLayout reads the saved layout, if there is one.
func loadLayout() *savedLayout {
	data, err := os.ReadFile(layoutPath())
	if err != nil {
		return nil
	}
	var l savedLayout
	if json.Unmarshal(data, &l) != nil || len(l.Tiles) == 0 {
		return nil
	}
	return &l
}

// restoreLayout reopens a saved layout once the first scan has listed the
// agents. Tiles whose agents are gone, or that no longer fit, are skipped; with
// none left nagare simply starts on the list.
func (m Model) restoreLayout() (Model, tea.Cmd) {
	l := m.restore
	m.restore = nil
	if l == nil || m.focus.on || m.pendingFocus != "" {
		return m, nil
	}
	var cmds []tea.Cmd
	active := -1
	m.focus.zoom = l.Zoom
	for i, st := range l.Tiles {
		if m.focus.on && (m.focus.n == maxTiles || !m.geometryFor(m.focus.n+1).fits()) {
			break
		}
		s, ok := m.savedAgent(st)
		if !ok || m.focus.onScreen(sessionKey(s)) {
			continue
		}
		var cmd tea.Cmd
		if !m.focus.on {
			m, cmd = m.enterFocus(s)
		} else {
			m, cmd = m.addTileFor(s)
		}
		cmds = append(cmds, cmd)
		if i == l.Active {
			active = m.focus.active
		}
	}
	if !m.focus.on {
		m.focus.zoom = false
		return m, nil
	}
	if active >= 0 {
		var cmd tea.Cmd
		m, cmd = m.activateTile(active)
		cmds = append(cmds, cmd)
	}
	log.Info("restored %d tiles", m.focus.n)
	return m, tea.Batch(cmds...)
}

// savedAgent finds the running agent a saved tile names: the same pane, still
// in the same session and running the same agent.
func (m Model) savedAgent(st savedTile) (models.Session, bool) {
	for _, s := range m.sessions {
		if s.PaneID == st.Pane && s.SessionName == st.Session && string(s.AgentType) == st.Agent &&
			s.Status != models.StatusSaved {
			return s, true
		}
	}
	return models.Session{}, false
}
