package picker

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemke/nagare-go/internal/git"
	"github.com/nemke/nagare-go/internal/models"
)

func TestPaletteRunsAnAction(t *testing.T) {
	m := newVisualModel(t, 140, 36)
	m = driveModel(t, m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if !m.palette.open {
		t.Fatal("Ctrl+k did not open the palette")
	}
	m = typeString(t, m, "grid view")
	items := m.paletteItems()
	if len(items) == 0 || !strings.Contains(items[0].label, "grid") {
		t.Fatalf("best match for %q is %+v", "grid view", items)
	}
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.palette.open || m.viewMode != GridView {
		t.Errorf("running the action: palette open=%v view=%v", m.palette.open, m.viewMode)
	}
}

func TestPaletteOpensAnAgent(t *testing.T) {
	m, _ := recordingModel(t, 140, 36)
	m = driveModel(t, m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m = typeString(t, m, "open cosmo")
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.focus.on {
		t.Fatal("choosing an agent did not open it")
	}
	if s, _ := m.focusedSession(); s.SessionName != "cosmo" {
		t.Errorf("opened %q, want the cosmo agent", s.Name)
	}
}

// TestPaletteLeadsWithWaitingAgents — with nothing typed, an agent waiting on
// the user is the first thing offered.
func TestPaletteLeadsWithWaitingAgents(t *testing.T) {
	m := newVisualModel(t, 140, 36)
	m, _ = m.openPalette()
	items := m.paletteItems()
	if len(items) == 0 || items[0].session == nil || items[0].session.Status != models.StatusWaitingInput {
		t.Errorf("first entry is %+v, want the waiting agent", items[0])
	}
}

// TestFocusPaletteActionsNeverReachTheAgent — every action the palette offers
// in focus mode is a key nagare keeps for itself. One that leaked through to
// the agent would type into it instead of doing what the palette said.
func TestFocusPaletteActionsNeverReachTheAgent(t *testing.T) {
	base, _ := splitModel(t, 240, 60, 6, 2)
	for _, it := range base.paletteActions() {
		t.Run(it.label, func(t *testing.T) {
			m, rec := splitModel(t, 240, 60, 6, 2)
			m = driveModel(t, m, it.press)
			if sends := rec.sends(m.focus.q); len(sends) != 0 {
				t.Errorf("%q (%s) reached the agent as %q", it.label, it.keys, sends)
			}
		})
	}
}

func TestLeavePress(t *testing.T) {
	cases := map[string]string{"ctrl+]": "ctrl+]", "ctrl+q": "ctrl+q", "alt+q": "alt+q", "f12": "f12", "garbage key": "ctrl+]"}
	for in, want := range cases {
		if got := leavePress(in).String(); got != want {
			t.Errorf("leavePress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPaletteFitsTheFrame(t *testing.T) {
	for _, sz := range [][2]int{{200, 50}, {120, 30}, {80, 20}} {
		m := newVisualModel(t, sz[0], sz[1])
		m, _ = m.openPalette()
		frame, _ := m.view()
		rows := strings.Split(frame, "\n")
		if len(rows) != sz[1] {
			t.Errorf("%dx%d: %d rows", sz[0], sz[1], len(rows))
		}
		for i, r := range rows {
			if w := ansi.StringWidth(r); w != sz[0] {
				t.Fatalf("%dx%d: row %d is %d wide", sz[0], sz[1], i, w)
			}
		}
	}
}

func TestCleanDiffDropsPreamble(t *testing.T) {
	d := "\x1b[1mdiff --git a/x b/x\x1b[m\n\x1b[1mindex 1..2 100644\x1b[m\n\x1b[1m--- a/x\x1b[m\n\x1b[1m+++ b/x\x1b[m\n\x1b[36m@@ -1 +1,2 @@\x1b[m\n a\n\x1b[32m+\tb\x1b[m\n"
	got := cleanDiff(d)
	if len(got) != 3 || !strings.Contains(ansi.Strip(got[0]), "@@") {
		t.Fatalf("cleanDiff = %q", got)
	}
	if strings.Contains(got[2], "\t") {
		t.Error("tabs were not expanded")
	}
}

func TestReviewNavigationAndFrame(t *testing.T) {
	m := newVisualModel(t, 160, 40)
	m.review = reviewState{open: true, dir: "/tmp/x", title: "repo / wt",
		files: []git.Change{
			{Path: "a/very/long/path/that/keeps/going/and/going/into/the/distance/file.go", Status: " M", Added: 3, Removed: 1},
			{Path: "b.go", Status: "??", Untracked: true, Added: 10},
		},
		diff: []string{"@@ -1 +1 @@", "+x"}, diffFor: "a/very/long/path/that/keeps/going/and/going/into/the/distance/file.go"}
	frame, _ := m.view()
	rows := strings.Split(frame, "\n")
	if len(rows) != 40 {
		t.Fatalf("review frame has %d rows", len(rows))
	}
	for i, r := range rows {
		if w := ansi.StringWidth(r); w != 160 {
			t.Fatalf("row %d is %d wide", i, w)
		}
	}
	plain := ansi.Strip(frame)
	if !strings.Contains(plain, "file.go") {
		t.Error("a long path lost its file name when shortened")
	}
	if !strings.Contains(plain, "2 files") || !strings.Contains(plain, "+13") {
		t.Error("the header does not total the changes")
	}

	m2, cmd := m.handleReviewKey(tea.KeyPressMsg{Code: tea.KeyDown})
	mm := m2.(Model)
	if mm.review.sel != 1 || cmd == nil {
		t.Errorf("Down: sel=%d, loading a diff=%v", mm.review.sel, cmd != nil)
	}
	m3, _ := mm.handleReviewKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m3.(Model).review.open {
		t.Error("Esc did not close the review")
	}
}
