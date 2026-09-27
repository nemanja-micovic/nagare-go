package picker

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTranslateKey(t *testing.T) {
	cases := []struct {
		name string
		key  tea.Key
		want forwardedKey
	}{
		{"letter", tea.Key{Code: 'a', Text: "a"}, forwardedKey{text: "a"}},
		{"shifted letter", tea.Key{Code: 'a', Text: "A", Mod: tea.ModShift}, forwardedKey{text: "A"}},
		{"unicode", tea.Key{Code: 'ž', Text: "ž"}, forwardedKey{text: "ž"}},
		{"space", tea.Key{Code: tea.KeySpace, Text: " "}, forwardedKey{text: " "}},
		{"enter", tea.Key{Code: tea.KeyEnter}, forwardedKey{name: "Enter"}},
		{"shift+enter is a newline", tea.Key{Code: tea.KeyEnter, Mod: tea.ModShift}, forwardedKey{name: "M-Enter"}},
		{"alt+enter", tea.Key{Code: tea.KeyEnter, Mod: tea.ModAlt}, forwardedKey{name: "M-Enter"}},
		{"escape", tea.Key{Code: tea.KeyEscape}, forwardedKey{name: "Escape"}},
		{"backspace", tea.Key{Code: tea.KeyBackspace}, forwardedKey{name: "BSpace"}},
		{"tab", tea.Key{Code: tea.KeyTab}, forwardedKey{name: "Tab"}},
		{"shift+tab", tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}, forwardedKey{name: "BTab"}},
		{"up", tea.Key{Code: tea.KeyUp}, forwardedKey{name: "Up"}},
		{"ctrl+left", tea.Key{Code: tea.KeyLeft, Mod: tea.ModCtrl}, forwardedKey{name: "C-Left"}},
		{"shift+right", tea.Key{Code: tea.KeyRight, Mod: tea.ModShift}, forwardedKey{name: "S-Right"}},
		{"page down", tea.Key{Code: tea.KeyPgDown}, forwardedKey{name: "NPage"}},
		{"delete", tea.Key{Code: tea.KeyDelete}, forwardedKey{name: "DC"}},
		{"ctrl+c", tea.Key{Code: 'c', Mod: tea.ModCtrl}, forwardedKey{name: "C-c"}},
		{"alt+b", tea.Key{Code: 'b', Mod: tea.ModAlt}, forwardedKey{name: "M-b"}},
		{"ctrl+alt+x", tea.Key{Code: 'x', Mod: tea.ModCtrl | tea.ModAlt}, forwardedKey{name: "C-M-x"}},
		{"f2", tea.Key{Code: tea.KeyF2}, forwardedKey{name: "F2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := translateKey(tc.key)
			if !ok {
				t.Fatalf("translateKey(%v) was dropped", tc.key)
			}
			if got != tc.want {
				t.Errorf("translateKey(%v) = %+v, want %+v", tc.key, got, tc.want)
			}
		})
	}
}

func TestTranslateKeyDropsBareModifiers(t *testing.T) {
	if _, ok := translateKey(tea.Key{Code: tea.KeyLeftShift, Mod: tea.ModShift}); ok {
		t.Error("a bare shift press should not be forwarded")
	}
}
