package tmux

import "testing"

func TestParseScreen(t *testing.T) {
	out := "\x1b[1mhello\x1b[0m\nworld\n\n3 1 1 80 3 120 0 2\n"
	sc, err := parseScreen(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Lines) != 3 || sc.Lines[0] != "\x1b[1mhello\x1b[0m" || sc.Lines[1] != "world" || sc.Lines[2] != "" {
		t.Errorf("lines = %q", sc.Lines)
	}
	if sc.CursorX != 3 || sc.CursorY != 1 || !sc.CursorVisible {
		t.Errorf("cursor = %d,%d visible=%v", sc.CursorX, sc.CursorY, sc.CursorVisible)
	}
	if sc.Width != 80 || sc.Height != 3 || sc.History != 120 || sc.Zoomed || sc.Panes != 2 {
		t.Errorf("geometry = %+v", sc)
	}
}

func TestParseScreenRejectsGarbage(t *testing.T) {
	for _, out := range []string{"", "just text\n", "a b c d e f g h\n"} {
		if _, err := parseScreen(out); err == nil {
			t.Errorf("parseScreen(%q) accepted malformed geometry", out)
		}
	}
}
