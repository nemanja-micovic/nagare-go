package picker

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

// TestActivityLevelOrdering — the numbers matter less than the order. An agent
// waiting on the user has to out-rank one that is working, so the tallest bars in a
// trace are the moments the user was needed.
func TestActivityLevelOrdering(t *testing.T) {
	waiting := activityLevel(models.StatusWaitingInput)
	running := activityLevel(models.StatusRunning)
	idle := activityLevel(models.StatusIdle)
	dead := activityLevel(models.StatusDead)

	if !(waiting > running && running > idle && idle > dead) {
		t.Errorf("levels out of order: waiting %d, running %d, idle %d, dead %d",
			waiting, running, idle, dead)
	}
	if waiting > sparkLevels {
		t.Errorf("waiting level %d exceeds the %d dot rows a braille cell has",
			waiting, sparkLevels)
	}
	if dead != 0 {
		t.Errorf("a dead session should draw nothing, got level %d", dead)
	}
}

// TestSparklineEncodesBraille checks the dot bit patterns directly. Getting these
// wrong produces a plausible-looking but meaningless trace, which no visual check
// would catch.
func TestSparklineEncodesBraille(t *testing.T) {
	cases := []struct {
		name   string
		levels []uint8
		want   string
	}{
		{"nothing", []uint8{0, 0}, "⠀"},
		{"both columns full", []uint8{4, 4}, "⣿"},
		{"left full, right empty", []uint8{4, 0}, "⡇"},
		{"left empty, right full", []uint8{0, 4}, "⢸"},
		{"one dot each, bottom row", []uint8{1, 1}, "⣀"},
		{"odd sample count pads the right column", []uint8{4}, "⡇"},
	}
	for _, tc := range cases {
		got := ansi.Strip(sparkline(tc.levels, 4))
		if got != tc.want {
			t.Errorf("%s: sparkline(%v) = %q (%U), want %q (%U)",
				tc.name, tc.levels, got, []rune(got), tc.want, []rune(tc.want))
		}
	}
}

// TestSparklineWidth — two samples to a cell, never wider than asked, and always
// showing the *most recent* history rather than the oldest.
func TestSparklineWidth(t *testing.T) {
	levels := make([]uint8, 40)
	for i := range levels {
		levels[i] = 3
	}

	for _, width := range []int{1, 4, 6, 12, 40} {
		got := ansi.StringWidth(sparkline(levels, width))
		if got > width {
			t.Errorf("width %d: trace is %d cells", width, got)
		}
	}

	// Fewer samples than room: one cell per two samples, not padded out.
	if got := ansi.StringWidth(sparkline([]uint8{3, 3, 3, 3}, 10)); got != 2 {
		t.Errorf("4 samples in 10 cells rendered %d cells, want 2", got)
	}

	// The recent end wins: a trace that starts idle and ends waiting must show the
	// waiting.
	recent := append(make([]uint8, 30), 4, 4)
	tail := ansi.Strip(sparkline(recent, 3))
	if !strings.HasSuffix(tail, "⣿") {
		t.Errorf("trace %q does not end on the most recent samples", tail)
	}

	if got := sparkline(nil, 8); got != "" {
		t.Errorf("empty history rendered %q", got)
	}
	if got := sparkline([]uint8{3, 3}, 0); got != "" {
		t.Errorf("zero width rendered %q", got)
	}
}

// TestSparklineCellTakesTheLoudestSample — a single moment of waiting inside a run
// of work is the thing worth seeing, so it must win its cell rather than being
// averaged away.
func TestSparklineCellTakesTheLoudestSample(t *testing.T) {
	waitingColor := models.StatusColor(models.StatusWaitingInput)
	runningColor := models.StatusColor(models.StatusRunning)

	// One waiting sample paired with a working one.
	mixed := sparkline([]uint8{3, 4}, 1)
	if !strings.Contains(mixed, fgSeq(levelColor(4))) {
		t.Errorf("a cell holding a waiting sample is not coloured for waiting (%s)", waitingColor)
	}
	if strings.Contains(mixed, fgSeq(levelColor(3))) {
		t.Errorf("a cell holding a waiting sample is also coloured for working (%s)", runningColor)
	}

	// Pure work stays the working colour.
	work := sparkline([]uint8{3, 3}, 1)
	if !strings.Contains(work, fgSeq(levelColor(3))) {
		t.Error("a working cell is not coloured for working")
	}
}

// TestRecordActivityTrimsAndPrunes — history is per session and the picker runs for
// hours, so it must neither grow without bound nor keep sessions that have gone.
func TestRecordActivityTrimsAndPrunes(t *testing.T) {
	hist := map[string][]uint8{}
	sessions := waitingSet("wri")

	for i := 0; i < sparkSamples*3; i++ {
		recordActivity(hist, sessions)
	}
	if len(hist) != len(sessions) {
		t.Fatalf("history holds %d sessions, want %d", len(hist), len(sessions))
	}
	for key, samples := range hist {
		if len(samples) != sparkSamples {
			t.Errorf("%s kept %d samples, want %d", key, len(samples), sparkSamples)
		}
	}

	// A session that goes away takes its history with it.
	recordActivity(hist, sessions[:1])
	if len(hist) != 1 {
		t.Errorf("history holds %d sessions after two disappeared, want 1", len(hist))
	}

	// Samples reflect status, in order.
	fresh := map[string][]uint8{}
	one := waitingSet("i")
	recordActivity(fresh, one)
	one[0].Status = models.StatusWaitingInput
	recordActivity(fresh, one)
	got := fresh[sessionKey(one[0])]
	want := []uint8{activityLevel(models.StatusIdle), activityLevel(models.StatusWaitingInput)}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("samples = %v, want %v", got, want)
	}
}

// TestSparkWidthStaysModest — the trace is a glance, not a chart, and the space it
// takes belongs to the session name.
func TestSparkWidthStaysModest(t *testing.T) {
	if got := sparkWidth(3); got != 0 {
		t.Errorf("sparkWidth(3) = %d; too little room should mean no trace", got)
	}
	if got := sparkWidth(200); got > 12 {
		t.Errorf("sparkWidth(200) = %d; the trace should stay small", got)
	}
	if got := sparkWidth(8); got != 8 {
		t.Errorf("sparkWidth(8) = %d, want 8", got)
	}
}

// TestSparklineReachesTheDetailPane — the trace has to show up where it was wired,
// and not before there is any history to show.
func TestSparklineReachesTheDetailPane(t *testing.T) {
	s := models.Session{
		Name: "svc", SessionName: "svc", Path: "/tmp/svc",
		Status: models.StatusRunning, AgentType: models.AgentClaude,
	}
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 160, Height: 34},
		SessionsUpdatedMsg([]models.Session{s}),
	)

	// One scan is one sample: not enough to be a trace worth drawing.
	if got := ansi.Strip(m.viewRight(128, 32)); strings.Contains(got, "Recent") {
		t.Error("a single sample drew a trace")
	}

	m.history[sessionKey(s)] = []uint8{1, 3, 3, 4, 3, 1, 3, 3}
	out := ansi.Strip(m.viewRight(128, 32))
	if !strings.Contains(out, "Recent") {
		t.Fatal("no trace in the detail pane despite history")
	}
	if !strings.ContainsAny(out, "⠀⣿⡇⢸⣀⣈⣶⣧") {
		t.Errorf("the Recent row has no braille in it:\n%s", out)
	}
}
