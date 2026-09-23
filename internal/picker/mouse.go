package picker

import (
	"image"

	tea "charm.land/bubbletea/v2"
)

// Mouse messages. Clicks are translated into intent here rather than being
// handled where they land, so the key handler and the mouse handler converge on
// the same code paths and cannot drift apart.
type (
	// mouseSelectMsg moves the cursor to a session.
	mouseSelectMsg struct{ index int }
	// mouseActivateMsg jumps to a session, as Enter would.
	mouseActivateMsg struct{ index int }
	// mouseDismissMsg closes an open overlay, as Esc would.
	mouseDismissMsg struct{}
	// mouseScrollMsg moves the cursor by delta rows.
	mouseScrollMsg struct{ delta int }
	// mouseFocusMsg focuses a session, from a click in focus mode's sidebar.
	mouseFocusMsg struct{ index int }
	// mouseTermScrollMsg scrolls the focused terminal by delta rows (positive
	// is back into history).
	mouseTermScrollMsg struct{ delta int }
)

// termScrollStep is how many rows a wheel notch scrolls a focused terminal —
// the three most terminals use.
const termScrollStep = 3

// hitTargets records where things were drawn, so a click can be resolved back
// to what the user actually clicked on.
//
// It is built during View — the only place the layout is genuinely known, since
// panel heights depend on wrapping — and captured by the OnMouse closure that
// Bubble Tea calls with coordinates from the last rendered frame. Recomputing
// the geometry inside Update would mean a second copy of the layout arithmetic
// that could silently disagree with the copy that actually drew the screen.
type hitTargets struct {
	// sessionAt maps a frame row to the session index drawn on it. Rows holding
	// a group header, or nothing, are simply absent.
	sessionAt map[int]int
	// listWidth bounds the clickable region for sessionAt. Clicks to the right
	// of it landed on the detail or preview panel, which is not a target.
	listWidth int
	// cards are grid-view card rectangles. Non-empty only in grid view.
	cards []cardHit
	// dialog bounds an open overlay. Empty when none is open.
	dialog image.Rectangle
	// dismissable is whether clicking outside dialog should close it. Modal
	// decisions — remove this worktree, send this prompt — are not dismissed by
	// a stray click; they want a deliberate answer.
	dismissable bool
	// focus is set in focus mode, where the sidebar and terminal take over.
	focus bool
	// term bounds the focused terminal panel.
	term image.Rectangle
}

type cardHit struct {
	index int
	area  image.Rectangle
}

// resolve turns a mouse event into intent, or nil when nothing was hit.
//
// cursor is the currently selected session, because the second click on an
// already-selected target is what activates it. Selecting and jumping on a
// single click would make one stray click abandon the picker.
func (h hitTargets) resolve(msg tea.MouseMsg, cursor int) tea.Msg {
	mouse := msg.Mouse()

	switch msg.(type) {
	case tea.MouseWheelMsg:
		if h.focus {
			// The wheel scrolls what it is over. Over the terminal that is the
			// agent's history; the sidebar is short enough not to need it, and
			// switching agents on a wheel notch would be far too easy to do.
			if h.dialog.Empty() && image.Pt(mouse.X, mouse.Y).In(h.term) {
				switch mouse.Button {
				case tea.MouseWheelUp:
					return mouseTermScrollMsg{delta: termScrollStep}
				case tea.MouseWheelDown:
					return mouseTermScrollMsg{delta: -termScrollStep}
				}
			}
			return nil
		}
		switch mouse.Button {
		case tea.MouseWheelUp:
			return mouseScrollMsg{delta: -1}
		case tea.MouseWheelDown:
			return mouseScrollMsg{delta: 1}
		}
		return nil

	case tea.MouseClickMsg:
		if mouse.Button != tea.MouseLeft {
			return nil
		}
		return h.resolveClick(mouse.X, mouse.Y, cursor)
	}

	return nil
}

func (h hitTargets) resolveClick(x, y, cursor int) tea.Msg {
	// An open overlay owns the whole screen: a click inside it is the dialog's
	// business, and a click outside means "get rid of this".
	if !h.dialog.Empty() {
		if h.dismissable && !image.Pt(x, y).In(h.dialog) {
			return mouseDismissMsg{}
		}
		return nil
	}

	// In focus mode one click on another agent switches to it. The list's
	// select-then-activate guard exists because activating used to leave nagare;
	// switching focus leaves nothing and is undone by clicking back.
	if h.focus {
		if x < h.listWidth {
			if idx, ok := h.sessionAt[y]; ok {
				return mouseFocusMsg{index: idx}
			}
		}
		return nil
	}

	for _, card := range h.cards {
		if image.Pt(x, y).In(card.area) {
			return activateOrSelect(card.index, cursor)
		}
	}

	if x < h.listWidth {
		if idx, ok := h.sessionAt[y]; ok {
			return activateOrSelect(idx, cursor)
		}
	}

	return nil
}

func activateOrSelect(index, cursor int) tea.Msg {
	if index == cursor {
		return mouseActivateMsg{index: index}
	}
	return mouseSelectMsg{index: index}
}
