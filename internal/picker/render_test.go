package picker

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

var sgrRe2 = regexp.MustCompile("^\x1b\\[[0-9;:]*m")

type attr struct {
	fg, bg     string
	bold, dim  bool
	ital, undl bool
	rev        bool
}

// cellAttrs walks a rendered frame and returns one entry per printable cell,
// carrying the character and every attribute in effect. This is the invariant a
// render optimisation must not change: same characters, same colors, same styles.
func cellAttrs(frame string) []string {
	var out []string
	for _, line := range strings.Split(stripOSC(frame), "\n") {
		a := attr{fg: "def", bg: "def"}
		i := 0
		for i < len(line) {
			if seq := sgrRe2.FindString(line[i:]); seq != "" {
				a = applySGR(a, seq[2:len(seq)-1])
				i += len(seq)
				continue
			}
			r, size := decodeRune(line[i:])
			out = append(out, fmt.Sprintf("%s|%s|%s|%v%v%v%v%v",
				r, a.fg, a.bg, a.bold, a.dim, a.ital, a.undl, a.rev))
			i += size
		}
		out = append(out, "\n")
	}
	return out
}

func decodeRune(s string) (string, int) {
	for i := 1; i <= len(s); i++ {
		if i == len(s) || (s[i]&0xC0) != 0x80 {
			return s[:i], i
		}
	}
	return s[:1], 1
}

func applySGR(a attr, params string) attr {
	if params == "" {
		return attr{fg: "def", bg: "def"}
	}
	f := strings.Split(params, ";")
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case "0", "00":
			a = attr{fg: "def", bg: "def"}
		case "1":
			a.bold = true
		case "2":
			a.dim = true
		case "22":
			a.bold, a.dim = false, false
		case "3":
			a.ital = true
		case "23":
			a.ital = false
		case "4":
			a.undl = true
		case "24":
			a.undl = false
		case "7":
			a.rev = true
		case "27":
			a.rev = false
		case "39":
			a.fg = "def"
		case "49":
			a.bg = "def"
		case "38", "48":
			target := &a.fg
			if f[i] == "48" {
				target = &a.bg
			}
			if i+1 < len(f) && f[i+1] == "5" && i+2 < len(f) {
				*target = "5:" + f[i+2]
				i += 2
			} else if i+1 < len(f) && f[i+1] == "2" && i+4 < len(f) {
				*target = strings.Join(f[i+2:i+5], ",")
				i += 4
			}
		default:
			if n, err := strconv.Atoi(f[i]); err == nil {
				switch {
				case n >= 30 && n <= 37, n >= 90 && n <= 97:
					a.fg = "a" + f[i]
				case n >= 40 && n <= 47, n >= 100 && n <= 107:
					a.bg = "a" + f[i]
				}
			}
		}
	}
	return a
}

// fidelityFrames is the set of frames compared before and after optimisation.
func fidelityFrames(t *testing.T) map[string]string {
	t.Helper()
	frames := map[string]string{}

	add := func(name string, m Model) {
		frame, _ := m.view()
		frames[name] = frame
	}

	for _, n := range []int{1, 8, 30} {
		for _, sz := range [][2]int{{200, 50}, {120, 30}, {80, 24}} {
			m := benchModel(n, sz[0], sz[1])
			add(fmt.Sprintf("list/%d/%dx%d", n, sz[0], sz[1]), m)

			g := m
			g.viewMode = GridView
			g.gridPreviews = map[string]string{}
			for _, s := range g.filtered {
				g.gridPreviews[sessionKey(s)] = strings.Repeat("captured output line\n", 30)
			}
			add(fmt.Sprintf("grid/%d/%dx%d", n, sz[0], sz[1]), g)
		}
	}

	base := benchModel(12, 160, 40)
	f := base
	f.searchInput.SetValue("cosmic")
	f.applyFilter()
	add("filtered", f)

	h := base
	h.showHelp = true
	add("help", h)

	th := base
	th.showThemePick = true
	th.themeNames = theme.Names()
	add("themes", th)

	for _, name := range theme.Names() {
		prev := theme.Current().Name
		theme.Set(name)
		add("theme/"+name, benchModel(6, 120, 30))
		theme.Set(prev)
	}
	return frames
}

// frameFingerprint hashes the cell attributes of a frame — the characters and
// every colour and style in effect on each one. It is the invariant a rendering
// change must preserve, and it is deliberately insensitive to how the escape
// sequences are arranged: emitting one SGR per run instead of one per cell
// produces different bytes and identical cells.
func frameFingerprint(frame string) string {
	h := sha256.Sum256([]byte(strings.Join(cellAttrs(frame), "\x00")))
	return fmt.Sprintf("%x", h[:8])
}

// TestRenderingIsDeterministic — the same model must render the same cells twice.
//
// This exists because it caught a real one: the detail pane renders LastActivity
// as a *relative* time, so a frame quietly differed run to run. That made the
// harness used to verify render optimisations useless until it was found, and it
// would defeat any future golden-image or snapshot test the same way.
func TestRenderingIsDeterministic(t *testing.T) {
	first := fidelityFrames(t)
	second := fidelityFrames(t)

	if len(first) != len(second) {
		t.Fatalf("frame sets differ in size: %d vs %d", len(first), len(second))
	}
	for name, a := range first {
		b, ok := second[name]
		if !ok {
			t.Errorf("%s missing from the second render", name)
			continue
		}
		if fa, fb := frameFingerprint(a), frameFingerprint(b); fa != fb {
			t.Errorf("%s is not deterministic: %s then %s", name, fa, fb)
		}
	}
}

// TestCellAttrsSeesStyles guards the harness itself: a fingerprint that ignored
// colour would call any two frames identical and silently approve a regression.
func TestCellAttrsSeesStyles(t *testing.T) {
	plain := lipgloss.NewStyle().Render("ab")
	colored := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Render("ab")
	bold := lipgloss.NewStyle().Bold(true).Render("ab")
	onBg := lipgloss.NewStyle().Background(lipgloss.Color("#00ff00")).Render("ab")

	seen := map[string]string{}
	for name, s := range map[string]string{
		"plain": plain, "colored": colored, "bold": bold, "background": onBg,
	} {
		fp := frameFingerprint(s)
		if prev, dup := seen[fp]; dup {
			t.Errorf("%s and %s fingerprint identically; the harness is blind to them", prev, name)
		}
		seen[fp] = name
	}
}
