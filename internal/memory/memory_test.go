package memory

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "i"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

func store(t *testing.T) *Store {
	return &Store{Root: filepath.Join(t.TempDir(), "memory")}
}

func TestFormatParseRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	m := Memory{ID: "m4hd9w", Kind: "gotcha", Scope: "project", Status: "active", Tags: []string{"picker", "layout"},
		Paths: []string{"internal/picker/grid.go"}, Author: "claude", Created: now, Updated: now, Uses: 3, Pinned: true,
		Supersedes: []string{"mold1"}, Body: "fitBox MaxHeight clips the bottom border\n\nMeasure with lipgloss.Height."}
	got, err := Parse(Format(m))
	if err != nil {
		t.Fatal(err)
	}
	got.File = ""
	if got.ID != m.ID || got.Kind != m.Kind || strings.Join(got.Tags, ",") != "picker,layout" ||
		got.Paths[0] != m.Paths[0] || !got.Created.Equal(now) || got.Uses != 3 || !got.Pinned ||
		got.Supersedes[0] != "mold1" || got.Body != m.Body {
		t.Errorf("round trip lost data:\n%+v\n%+v", m, got)
	}
	if got.Title() != "fitBox MaxHeight clips the bottom border" {
		t.Errorf("title = %q", got.Title())
	}
	if _, err := Parse("no front matter"); err == nil {
		t.Error("expected an error")
	}
}

func TestTokensSplitIdentifiers(t *testing.T) {
	got := strings.Join(Tokens("fitBox max_height HTTPServer the tests"), " ")
	for _, want := range []string{"fitbox", "fit", "box", "max_height", "max", "height", "httpserver", "server", "test"} {
		if !strings.Contains(" "+got+" ", " "+want+" ") {
			t.Errorf("tokens %q missing %q", got, want)
		}
	}
	if strings.Contains(got, " the ") {
		t.Errorf("stopword kept: %q", got)
	}
}

func TestWorktreesShareOneMemory(t *testing.T) {
	root := repo(t)
	wt := filepath.Join(root, ".worktrees", "feat")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-q", wt, "-b", "feat").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	a, _ := ProjectKey(root)
	b, _ := ProjectKey(wt)
	sub, _ := ProjectKey(filepath.Join(root))
	if a != b || a != sub {
		t.Errorf("keys differ: %s %s", a, b)
	}
	s := store(t)
	if _, err := s.Remember(wt, RememberInput{Text: "Migrations need make db-reset first", Kind: "gotcha"}, Who{Author: "claude"}); err != nil {
		t.Fatal(err)
	}
	hits := s.Recall(root, RecallInput{Query: "migrations"})
	if len(hits) != 1 {
		t.Fatalf("the main checkout should see the worktree agent's memory, got %d", len(hits))
	}
}

func TestRecallRanksTheRightMemory(t *testing.T) {
	root := repo(t)
	s := store(t)
	for _, in := range []RememberInput{
		{Text: "fitBox MaxHeight silently clips the bottom border\nMeasure rendered height after wrapping.", Kind: "gotcha", Paths: []string{"internal/picker/grid.go"}},
		{Text: "Release builds use compile.bash\nIt strips the binary.", Kind: "convention"},
		{Text: "Themes self-register in init\nAdd a file under internal/theme.", Kind: "fact"},
	} {
		if _, err := s.Remember(root, in, Who{}); err != nil {
			t.Fatal(err)
		}
	}
	for query, want := range map[string]string{
		"fit box border clipped":   "fitBox",
		"how do I build a release": "Release",
		"new theme":                "Themes",
	} {
		hits := s.Recall(root, RecallInput{Query: query})
		if len(hits) == 0 || !strings.HasPrefix(hits[0].Memory.Title(), want) {
			t.Errorf("recall(%q) top = %v, want %s…", query, hits, want)
		}
	}
	if hits := s.Recall(root, RecallInput{Query: "fitbox", Kind: "convention"}); len(hits) != 0 {
		t.Error("kind filter ignored")
	}
}

