package picker

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/nemke/nagare-go/internal/git"
	"github.com/nemke/nagare-go/internal/models"
)

// wt builds a session that sits in a worktree of sessName.
func wt(sessName, worktree string, status models.SessionStatus) models.Session {
	return models.Session{
		Name:        sessName + "/" + worktree,
		SessionName: sessName,
		Status:      status,
		AgentType:   models.AgentClaude,
		Details:     models.SessionDetails{Worktree: worktree, RepoName: sessName},
	}
}

// solo builds a single-pane session with no worktree.
func solo(name string, status models.SessionStatus) models.Session {
	return models.Session{
		Name:        name,
		SessionName: name,
		Status:      status,
		AgentType:   models.AgentClaude,
	}
}

func TestBuildRowsUngrouped(t *testing.T) {
	sessions := []models.Session{
		solo("nagare", models.StatusIdle),
		solo("propco", models.StatusIdle),
	}
	rows := buildRows(sessions)

	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5 (two singleton blocks + spacer)", len(rows))
	}
	for i, rowIdx := range []int{1, 4} {
		r := rows[rowIdx]
		if r.SessionIdx != i || r.Glyph != glyphEnd {
			t.Errorf("singleton child %d = %+v, want session %d with end glyph", i, r, i)
		}
	}
	if rows[0].Group != "nagare" || rows[1].Label != "claude_01" {
		t.Errorf("first singleton rows = %+v / %+v", rows[0], rows[1])
	}
	if rows[2].SessionIdx != spacerRow {
		t.Errorf("row 2 = %+v, want project spacer", rows[2])
	}
}

func TestBuildRowsOneGroup(t *testing.T) {
	sessions := []models.Session{
		wt("cosmic-platform-frontend", "shipping", models.StatusWaitingInput),
		wt("cosmic-platform-frontend", "the-site", models.StatusRunning),
		wt("cosmic-platform-frontend", "fluttering-watching-gadget", models.StatusIdle),
	}
	rows := buildRows(sessions)

	// header + 3 children
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	if rows[0].SessionIdx != -1 {
		t.Errorf("row 0 should be a header, got SessionIdx %d", rows[0].SessionIdx)
	}
	if rows[0].Group != "cosmic-platform-frontend" || rows[0].Count != 3 {
		t.Errorf("header = %+v, want group cosmic-platform-frontend count 3", rows[0])
	}
	wantLabels := []string{"shipping", "the-site", "fluttering-watching-gadget"}
	wantGlyphs := []string{glyphMid, glyphMid, glyphEnd}
	for i := range wantLabels {
		r := rows[i+1]
		if r.Label != wantLabels[i] {
			t.Errorf("child %d label = %q, want %q", i, r.Label, wantLabels[i])
		}
		if r.Glyph != wantGlyphs[i] {
			t.Errorf("child %d glyph = %q, want %q", i, r.Glyph, wantGlyphs[i])
		}
		if r.SessionIdx != i {
			t.Errorf("child %d SessionIdx = %d, want %d", i, r.SessionIdx, i)
		}
	}
}

func TestBuildRowsTwoGroupsAndSingleton(t *testing.T) {
	sessions := []models.Session{
		wt("frontend", "a", models.StatusIdle),
		wt("frontend", "b", models.StatusIdle),
		solo("nagare", models.StatusIdle),
		wt("backend", "x", models.StatusIdle),
		wt("backend", "y", models.StatusIdle),
	}
	rows := buildRows(sessions)

	// 3 headers + 5 children + 2 inter-project spacers
	if len(rows) != 10 {
		t.Fatalf("got %d rows, want 10", len(rows))
	}
	var headers []string
	for _, r := range rows {
		if r.SessionIdx == -1 {
			headers = append(headers, r.Group)
		}
	}
	if len(headers) != 3 || headers[0] != "frontend" || headers[1] != "nagare" || headers[2] != "backend" {
		t.Errorf("headers = %v, want [frontend nagare backend]", headers)
	}
	// The singleton gets the same header/child hierarchy as every other project.
	for _, r := range rows {
		if r.SessionIdx == 2 && (r.Label != "claude_01" || r.Glyph != glyphEnd) {
			t.Errorf("singleton row = %+v, want indented claude_01", r)
		}
	}
}

func TestBuildRowsAddsSpaceOnlyBetweenGroups(t *testing.T) {
	rows := buildRows([]models.Session{
		wt("one", "a", models.StatusIdle),
		wt("one", "b", models.StatusIdle),
		solo("two", models.StatusIdle),
	})
	spacers := 0
	for i, row := range rows {
		if row.SessionIdx == spacerRow {
			spacers++
			if i == 0 || i == len(rows)-1 {
				t.Errorf("spacer appears at list edge: %+v", rows)
			}
		}
	}
	if spacers != 1 {
		t.Errorf("spacers = %d, want one between two groups", spacers)
	}
}

