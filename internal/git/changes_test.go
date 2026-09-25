package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestChanges covers every kind of change an agent leaves behind: an edit, a
// new file, a deletion and a rename, with line counts against HEAD.
func TestChanges(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
		os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	write("edit.go", "a\nb\nc\n")
	write("gone.go", "x\n")
	write("old.go", "one\ntwo\nthree\nfour\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "init")

	write("edit.go", "a\nB\nc\nd\n")
	write("sub dir/new file.md", "1\n2\n3\n")
	os.Remove(filepath.Join(dir, "gone.go"))
	gitIn(t, dir, "mv", "old.go", "renamed.go")

	got, err := Changes(dir)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Change{}
	for _, c := range got {
		byPath[c.Path] = c
	}
	check := func(path string, added, removed int, untracked bool) {
		t.Helper()
		c, ok := byPath[path]
		if !ok {
			t.Errorf("%s missing from %+v", path, got)
			return
		}
		if c.Added != added || c.Removed != removed || c.Untracked != untracked {
			t.Errorf("%s = +%d -%d untracked=%v, want +%d -%d untracked=%v",
				path, c.Added, c.Removed, c.Untracked, added, removed, untracked)
		}
	}
	check("edit.go", 2, 1, false)
	check("gone.go", 0, 1, false)
	check("sub dir/new file.md", 3, 0, true)
	check("renamed.go", 0, 0, false)
	if _, ok := byPath["old.go"]; ok {
		t.Error("a rename's source path was listed as a change of its own")
	}
}

func TestFileDiff(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("brand new\n"), 0o644)

	d, err := FileDiff(dir, Change{Path: "a.txt", Status: " M"})
	if err != nil || !strings.Contains(d, "world") {
		t.Errorf("tracked diff = %q, %v", d, err)
	}
	d, err = FileDiff(dir, Change{Path: "b.txt", Status: "??", Untracked: true})
	if err != nil || !strings.Contains(d, "brand new") {
		t.Errorf("untracked diff = %q, %v", d, err)
	}
}

// TestWorktreesDoNotPolluteTheMainCheckout — a worktree nagare creates lives
// inside the repository; it must not show up as a change in the main checkout,
// neither in git status nor in the review panel.
func TestWorktreesDoNotPolluteTheMainCheckout(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "init")

	if _, err := AddWorktree(dir, "feature"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddWorktree(dir, "other"); err != nil {
		t.Fatal(err)
	}
	status, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if len(strings.TrimSpace(string(status))) != 0 {
		t.Errorf("main checkout status is not clean after adding worktrees: %q", status)
	}
	exclude, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if strings.Count(string(exclude), "/.worktrees/") != 1 {
		t.Errorf("exclude file should carry the pattern exactly once:\n%s", exclude)
	}
	if changes, _ := Changes(dir); len(changes) != 0 {
		t.Errorf("review lists %+v in a clean main checkout", changes)
	}
}
