// Package policy answers an agent's permission requests on the user's
// behalf, from a per-project `.nagare/policy` — the autonomy dial the
// orchestration tools converged on (Antigravity's Off/Auto/Turbo, Warp's
// per-agent auto-accept), with a deny list that always wins.
//
//	# ask | auto | turbo
//	mode: auto
//	allow: Bash(go test *) Bash(git status*)
//	deny:  Bash(rm -rf *) Bash(git push*)
//
// Modes:
//
//	ask    nothing is approved beyond the allow rules — the agent asks as usual
//	auto   reads and edits inside the project are approved; commands ask
//	       unless allowed
//	turbo  everything is approved except what the deny rules catch
//
// A deny rule never silently blocks: it forces the question to the human,
// even when the agent's own settings would have allowed the call. The file
// comes with the repository, so it acts only once approved (see trust).
package policy

import (
	"os"
	"path/filepath"
	"strings"
)

// FileName is the policy file, relative to the project root.
const FileName = ".nagare/policy"

// Policy is a parsed policy file.
type Policy struct {
	Mode  string // ask, auto or turbo
	Allow []Rule
	Deny  []Rule
}

// Rule matches a tool, optionally with a pattern over what the call is
// about: `Bash(go test *)`, `Edit(src/*)`, `WebFetch`.
type Rule struct {
	Tool    string
	Pattern string // "" matches any call of the tool
}

func (r Rule) String() string {
	if r.Pattern == "" {
		return r.Tool
	}
	return r.Tool + "(" + r.Pattern + ")"
}

// Parse reads policy text. Unknown lines are ignored.
func Parse(text string) Policy {
	p := Policy{Mode: "ask"}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "mode":
			switch v {
			case "ask", "auto", "turbo":
				p.Mode = v
			}
		case "allow":
			p.Allow = append(p.Allow, parseRules(v)...)
		case "deny":
			p.Deny = append(p.Deny, parseRules(v)...)
		}
	}
	return p
}

// parseRules splits `Bash(go test *) Read Edit(src/*)` — spaces inside
// parentheses belong to the pattern.
func parseRules(s string) []Rule {
	var out []Rule
	for len(strings.TrimSpace(s)) > 0 {
		s = strings.TrimLeft(s, " \t,")
		end := strings.IndexAny(s, " \t,(")
		if end < 0 {
			out = append(out, Rule{Tool: s})
			break
		}
		if s[end] != '(' {
			out = append(out, Rule{Tool: s[:end]})
			s = s[end:]
			continue
		}
		closeAt := strings.Index(s[end:], ")")
		if closeAt < 0 {
			out = append(out, Rule{Tool: s[:end], Pattern: s[end+1:]})
			break
		}
		out = append(out, Rule{Tool: s[:end], Pattern: s[end+1 : end+closeAt]})
		s = s[end+closeAt+1:]
	}
	return out
}

// Load reads a policy file.
func Load(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, err
	}
	return Parse(string(data)), nil
}

// Call is one tool call to decide on.
type Call struct {
	Tool   string // e.g. "Bash", "Edit", "mcp__nagare__recall"
	Detail string // what it is about: the command, file path or URL
	Cwd    string // the agent's directory; auto only approves edits inside it
}

// Decision is what to tell the agent: "allow", "ask", or "" for no
// opinion (the agent's normal permission flow decides).
type Decision struct {
	Verdict string
	Reason  string
}

// readOnly and editing are the tools auto mode approves inside the project.
var readOnly = map[string]bool{"Read": true, "Glob": true, "Grep": true, "LS": true, "TodoWrite": true, "NotebookRead": true}
var editing = map[string]bool{"Edit": true, "MultiEdit": true, "Write": true, "NotebookEdit": true}

// Decide applies the policy to a call.
func (p Policy) Decide(c Call) Decision {
	for _, r := range p.Deny {
		if r.Matches(c) {
			return Decision{"ask", "nagare policy: matches deny rule " + r.String() + " — a human decides"}
		}
	}
	for _, r := range p.Allow {
		if r.Matches(c) {
			return Decision{"allow", "nagare policy: allowed by " + r.String()}
		}
	}
	switch p.Mode {
	case "turbo":
		return Decision{"allow", "nagare policy: turbo"}
	case "auto":
		if readOnly[c.Tool] {
			return Decision{"allow", "nagare policy: auto (read)"}
		}
		if editing[c.Tool] && inside(c.Detail, c.Cwd) {
			return Decision{"allow", "nagare policy: auto (edit inside the project)"}
		}
	}
	return Decision{}
}

// Matches reports whether a rule covers a call.
func (r Rule) Matches(c Call) bool {
	if r.Tool != c.Tool && r.Tool != "*" {
		return false
	}
	return r.Pattern == "" || glob(r.Pattern, c.Detail)
}

// inside reports whether path lies within dir (relative paths count as
// inside, being relative to the agent's directory).
func inside(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// glob matches s against a pattern where * matches any run of characters
// (including spaces and slashes) and everything else is literal.
func glob(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i, part := range parts[1:] {
		last := i == len(parts)-2
		if last {
			return strings.HasSuffix(s, part)
		}
		idx := strings.Index(s, part)
		if idx < 0 {
			return false
		}
		s = s[idx+len(part):]
	}
	return true
}
