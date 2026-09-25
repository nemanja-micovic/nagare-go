package policy

import "testing"

func TestParseRules(t *testing.T) {
	p := Parse("# comment\nmode: auto\nallow: Bash(go test *) Read, Bash(git status)\ndeny: Bash(rm -rf *)\nmode: bogus\n")
	if p.Mode != "auto" {
		t.Errorf("mode = %q (bogus must not override)", p.Mode)
	}
	if len(p.Allow) != 3 || p.Allow[0].Pattern != "go test *" || p.Allow[1].Tool != "Read" || p.Allow[2].Pattern != "git status" {
		t.Errorf("allow = %+v", p.Allow)
	}
	if len(p.Deny) != 1 || p.Deny[0].String() != "Bash(rm -rf *)" {
		t.Errorf("deny = %+v", p.Deny)
	}
	if Parse("").Mode != "ask" {
		t.Error("default mode should be ask")
	}
}

func TestDecide(t *testing.T) {
	cwd := "/src/app/.worktrees/feat"
	p := Parse("mode: auto\nallow: Bash(go test *)\ndeny: Bash(rm -rf *) Bash(git push*)")
	cases := []struct {
		name string
		call Call
		want string
	}{
		{"allowed command", Call{"Bash", "go test ./...", cwd}, "allow"},
		{"other command asks as usual", Call{"Bash", "make deploy", cwd}, ""},
		{"deny wins over mode", Call{"Bash", "rm -rf build", cwd}, "ask"},
		{"deny wins over allow", Call{"Bash", "git push --force", cwd}, "ask"},
		{"read is auto", Call{"Read", "/etc/hosts", cwd}, "allow"},
		{"edit inside the project", Call{"Edit", cwd + "/main.go", cwd}, "allow"},
		{"relative edit is inside", Call{"Write", "docs/x.md", cwd}, "allow"},
		{"edit outside the project asks", Call{"Edit", "/src/app/main.go", cwd}, ""},
		{"edit escaping with ..", Call{"Write", "../../secrets", cwd}, ""},
		{"unknown tool asks", Call{"WebFetch", "https://x", cwd}, ""},
	}
	for _, c := range cases {
		if got := p.Decide(c.call).Verdict; got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	turbo := Parse("mode: turbo\ndeny: Bash(rm -rf *)")
	if turbo.Decide(Call{"WebFetch", "https://x", cwd}).Verdict != "allow" {
		t.Error("turbo should allow")
	}
	if turbo.Decide(Call{"Bash", "rm -rf /", cwd}).Verdict != "ask" {
		t.Error("turbo deny should ask")
	}
	ask := Parse("mode: ask")
	if ask.Decide(Call{"Read", "x", cwd}).Verdict != "" {
		t.Error("ask mode approves nothing by itself")
	}
}

func TestGlob(t *testing.T) {
	for _, c := range []struct {
		p, s string
		want bool
	}{
		{"go test *", "go test ./...", true},
		{"go test *", "go testify", false},
		{"*", "anything at all", true},
		{"git *status*", "git -C x status --short", true},
		{"exact", "exact", true},
		{"exact", "exactly", false},
		{"*.go", "a/b.go", true},
		{"*.go", "a/b.gox", false},
	} {
		if got := glob(c.p, c.s); got != c.want {
			t.Errorf("glob(%q, %q) = %v", c.p, c.s, got)
		}
	}
}