// Every session index must appear exactly once, or the cursor can land on a
// session that has no row to highlight.
func TestBuildRowsCoversEverySession(t *testing.T) {
	sessions := []models.Session{
		wt("frontend", "a", models.StatusIdle),
		wt("frontend", "b", models.StatusIdle),
		solo("nagare", models.StatusIdle),
	}
	seen := make(map[int]int)
	for _, r := range buildRows(sessions) {
		if r.SessionIdx >= 0 {
			seen[r.SessionIdx]++
		}
	}
	for i := range sessions {
		if seen[i] != 1 {
			t.Errorf("session %d appears in %d rows, want exactly 1", i, seen[i])
		}
	}
}

func TestChildLabel(t *testing.T) {
	tests := []struct {
		name string
		s    models.Session
		want string
	}{
		{
			name: "worktree wins",
			s: models.Session{
				Name: "repo/custom", SessionName: "repo",
				Details: models.SessionDetails{Worktree: "the-site"},
			},
			want: "the-site",
		},
		{
			name: "falls back to the suffix after the session name",
			s:    models.Session{Name: "repo/claude_02", SessionName: "repo"},
			want: "claude_02",
		},
		{
			name: "custom window name suffix",
			s:    models.Session{Name: "repo/review", SessionName: "repo"},
			want: "review",
		},
		{
			name: "bare name when there is no prefix to strip",
			s:    models.Session{Name: "nagare", SessionName: "nagare"},
			want: "nagare",
		},
	}
	for _, tt := range tests {
		if got := childLabel(tt.s); got != tt.want {
			t.Errorf("%s: childLabel() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// names returns the visual order of a sorted session slice.
func names(sessions []models.Session) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.Name
	}
	return out
}

// A waiting child must lift its whole repo above a quiet one, otherwise
// grouping buries the one thing the picker exists to surface.
func TestSortFilteredWaitingChildLiftsGroup(t *testing.T) {
	m := Model{sortMode: SortByStatus}
	m.filtered = []models.Session{
		solo("aaa-quiet", models.StatusIdle),
		wt("zzz-busy", "one", models.StatusIdle),
		wt("zzz-busy", "two", models.StatusWaitingInput),
	}
	m.sortFiltered()

	if got := names(m.filtered)[0]; got != "zzz-busy/two" && got != "zzz-busy/one" {
		t.Errorf("first row = %q, want a zzz-busy member (it has a waiting child)", got)
	}
	if last := names(m.filtered)[2]; last != "aaa-quiet" {
		t.Errorf("last row = %q, want aaa-quiet", last)
	}
}

// Group members must stay contiguous or buildRows emits duplicate headers.
func TestSortFilteredKeepsGroupsContiguous(t *testing.T) {
	m := Model{sortMode: SortByStatus}
	m.filtered = []models.Session{
		wt("repo-a", "one", models.StatusIdle),
		wt("repo-b", "one", models.StatusWaitingInput),
		wt("repo-a", "two", models.StatusWaitingInput),
		wt("repo-b", "two", models.StatusIdle),
	}
	m.sortFiltered()

	seen := map[string]bool{}
	prev := ""
	for _, s := range m.filtered {
		key := groupKeyOf(s)
		if key != prev {
			if seen[key] {
				t.Fatalf("group %q is not contiguous: %v", key, names(m.filtered))
			}
			seen[key] = true
			prev = key
		}
	}
}

// Within a group the active sort mode still applies.
func TestSortFilteredWithinGroupFollowsMode(t *testing.T) {
	m := Model{sortMode: SortByName}
	m.filtered = []models.Session{
		wt("repo", "zebra", models.StatusIdle),
		wt("repo", "alpha", models.StatusIdle),
	}
	m.sortFiltered()

	if got := names(m.filtered); got[0] != "repo/alpha" {
		t.Errorf("order = %v, want alpha first under SortByName", got)
	}
}

// With no groups at all, ordering must match the old flat behaviour.
func TestSortFilteredSingletonRegression(t *testing.T) {
	m := Model{sortMode: SortByStatus}
	m.filtered = []models.Session{
		solo("idle-one", models.StatusIdle),
		solo("dead-one", models.StatusDead),
		solo("waiting-one", models.StatusWaitingInput),
		solo("running-one", models.StatusRunning),
	}
	m.sortFiltered()

	want := []string{"waiting-one", "running-one", "idle-one", "dead-one"}
	got := names(m.filtered)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// The complaint that motivated grouping: at the observed pane width the full
// name "cosmic-platform-frontend/fluttering-watching-gadget" (51 chars) was
// truncated to "cosmic-platform-frontend/flutter…", hiding the only part that
// identifies the pane. Grouped, the label alone must fit.
func TestRenderListViewFitsWorktreeNames(t *testing.T) {
	const width = 46

	m := Model{sortMode: SortByStatus}
	m.filtered = []models.Session{
		wt("cosmic-platform-frontend", "shipping", models.StatusWaitingInput),
		wt("cosmic-platform-frontend", "fluttering-watching-gadget", models.StatusIdle),
		wt("cosmic-platform-frontend", "the-site", models.StatusRunning),
	}
	m.sortFiltered()

	out, _ := m.renderListView(width, 10)

	for _, label := range []string{"shipping", "fluttering-watching-gadget", "the-site"} {
		if !strings.Contains(out, label) {
			t.Errorf("rendered list is missing %q in full:\n%s", label, out)
		}
	}
	// The repo is named once on the header, not on every row.
	if n := strings.Count(out, "cosmic-platform-frontend"); n != 1 {
		t.Errorf("repo name appears %d times, want 1 (on the header):\n%s", n, out)
	}
	// Every rendered line must respect the pane width.
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line is %d cells wide, want <= %d: %q", w, width, line)
		}
	}
}

func TestListAgentLabelsDistinguishClaudeCodeAndCodex(t *testing.T) {
	if got := listAgentLabel(models.AgentClaude); got != "C" {
		t.Errorf("Claude Code sigil = %q, want C", got)
	}
	if got := listAgentLabel(models.AgentCodex); got != "X" {
		t.Errorf("Codex sigil = %q, want X", got)
	}

	m := Model{}
	m.filtered = []models.Session{
		{Name: "repo/claude_01", SessionName: "repo", AgentType: models.AgentClaude},
		{Name: "repo/codex_01", SessionName: "repo", AgentType: models.AgentCodex},
	}

	out, _ := m.renderListView(48, 10)
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 48 {
			t.Errorf("mixed-agent row is %d cells wide, want <= 48: %q", w, line)
		}
	}
}