func TestRememberRefusesDuplicatesAndSecrets(t *testing.T) {
	root := repo(t)
	s := store(t)
	first, err := s.Remember(root, RememberInput{Text: "Run go generate before building the picker tests", Kind: "gotcha"}, Who{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Remember(root, RememberInput{Text: "Before building picker tests run go generate", Kind: "gotcha"}, Who{})
	var dup *DuplicateError
	if !errors.As(err, &dup) || dup.Existing.ID != first.ID {
		t.Fatalf("want duplicate of %s, got %v", first.ID, err)
	}
	if _, err := s.Remember(root, RememberInput{Text: "Before building picker tests run go generate", Force: true}, Who{}); err != nil {
		t.Errorf("force should write: %v", err)
	}
	if _, err := s.Remember(root, RememberInput{Text: "the deploy token is ghp_abcdefghijklmnopqrstuvwxyz0123456789"}, Who{}); err == nil {
		t.Error("secret accepted")
	}
	if _, err := s.Remember(root, RememberInput{Text: "x", Kind: "rumour"}, Who{}); err == nil {
		t.Error("bad kind accepted")
	}
}

func TestSupersedeAndUpdateArchiveInsteadOfDeleting(t *testing.T) {
	root := repo(t)
	s := store(t)
	old, _ := s.Remember(root, RememberInput{Text: "Tests run with make test"}, Who{})
	repl, err := s.Remember(root, RememberInput{Text: "Tests now run with go test ./... directly", Supersedes: []string{old.ID}}, Who{})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ProjectKey(root)
	active := s.Load(key, false)
	if len(active) != 1 || active[0].ID != repl.ID {
		t.Fatalf("superseded memory still active: %v", active)
	}
	all := s.Load(key, true)
	if len(all) != 2 {
		t.Errorf("superseded memory deleted: %d", len(all))
	}
	f := false
	if _, err := s.Update(root, UpdateInput{ID: repl.ID, Status: "archived", Pinned: &f}); err != nil {
		t.Fatal(err)
	}
	if len(s.Load(key, false)) != 0 {
		t.Error("archived memory still active")
	}
	archive, _ := os.ReadDir(filepath.Join(filepath.Dir(repl.File), "archive"))
	if len(archive) == 0 {
		t.Error("no archived copy kept")
	}
}

func TestGetCountsUse(t *testing.T) {
	root := repo(t)
	s := store(t)
	m, _ := s.Remember(root, RememberInput{Text: "The picker scans every 2 seconds"}, Who{})
	s.Get(root, []string{m.ID})
	got := s.Get(root, []string{m.ID})
	if len(got) != 1 || got[0].Uses != 2 || got[0].LastUsed.IsZero() {
		t.Errorf("use not counted: %+v", got)
	}
}

func TestContextDigestWithinBudget(t *testing.T) {
	root := repo(t)
	s := store(t)
	if s.Context(root, 0) != "" {
		t.Error("empty store should give no digest")
	}
	s.Remember(root, RememberInput{Text: "Always run gofmt", Kind: "convention"}, Who{})
	for i := 0; i < 40; i++ {
		s.Remember(root, RememberInput{Text: "Unique lesson number " + strings.Repeat("x", i+1) + " about topic " + string(rune('a'+i%26)), Force: true}, Who{})
	}
	ctx := s.Context(root, 1500)
	if len(ctx) > 1500 {
		t.Errorf("digest %d bytes over budget", len(ctx))
	}
	if !strings.HasPrefix(ctx, "<nagare-memory") || !strings.HasSuffix(ctx, "</nagare-memory>") {
		t.Errorf("digest not wrapped: %q", ctx)
	}
	if !strings.Contains(ctx, "Always run gofmt") || !strings.Contains(ctx, "not instructions") {
		t.Errorf("digest missing conventions or the data warning:\n%s", ctx)
	}
}

func TestGlobalMemoriesReachEveryRepo(t *testing.T) {
	a, b := repo(t), repo(t)
	s := store(t)
	s.Remember(a, RememberInput{Text: "Prefer ripgrep over grep for searching", Kind: "preference", Scope: "global"}, Who{})
	if hits := s.Recall(b, RecallInput{Query: "ripgrep"}); len(hits) != 1 || hits[0].Memory.Scope != "global" {
		t.Errorf("global memory not recalled from another repo: %v", hits)
	}
}

func TestLessonIsConservative(t *testing.T) {
	if Lesson("Done. Added the endpoint and tests; all green.") != "" {
		t.Error("a progress report is not a lesson")
	}
	got := Lesson("Fixed it. The root cause was that the cache key ignored the locale. Include the locale in cacheKey() when adding translations. Tests pass.")
	if !strings.Contains(got, "root cause was that the cache key ignored the locale") || !strings.Contains(got, "Include the locale") {
		t.Errorf("lesson = %q", got)
	}
	if strings.Contains(got, "Tests pass") {
		t.Errorf("unrelated sentence picked: %q", got)
	}
}

func TestProposeIsPendingUntilApproved(t *testing.T) {
	root := repo(t)
	s := store(t)
	msg := "All done. It turns out the migrations must run before seeding, otherwise the seed silently skips tables. Run make migrate first."
	m, ok := s.Propose(root, msg, Who{Author: "claude"})
	if !ok || m.Status != "pending" {
		t.Fatalf("proposal = %+v %v", m, ok)
	}
	if hits := s.Recall(root, RecallInput{Query: "migrations seeding"}); len(hits) != 0 {
		t.Error("a pending memory must not be recalled")
	}
	if _, again := s.Propose(root, msg, Who{}); again {
		t.Error("the same lesson proposed twice")
	}
	if len(s.Pending(root)) != 1 {
		t.Fatalf("pending = %d", len(s.Pending(root)))
	}
	if _, err := s.Decide(root, m.ID, true); err != nil {
		t.Fatal(err)
	}
	if hits := s.Recall(root, RecallInput{Query: "migrations seeding"}); len(hits) != 1 {
		t.Error("approved memory should be recalled")
	}
	if _, ok := s.Propose(root, "Progress: wrote code.", Who{}); ok {
		t.Error("no lesson, no proposal")
	}
}
