package picker

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/nemanja-micovic/nagare-go/internal/models"
	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

// Activity sparklines.
//
// The picker shows what every agent is doing *now* and nothing about what it has
// been doing. Those are different questions: an agent that has been grinding for
// ten minutes and one that woke up four seconds ago look identical in a list of
// status dots, and which is which changes what you do about it.
//
// Each scan appends one sample per session, so the trace is a free by-product of
// the polling the picker already does.
//
// Braille is the right glyph for this because a cell holds 2x4 dots: two samples
// wide and four levels tall per character. Nothing else in Unicode gives that
// density, and unlike the image protocols it needs no terminal capability
// negotiation and no passthrough games with tmux — it is just text.
const (
	// sparkSamples is how much history to keep. At the picker's 2s scan interval
	// this is a little over a minute and a half, which is the timescale an agent's
	// bursts of work actually happen on.
	sparkSamples = 48
	// sparkLevels is the number of dot rows in a braille cell.
	sparkLevels = 4
	// brailleBlank is the empty braille cell, the base every pattern is added to.
	brailleBlank = 0x2800
)

// Dot bit values within a braille cell, bottom-up for each of its two columns:
//
//	1 4
//	2 5
//	3 6
//	7 8
//
// A bar of height h lights the lowest h dots of its column.
var (
	brailleLeft  = [sparkLevels]rune{0x40, 0x04, 0x02, 0x01}
	brailleRight = [sparkLevels]rune{0x80, 0x20, 0x10, 0x08}
)

// activityLevel maps a status onto a bar height. The order matters more than the
// numbers: an agent waiting on the user has to out-rank one that is working, so a
// glance at the trace finds the moments the user was needed.
func activityLevel(s models.SessionStatus) uint8 {
	switch s {
	case models.StatusWaitingInput:
		return 4
	case models.StatusRunning:
		return 3
	case models.StatusIdle:
		return 1
	default: // dead, saved, unknown
		return 0
	}
}

// recordActivity appends this scan's statuses to the history and drops sessions
// that have gone away, so the map cannot grow without bound over a long session.
func recordActivity(hist map[string][]uint8, sessions []models.Session) {
	seen := make(map[string]struct{}, len(sessions))
	for _, s := range sessions {
		key := sessionKey(s)
		seen[key] = struct{}{}
		samples := append(hist[key], activityLevel(s.Status))
		if len(samples) > sparkSamples {
			samples = samples[len(samples)-sparkSamples:]
		}
		hist[key] = samples
	}
	for key := range hist {
		if _, ok := seen[key]; !ok {
			delete(hist, key)
		}
	}
}

// levelColor is the colour for a bar of a given height, so the trace reads without
// a legend: the same colours the status dots use.
func levelColor(level uint8) color.Color {
	switch {
	case level >= 4:
		return lipgloss.Color(models.StatusColor(models.StatusWaitingInput))
	case level == 3:
		return lipgloss.Color(models.StatusColor(models.StatusRunning))
	default:
		return lipgloss.Color(models.StatusColor(models.StatusIdle))
	}
}

// sparkline renders the most recent samples as at most width braille cells, two
// samples to a cell, oldest on the left.
//
// Each cell is coloured by the louder of its two samples: a single moment of
// waiting inside a long run of work is the thing worth seeing, so it wins the cell
// rather than being averaged away.
func sparkline(levels []uint8, width int) string {
	if width < 1 || len(levels) == 0 {
		return ""
	}

	// Two samples per cell, and only as much history as fits.
	if max := width * 2; len(levels) > max {
		levels = levels[len(levels)-max:]
	}

	var b strings.Builder
	b.Grow(width * 24)
	for i := 0; i < len(levels); i += 2 {
		left := levels[i]
		var right uint8
		if i+1 < len(levels) {
			right = levels[i+1]
		}

		cell := rune(brailleBlank)
		for h := uint8(0); h < left && h < sparkLevels; h++ {
			cell |= brailleLeft[h]
		}
		for h := uint8(0); h < right && h < sparkLevels; h++ {
			cell |= brailleRight[h]
		}

		loud := left
		if right > loud {
			loud = right
		}
		b.WriteString(lipgloss.NewStyle().Foreground(levelColor(loud)).Render(string(cell)))
	}
	return b.String()
}

// sparklineOn draws the trace over a known background, so it sits inside a tinted
// row or a lifted panel without punching a hole through it.
func sparklineOn(levels []uint8, width int, bg color.Color) string {
	if bg == nil {
		return sparkline(levels, width)
	}
	return onPlane(sparkline(levels, width), bg)
}

// sparkWidth is how many cells to spend on a trace, given the room available. It
// stays small: this is a glance, not a chart, and the space belongs to the name.
func sparkWidth(available int) int {
	const (
		minWidth = 6
		maxWidth = 12
	)
	if available < minWidth {
		return 0
	}
	if available > maxWidth {
		return maxWidth
	}
	return available
}

// sparkLegend describes what the trace means, for the detail pane. Colour carries
// the status, height carries it again, and neither is any use unaided the first
// time someone sees it.
func sparkLegend() string {
	c := theme.Current().Colors
	return lipgloss.NewStyle().Foreground(c.Muted).Render("idle · working · waiting")
}