// Two agents in one working tree will fight over files, so the picker has to
// say so rather than letting it be discovered through conflicts.
func TestSharedPaths(t *testing.T) {
	shared := "/repo/.worktrees/busy"
	sessions := []models.Session{
		{Name: "a", Path: shared},
		{Name: "b", Path: shared},
		{Name: "c", Path: "/repo/.worktrees/quiet"},
		{Name: "d", Path: ""}, // saved sessions have no path; must not count
		{Name: "e", Path: ""},
	}
	got := sharedPaths(sessions)

	if got[shared] != 2 {
		t.Errorf("shared count = %d, want 2", got[shared])
	}
	if _, ok := got["/repo/.worktrees/quiet"]; ok {
		t.Error("a path with one agent must not be reported as shared")
	}
	if _, ok := got[""]; ok {
		t.Error("empty paths must not be reported as shared")
	}
}

// Ctrl+x must not take a repo's other worktrees down with it.
func TestKillTarget(t *testing.T) {
	multi := []models.Session{
		{Name: "repo/a", SessionName: "repo", WindowIndex: 0},
		{Name: "repo/b", SessionName: "repo", WindowIndex: 1},
	}
	target, isWindow := killTarget(multi[1], multi)
	if !isWindow {
		t.Error("a session with sibling agent panes must be killed by window")
	}
	if target != "repo:1" {
		t.Errorf("target = %q, want repo:1", target)
	}

	lone := []models.Session{{Name: "solo", SessionName: "solo", WindowIndex: 0}}
	target, isWindow = killTarget(lone[0], lone)
	if isWindow {
		t.Error("a single-pane session must still be killed whole")
	}
	if target != "solo" {
		t.Errorf("target = %q, want solo", target)
	}
}

// The detail pane re-renders every frame for the pulse, so a lookup must hit
// the cache after the first call rather than shelling out to git again.
func TestWorkForCachesByPath(t *testing.T) {
	m := Model{workCache: map[string]git.Work{
		"/repo/wt": {Dirty: 3, Ahead: 1, HasUpstream: true},
	}}

	got := m.workFor(models.Session{Path: "/repo/wt"})
	if got.Dirty != 3 || got.Ahead != 1 {
		t.Errorf("workFor = %+v, want the cached value", got)
	}
	// A session with no path must not be probed at all.
	if got := m.workFor(models.Session{}); got != (git.Work{}) {
		t.Errorf("workFor(no path) = %+v, want zero", got)
	}
}

