package verify

import (
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

func TestFindLooksInTheWorktreeThenTheMainCheckout(t *testing.T) {
	root := repo(t)
	if got, _ := Find(root); got != "" {
		t.Fatalf("no file: got %q", got)
	}
	if err := os.MkdirAll(filepath.Join(root, ".nagare"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("  go test ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := Find(root); got != "go test ./..." {
		t.Errorf("main checkout: got %q", got)
	}
	wt := filepath.Join(root, ".worktrees", "feat")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-q", wt, "-b", "feat").CombinedOutput(); err != nil {
		t.Fatalf("worktree: %v %s", err, out)
	}
	// The file is untracked, so the worktree does not have it: it falls back
	// to the main checkout's.
	if got, file := Find(filepath.Join(wt)); got != "go test ./..." || file != filepath.Join(root, FileName) {
		t.Errorf("worktree fallback: got %q", got)
	}
}

func TestRunReportsFailureWithTheTailOfTheOutput(t *testing.T) {
	dir := t.TempDir()
	r := Run(dir, "echo start; seq 1 3000; echo 'FAIL: TestX'; exit 1", time.Minute)
	if r.OK {
		t.Fatal("exit 1 should fail")
	}
	if !strings.HasSuffix(r.Output, "FAIL: TestX") || len(r.Output) > maxOutput+10 {
		t.Errorf("want the tail, %d bytes ending %q", len(r.Output), r.Output[len(r.Output)-20:])
	}
	if !Run(dir, "true", time.Minute).OK {
		t.Error("true should pass")
	}
	slow := Run(dir, "sleep 5", 100*time.Millisecond)
	if slow.OK || !strings.Contains(slow.Output, "timed out") {
		t.Errorf("timeout: %+v", slow)
	}
}

func TestCommandSkipsComments(t *testing.T) {
	if got := command("# e.g.\n# go test\n"); got != "" {
		t.Errorf("only comments: %q", got)
	}
	if got := command("# checks\ngo vet ./...\n\ngo test ./...\n"); got != "go vet ./...\ngo test ./..." {
		t.Errorf("got %q", got)
	}
}
