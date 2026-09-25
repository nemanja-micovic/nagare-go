// Package git resolves a directory into the git repository facts nagare
// displays: the current branch, the repository name, and — when the directory
// is a linked worktree — the worktree name.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Repo describes the git checkout a directory belongs to. The zero value is
// what callers get for a directory that is not a repository, and it renders as
// "no git information" rather than as an error.
type Repo struct {
	Branch     string // empty when detached or not a repository
	RepoName   string // basename of the main checkout, identical across its worktrees
	Worktree   string // basename of the worktree directory, empty for the main checkout
	IsWorktree bool
}

// parseRevParse turns the three lines of `git rev-parse --git-common-dir
// --show-toplevel --abbrev-ref HEAD` into a Repo. dir is the directory the
// command ran in, needed because git prints a bare ".git" for a main checkout.
func parseRevParse(out, dir string) Repo {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		return Repo{}
	}

	commonDir, toplevel, branch := lines[0], lines[1], lines[2]
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(dir, commonDir)
	}

	// A main checkout's common dir sits directly inside its own toplevel; a
	// linked worktree's points back at the main checkout instead.
	mainRoot := filepath.Dir(filepath.Clean(commonDir))
	repo := Repo{
		RepoName:   filepath.Base(mainRoot),
		IsWorktree: mainRoot != filepath.Clean(toplevel),
	}
	if repo.IsWorktree {
		repo.Worktree = filepath.Base(toplevel)
	}
	// Detached HEAD prints the literal "HEAD"; report no branch, matching what
	// `git branch --show-current` returns.
	if branch != "HEAD" {
		repo.Branch = branch
	}
	return repo
}

// Describe resolves dir into a Repo using a single git invocation. Any failure
// (not a repository, git missing) returns the zero Repo.
func Describe(dir string) Repo {
	cmd := exec.Command("git", "-C", dir, "rev-parse",
		"--git-common-dir", "--show-toplevel", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return Repo{}
	}
	return parseRevParse(string(out), dir)
}

// worktreeDir is where nagare puts worktrees it creates itself. Claude Code
// uses .claude/worktrees for the ones it creates via `claude -w`; both are
// found the same way, since detection is structural rather than path-based.
const worktreeDir = ".worktrees"

// ValidWorktreeName reports whether name is usable as both a directory name and
// a branch name. Rejecting separators keeps a worktree inside its parent repo,
// and rejecting a leading dash keeps the name from being read as a git flag.
func ValidWorktreeName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("worktree name is empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("worktree name %q is reserved", name)
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("worktree name %q cannot start with a dash", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("worktree name %q may only contain letters, digits, dot, dash and underscore", name)
		}
	}
	return nil
}

// MainRoot returns the main checkout of the repository dir belongs to, whether
// dir is that checkout or one of its linked worktrees. It returns "" when dir
// is not in a repository.
func MainRoot(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	commonDir := strings.TrimSpace(string(out))
	if commonDir == "" {
		return ""
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(dir, commonDir)
	}
	return filepath.Dir(filepath.Clean(commonDir))
}

// AddWorktree creates a linked worktree named name under repoRoot, on a new
// branch of the same name, and returns its path. An existing name fails rather
// than silently reusing a worktree that may hold unrelated work.
func AddWorktree(repoRoot, name string) (string, error) {
	if err := ValidWorktreeName(name); err != nil {
		return "", err
	}
	path := filepath.Join(repoRoot, worktreeDir, name)
	out, err := exec.Command("git", "-C", repoRoot, "worktree", "add", path, "-b", name).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree add %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	ExcludeLocally(repoRoot, "/"+worktreeDir+"/")
	return path, nil
}

// ExcludeLocally adds pattern to the repository's .git/info/exclude unless it
// is already there. Worktrees nagare creates live inside the repository, and
// without this every one of them shows up in the main checkout's git status as
// an untracked directory. info/exclude rather than .gitignore: it is local to
// this clone, so nagare never edits a tracked file on the user's behalf.
func ExcludeLocally(repoRoot, pattern string) error {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return err
	}
	common := strings.TrimSpace(string(out))
	if !filepath.IsAbs(common) {
		common = filepath.Join(repoRoot, common)
	}
	path := filepath.Join(common, "info", "exclude")
	existing, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		prefix = "\n"
	}
	_, err = fmt.Fprintf(f, "%s%s\n", prefix, pattern)
	return err
}

// ClaudeWorktreePath returns where `claude -w <name>` places its worktree.
// nagare predicts it so it can register the session without waiting for Claude
// to start.
func ClaudeWorktreePath(repoRoot, name string) string {
	return filepath.Join(repoRoot, ".claude", "worktrees", name)
}

// Work describes outstanding work in a checkout. The zero value is what a
// non-repository yields, and reads as "nothing to report".
type Work struct {
	Dirty       int  // changed files, tracked or not
	Ahead       int  // commits ahead of the upstream branch
	HasUpstream bool // false when the branch tracks nothing
}

// WorkStatus reports what is outstanding in dir. It is deliberately cheap —
// two git calls — because callers run it on selection rather than per frame.
//
// Ahead is only meaningful with an upstream; without one there is no
// non-arbitrary base to count against, so HasUpstream stays false and callers
// show only the dirty count.
func WorkStatus(dir string) Work {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return Work{}
	}

	var w Work
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			w.Dirty++
		}
	}

	if out, err := exec.Command("git", "-C", dir, "rev-list", "--count", "@{u}..HEAD").Output(); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
			w.Ahead = n
			w.HasUpstream = true
		}
	}
	return w
}

// RemoveWorktree removes the worktree at path, refusing while it holds
// uncommitted work. The branch is left alone so its commits survive.
//
// The dirty check is made here rather than left to git because Claude Code locks
// the worktrees it creates, and git refuses to remove a locked worktree unless
// --force is passed — which would also override the dirty check. So nagare
// verifies cleanliness itself, unlocks, and then removes without --force.
func RemoveWorktree(path string) error {
	if w := WorkStatus(path); w.Dirty > 0 {
		return fmt.Errorf("worktree has %d uncommitted change(s); commit or discard them first", w.Dirty)
	}
	// Best-effort: fails harmlessly when the worktree was never locked.
	exec.Command("git", "-C", path, "worktree", "unlock", path).Run()

	out, err := exec.Command("git", "-C", path, "worktree", "remove", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
