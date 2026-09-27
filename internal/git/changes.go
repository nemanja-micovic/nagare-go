package git

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Change is one file with uncommitted work in a working tree.
type Change struct {
	Path      string
	Status    string // two-letter porcelain status: " M", "A ", "??", ...
	Added     int
	Removed   int
	Untracked bool
	Binary    bool
}

// Changes lists the files with uncommitted work in the repository holding dir —
// everything an agent has done that is not yet in a commit — with line counts
// against HEAD. Paths are relative to the returned root, the repository's top
// level: an agent working in a subdirectory still changes files by their
// repository path, and that is what git reports.
func Changes(dir string) (root string, changes []Change, err error) {
	top, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", nil, err
	}
	root = strings.TrimSpace(string(top))
	out, err := exec.Command("git", "-C", root, "status", "--porcelain=v1", "-z", "-uall").Output()
	if err != nil {
		return "", nil, err
	}
	changes = parsePorcelainZ(out)

	counts := map[string][2]int{}
	binary := map[string]bool{}
	if num, err := exec.Command("git", "-C", root, "diff", "HEAD", "--numstat", "-z").Output(); err == nil {
		counts, binary = parseNumstatZ(num)
	}
	for i := range changes {
		c := &changes[i]
		if c.Untracked {
			c.Added, c.Binary = countLines(filepath.Join(root, c.Path))
			continue
		}
		n := counts[c.Path]
		c.Added, c.Removed, c.Binary = n[0], n[1], binary[c.Path]
	}
	sort.SliceStable(changes, func(a, b int) bool { return changes[a].Path < changes[b].Path })
	return root, changes, nil
}

// parsePorcelainZ parses `git status --porcelain=v1 -z`. Renames carry their
// original path as an extra NUL-separated field, which is skipped.
func parsePorcelainZ(out []byte) []Change {
	var changes []Change
	fields := bytes.Split(out, []byte{0})
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		status, path := string(f[:2]), string(f[3:])
		// An untracked directory under -uall is a nested repository — most
		// often a worktree kept inside the repo. It is not a change.
		if status == "??" && strings.HasSuffix(path, "/") {
			continue
		}
		changes = append(changes, Change{Path: path, Status: status, Untracked: status == "??"})
		if status[0] == 'R' || status[0] == 'C' {
			i++ // the rename's source path
		}
	}
	return changes
}

// parseNumstatZ parses `git diff --numstat -z`: "added\tremoved\tpath\0", with
// "-" counts for binary files, and renames as "added\tremoved\t\0from\0to\0".
func parseNumstatZ(out []byte) (map[string][2]int, map[string]bool) {
	counts := map[string][2]int{}
	binary := map[string]bool{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(fields) {
			path = fields[i+2] // rename: the new path
			i += 2
		}
		if parts[0] == "-" {
			binary[path] = true
			continue
		}
		a, _ := strconv.Atoi(parts[0])
		r, _ := strconv.Atoi(parts[1])
		counts[path] = [2]int{a, r}
	}
	return counts, binary
}

// countLines counts an untracked file's lines, and reports whether it looks
// binary (a NUL in its first few kilobytes, as git decides).
func countLines(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	head := make([]byte, 8000)
	n, _ := f.Read(head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return 0, true
	}
	f.Seek(0, 0)
	lines := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		lines++
	}
	return lines, false
}

// FileDiff returns git's own coloured diff of one changed file against HEAD.
// root is the repository top level Changes returned. An untracked file is shown
// as entirely added.
func FileDiff(root string, c Change) (string, error) {
	dir := root
	var cmd *exec.Cmd
	if c.Untracked {
		// --no-index exits 1 whenever the files differ, which here is always.
		cmd = exec.Command("git", "-C", dir, "diff", "--no-index", "--color=always", "--", os.DevNull, c.Path)
	} else {
		cmd = exec.Command("git", "-C", dir, "diff", "HEAD", "--color=always", "--", c.Path)
	}
	out, err := cmd.Output()
	if err != nil && !(c.Untracked && len(out) > 0) {
		return "", err
	}
	return string(out), nil
}
