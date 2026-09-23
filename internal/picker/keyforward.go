package picker

import (
	"fmt"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// forwardedKey is a keypress translated for tmux: either text to type literally
// or one named key for send-keys. Exactly one of the two is set.
type forwardedKey struct {
	text string
	name string
}

// specialKeys maps Bubble Tea key codes to tmux key names.
var specialKeys = map[rune]string{
	tea.KeyEnter:     "Enter",
	tea.KeyTab:       "Tab",
	tea.KeyBackspace: "BSpace",
	tea.KeyEscape:    "Escape",
	tea.KeySpace:     "Space",
	tea.KeyUp:        "Up",
	tea.KeyDown:      "Down",
	tea.KeyLeft:      "Left",
	tea.KeyRight:     "Right",
	tea.KeyHome:      "Home",
	tea.KeyEnd:       "End",
	tea.KeyPgUp:      "PPage",
	tea.KeyPgDown:    "NPage",
	tea.KeyInsert:    "IC",
	tea.KeyDelete:    "DC",
	tea.KeyKpEnter:   "Enter",
}

// translateKey converts a keypress into what tmux should send to the pane. It
// reports false for keys that have no terminal encoding worth sending (a bare
// modifier, a media key), which are dropped rather than guessed at.
//
// tmux, not nagare, encodes the result: send-keys knows what the program in the
// pane asked for — application cursor keys, extended keys — which nagare,
// looking at its own terminal, cannot know.
func translateKey(k tea.Key) (forwardedKey, bool) {
	ctrl := k.Mod.Contains(tea.ModCtrl)
	alt := k.Mod.Contains(tea.ModAlt)
	shift := k.Mod.Contains(tea.ModShift)

	// Plain text, including shifted characters: type it as-is.
	if k.Text != "" && !ctrl && !alt {
		return forwardedKey{text: k.Text}, true
	}

	prefix := ""
	if ctrl {
		prefix += "C-"
	}
	if alt {
		prefix += "M-"
	}

	if name, ok := specialKeys[k.Code]; ok {
		switch {
		case k.Code == tea.KeyTab && shift && !ctrl && !alt:
			return forwardedKey{name: "BTab"}, true
		case k.Code == tea.KeyEnter && shift && !ctrl && !alt:
			// Shift+Enter is how most agents are asked for a newline, but a
			// terminal only sees it with extended keys negotiated end to end.
			// Meta+Enter (ESC CR) survives any terminal and is the portable
			// newline binding Claude Code documents.
			return forwardedKey{name: "M-Enter"}, true
		case shift && k.Code != tea.KeySpace:
			prefix += "S-"
		}
		return forwardedKey{name: prefix + name}, true
	}

	if k.Code >= tea.KeyF1 && k.Code <= tea.KeyF12 {
		if shift {
			prefix += "S-"
		}
		return forwardedKey{name: fmt.Sprintf("%sF%d", prefix, k.Code-tea.KeyF1+1)}, true
	}

	// A modified printable key: C-a, M-x, C-M-b.
	if k.Code > 0 && k.Code < tea.KeyExtended && unicode.IsPrint(k.Code) && (ctrl || alt) {
		r := k.Code
		if shift && alt && !ctrl {
			r = unicode.ToUpper(r)
		}
		return forwardedKey{name: prefix + string(r)}, true
	}

	return forwardedKey{}, false
}