// A nil cache means "do not touch git", so a zero Model stays render-safe.
func TestWorkForNilCacheIsInert(t *testing.T) {
	var m Model
	if got := m.workFor(models.Session{Path: "/repo/wt"}); got != (git.Work{}) {
		t.Errorf("workFor with nil cache = %+v, want zero", got)
	}
}

// Typing a worktree name must not double as a search query — the list used to
// filter itself away underneath the name being typed.
func TestQueryIgnoresNameEntryModes(t *testing.T) {
	m := Model{searchInput: textinput.New()}
	m.searchInput.SetValue("test-worktree")

	if got := m.query(); got != "test-worktree" {
		t.Errorf("query() = %q, want the search text when no mode is active", got)
	}
	for _, tc := range []struct {
		name  string
		apply func(*Model)
	}{
		{"worktree", func(m *Model) { m.worktreeMode = true }},
		{"rename", func(m *Model) { m.renameMode = true }},
		{"confirm", func(m *Model) { m.confirmMode = true }},
	} {
		mm := Model{searchInput: textinput.New()}
		mm.searchInput.SetValue("test-worktree")
		tc.apply(&mm)
		if got := mm.query(); got != "" {
			t.Errorf("%s mode: query() = %q, want empty", tc.name, got)
		}
	}
}

// A destructive prompt must survive keys that are not an answer.
func TestConfirmIgnoresUnrelatedKeys(t *testing.T) {
	base := Model{confirmMode: true, confirmOn: models.Session{
		Path: "/nonexistent", Details: models.SessionDetails{Worktree: "wt"},
	}}

	for _, key := range []string{"j", "down", "ctrl+f", "tab"} {
		got, _ := base.handleConfirmKey(tea.KeyPressMsg{Code: 'x', Text: key})
		if !got.(Model).confirmMode {
			t.Errorf("key %q dismissed the confirmation", key)
		}
	}
	for _, key := range []string{"n", "N", "esc", "enter"} {
		got, _ := base.handleConfirmKey(tea.KeyPressMsg{Code: 'x', Text: key})
		if got.(Model).confirmMode {
			t.Errorf("key %q did not close the confirmation", key)
		}
	}
}

// A worktree's branch is "worktree-" plus the label the row already shows, so
// printing it wastes the columns a real branch needs.
func TestBranchFor(t *testing.T) {
	tests := []struct {
		label, worktree, branch, want string
	}{
		{"the-site", "the-site", "worktree-the-site", ""},
		{"shipping", "shipping", "worktree-shipping", ""},
		{"main-checkout", "", "main-checkout", ""},
		{"", "", "", ""},
		// A lone pane in a worktree is labelled with its full name, so the
		// branch has to be checked against the worktree too.
		{"cosmiclab-frontend/splat-zoomin", "splat-zoomin", "worktree-splat-zoomin", ""},
		{"cosmiclab-backend", "", "feat/splat_loader", "feat/splat_loader"},
		{"cosmo-ai", "", "fix/slack-approval-observability", "fix/slack-approval-observability"},
		// The default branch is what a row without one is taken to be on, so
		// naming it only costs the label columns.
		{"nagare", "", "main", ""},
		{"legacy", "", "master", ""},
		{"nagare", "", "maintenance", "maintenance"},
		// A worktree on a branch of its own name still says something.
		{"repo/wt", "wt", "feat/real-branch", "feat/real-branch"},
	}
	for _, tt := range tests {
		if got := branchFor(tt.label, tt.worktree, tt.branch); got != tt.want {
			t.Errorf("branchFor(%q, %q, %q) = %q, want %q", tt.label, tt.worktree, tt.branch, got, tt.want)
		}
	}
}

func TestSplitRowWidth(t *testing.T) {
	// No branch: the label gets everything.
	if l, b := splitRowWidth(40, 12, 0); l != 40 || b != 0 {
		t.Errorf("no branch: got label=%d branch=%d, want 40/0", l, b)
	}

	// A short label leaves the branch plenty — the real case that motivated
	// this: "cosmo-ai" plus a 32-character branch must both fit.
	l, b := splitRowWidth(41, 8, 32)
	if l != 8 || b != 32 {
		t.Errorf("short label: got label=%d branch=%d, want 8/32", l, b)
	}

	// A long label yields rather than clipping the branch to nothing.
	l, b = splitRowWidth(40, 38, 20)
	if b < minBranchWidth {
		t.Errorf("branch squeezed to %d, want at least %d", b, minBranchWidth)
	}
	if l+b+1 > 40 {
		t.Errorf("label+branch = %d, overflows 40", l+b+1)
	}

	// Degenerate width must not produce negatives.
	if l, b := splitRowWidth(5, 30, 30); l < 1 || b < 0 {
		t.Errorf("tiny width: got label=%d branch=%d", l, b)
	}
}
