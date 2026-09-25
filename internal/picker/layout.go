package picker

import (
	"encoding/json"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/nemke/nagare-go/internal/fsutil"
	"github.com/nemke/nagare-go/internal/log"
	"github.com/nemke/nagare-go/internal/paths"
)

// Layout restore: quit nagare in focus mode and the next start reopens the same
// tiles, as long as their agents are still running. nagare is meant to be the
// place work happens, so starting it should put you back where you were — not
// in front of a list you then have to navigate to the same spot.
//
// Quitting from the list forgets the layout: that says the list is where the
// user wanted to be.

type savedLayout struct {
	Tiles  []savedTile `json:"tiles"`
	Active int         `json:"active"`
	Zoom   bool        `json:"zoom"`
}

// savedTile names an agent both ways: the pane id survives window renumbering,
// the session key survives a pane id the scan has not reported yet.
type savedTile struct {
	Pane string `json:"pane"`
	Key  string `json:"key"`
}

func layoutPath() string { return filepath.Join(paths.Data(), "layout.json") }

// saveLayout records the focus layout, or forgets it when nagare is closing on
// the list. A shell tile is saved as its agent: the shell is a moment, the
// agent is the work.
func (m Model) saveLayout() {
	if !m.restoreEnabled {
		return
	}
	path := layoutPath()
	if !m.focus.on || m.focus.n == 0 {
		os.Remove(path)
		return
	}
	l := savedLayout{Active: m.focus.active, Zoom: m.focus.zoom}
	for i := range m.focus.n {
		t := m.focus.tiles[i]
		st := savedTile{Key: t.key, Pane: t.pane}
		if s, ok := m.sessionFor(t.key); ok {
			st.Pane = s.PaneID
		}
		l.Tiles = append(l.Tiles, st)
	}
	data, err := json.Marshal(l)
	if err != nil {
		return
	}
	if err := fsutil.AtomicWrite(path, data, 0o644); err != nil {
		log.Error("save layout: %v", err)
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
// agents. Tiles whose agents are gone are skipped; if none are left, nagare
// simply starts on the list.
func (m Model) restoreLayout() (Model, tea.Cmd) {
	l := m.restore
	m.restore = nil
	if l == nil || m.focus.on || m.pendingFocus != "" {
		return m, nil
	}
	var cmds []tea.Cmd
	restored := 0
	m.focus.zoom = l.Zoom
	for _, st := range l.Tiles {
		if restored == maxTiles {
			break
		}
		for _, s := range m.sessions {
			if (st.Pane != "" && s.PaneID == st.Pane) || sessionKey(s) == st.Key {
				var cmd tea.Cmd
				if restored == 0 {
					m, cmd = m.enterFocus(s)
				} else if m.geometryFor(m.focus.n + 1).fits() {
					m, cmd = m.addTileFor(s)
				}
				cmds = append(cmds, cmd)
				restored++
				break
			}
		}
	}
	if restored == 0 {
		m.focus.zoom = false
		return m, nil
	}
	if l.Active < m.focus.n {
		var cmd tea.Cmd
		m, cmd = m.activateTile(l.Active)
		cmds = append(cmds, cmd)
	}
	log.Info("restored %d tiles", restored)
	return m, tea.Batch(cmds...)
}
