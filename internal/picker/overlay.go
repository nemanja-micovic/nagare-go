package picker

import (
	"image"

	"charm.land/lipgloss/v2"

	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

// shadowOffset is how far down and to the right an overlay casts. One cell.
// Two reads as a misaligned duplicate rather than a shadow, because terminal
// cells are tall enough that a single row already carries the depth.
const shadowOffset = 1

// placeOverlay draws fg centered on top of bg, preserving bg around it and
// casting a shadow down-right of the dialog.
//
// This is built on lipgloss v2's compositor rather than by splicing strings:
// layers carry real Z-order, so the ground, the backdrop, the shadow and the
// dialog stack in a declared order instead of an implied one, and the same
// layer set can answer a mouse hit test later. (The hand-rolled predecessor
// here descended from opencode's and lipgloss PR #102's approach, which
// predated the compositor existing.)
func placeOverlay(width, height int, fg, bg string, dy int) string {
	if width <= 0 || height <= 0 {
		return bg
	}

	c := theme.Current().Colors

	area := overlayRect(width, height, fg, dy)
	x, y := area.Min.X, area.Min.Y
	fgWidth, fgHeight := area.Dx(), area.Dy()

	// The ground layer pins the composite to exactly width×height. Without it
	// the bounds come from bg, which is ragged — short lines and a missing
	// final row both leave the shadow hanging off an edge that isn't there.
	ground := lipgloss.NewStyle().
		Background(canvasBg()).
		Width(width).
		Height(height).
		Render("")

	layers := []*lipgloss.Layer{
		lipgloss.NewLayer(ground).ID("ground").X(0).Y(0).Z(0),
		lipgloss.NewLayer(bg).ID("backdrop").X(0).Y(0).Z(1),
	}

	// A solid block offset behind the dialog: the dialog covers all of it but
	// the rim, which is the shadow.
	//
	// It is clamped to the room actually left rather than skipped when it
	// doesn't fit, so a dialog taller than the frame — the help screen, on a
	// short terminal — still casts to the right instead of losing its shadow
	// entirely. The clamp is not optional: a layer extending past the
	// compositor's bounds widens the composite, and the surplus columns then
	// get clipped off the *opposite* edge by the frame clamp in View().
	shadowWidth := min(fgWidth, width-x-shadowOffset)
	shadowHeight := min(fgHeight, height-y-shadowOffset)
	if shadowWidth > 0 && shadowHeight > 0 {
		shadow := lipgloss.NewStyle().
			Background(c.Shadow).
			Width(shadowWidth).
			Height(shadowHeight).
			Render("")
		layers = append(layers,
			lipgloss.NewLayer(shadow).ID("shadow").X(x+shadowOffset).Y(y+shadowOffset).Z(2))
	}

	layers = append(layers, lipgloss.NewLayer(fg).ID("dialog").X(x).Y(y).Z(3))

	frame := lipgloss.NewCompositor(layers...).Render()

	// The compositor's bounds grow to fit their layers, so a dialog larger than
	// the frame — the help screen on a short terminal — would hand back a
	// composite bigger than the size we were asked for, and every row would
	// wrap. Clamping here makes the function honor its own signature instead of
	// relying on the caller to clean up after it.
	return lipgloss.NewStyle().MaxWidth(width).MaxHeight(height).Render(frame)
}

// overlayRect reports where placeOverlay will draw fg: centered, clamped to the
// top-left when fg is larger than the frame.
//
// Hit-testing and drawing both go through this, so a dialog's clickable bounds
// cannot drift away from where it was actually painted.
// dy displaces it vertically, which is how the entry animation is expressed;
// zero is the resting position.
func overlayRect(width, height int, fg string, dy int) image.Rectangle {
	fgWidth, fgHeight := lipgloss.Width(fg), lipgloss.Height(fg)
	x := max((width-fgWidth)/2, 0)
	y := max((height-fgHeight)/2+dy, 0)
	return image.Rect(x, y, x+fgWidth, y+fgHeight)
}
