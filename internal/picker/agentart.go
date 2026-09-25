package picker

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/ansi"
	"github.com/nemanja-micovic/nagare-go/internal/models"
	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

// agentArt maps agent types to block-character letter logos.
// All logos are exactly 8 chars wide and 5 lines tall.
var agentArt = map[models.AgentType]string{
	models.AgentClaude: "" +
		" ██████╗\n" +
		"██╔════╝\n" +
		"██║     \n" +
		"██╚════╗\n" +
		" ██████╝",
	models.AgentOpenCode: "" +
		" ╔════╗ \n" +
		"║║╔══╗║║\n" +
		"║║║  ║║║\n" +
		"║║╚══╝║║\n" +
		" ╚════╝ ",
	models.AgentGemini: "" +
		" ██████╗\n" +
		"██╔════╝\n" +
		"██║ ███╗\n" +
		"██║  ██║\n" +
		" ██████╝",
	models.AgentCrush: "" +
		" ╔╗ ╔╗ \n" +
		" ╚╝╔╝╚╗\n" +
		"   ║♥♥║\n" +
		"   ╚╗╔╝\n" +
		"    ╚╝  ",
	models.AgentPi: "" +
		"███████╗\n" +
		" ██║██║ \n" +
		" ██║██║ \n" +
		" ██║██║ \n" +
		" ╚═╝╚═╝ ",
	models.AgentOhMyPi: "" +
		"╔══════╗\n" +
		"║ OMP  ║\n" +
		"║ ╭──╮ ║\n" +
		"║ ╰──╯ ║\n" +
		"╚══════╝",
	models.AgentCodex: "" +
		"██╗  ██╗\n" +
		"╚██╗██╔╝\n" +
		" ╚███╔╝ \n" +
		" ██╔██╗ \n" +
		"██╔╝╚██╗",
}

// agentGradients defines the start and end colors for each agent's gradient.
var agentGradients = map[models.AgentType][2]string{
	models.AgentClaude:   {"#e8825a", "#d4532b"},
	models.AgentOpenCode: {"#00e5ff", "#0088aa"},
	models.AgentGemini:   {"#6fa8ff", "#2b5fc7"},
	models.AgentCrush:    {"#ff8ce8", "#d43faa"},
	models.AgentPi:       {"#c4b5fd", "#7c5cf0"},
	models.AgentOhMyPi:   {"#ffe27a", "#d39b16"},
	models.AgentCodex:    {"#34d399", "#078566"},
}

// renderAgentArtSmall returns a compact single-line styled label for grid cells.
func renderAgentArtSmall(agent models.AgentType) string {
	grad, ok := agentGradients[agent]
	if !ok {
		return ""
	}
	from, _ := colorful.Hex(grad[0])
	bg := theme.Current().Colors.Surface
	label := strings.ToUpper(models.AgentLabel(agent))
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(from.Hex())).
		Background(bg).
		Bold(true)
	return style.Render("[ " + label + " ]")
}

// renderAgentArt returns a gradient-colored block logo for the given agent type,
// pre-padded to a fixed width with the theme background so JoinHorizontal
// doesn't introduce unstyled gaps.
func renderAgentArt(agent models.AgentType) string {
	art, ok := agentArt[agent]
	if !ok {
		return ""
	}

	grad, ok := agentGradients[agent]
	if !ok {
		return ""
	}

	lines := strings.Split(art, "\n")
	from, _ := colorful.Hex(grad[0])
	to, _ := colorful.Hex(grad[1])
	bg := theme.Current().Colors.Surface

	// Find the widest line to set a consistent block width.
	artWidth := 0
	for _, line := range lines {
		if w := ansi.PrintableRuneWidth(line); w > artWidth {
			artWidth = w
		}
	}

	totalLines := len(lines)
	var rendered []string
	for i, line := range lines {
		t := float64(i) / float64(max(totalLines-1, 1))
		c := from.BlendHcl(to, t).Clamped()
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(c.Hex())).
			Background(bg).
			Width(artWidth)
		rendered = append(rendered, style.Render(line))
	}

	return strings.Join(rendered, "\n")
}
