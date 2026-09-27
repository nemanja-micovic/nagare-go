package theme

import (
	"fmt"
	"image/color"
	"strings"
)

// OnPlane reasserts bg after ANSI resets so styled content cannot punch holes
// through a lifted surface to the terminal's default background.
func OnPlane(content string, bg color.Color) string {
	if content == "" {
		return content
	}
	set := backgroundSequence(bg)
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lines[index] = set + reassertBackground(line, set)
	}
	return strings.Join(lines, "\n")
}

func backgroundSequence(bg color.Color) string {
	r, g, b, _ := bg.RGBA()
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

func reassertBackground(line, set string) string {
	var result strings.Builder
	result.Grow(len(line) + len(set))
	for index := 0; index < len(line); {
		if sequence, length := scanSGR(line[index:]); length > 0 {
			result.WriteString(sequence)
			index += length
			if index < len(line) && clearsBackground(sequence) {
				result.WriteString(set)
			}
			continue
		}
		result.WriteByte(line[index])
		index++
	}
	return result.String()
}

func scanSGR(value string) (string, int) {
	if !strings.HasPrefix(value, "\x1b[") {
		return "", 0
	}
	for index := 2; index < len(value); index++ {
		char := value[index]
		if char == 'm' {
			return value[:index+1], index + 1
		}
		if (char < '0' || char > '9') && char != ';' && char != ':' {
			return "", 0
		}
	}
	return "", 0
}

func extendedColorParams(fields []string, index int) int {
	if index+1 >= len(fields) {
		return 0
	}
	switch fields[index+1] {
	case "5":
		return 2
	case "2":
		return 4
	default:
		return 0
	}
}

func clearsBackground(sequence string) bool {
	params := sequence[2 : len(sequence)-1]
	if params == "" {
		return true
	}
	cleared := false
	fields := strings.Split(params, ";")
	for index := 0; index < len(fields); index++ {
		switch field := fields[index]; field {
		case "0", "00", "49":
			cleared = true
		case "48":
			cleared = false
			index += extendedColorParams(fields, index)
		case "38", "58":
			index += extendedColorParams(fields, index)
		default:
			if len(field) == 2 && field[0] == '4' && field[1] >= '0' && field[1] <= '7' {
				cleared = false
			}
			if len(field) == 3 && field[0] == '1' && field[1] == '0' && field[2] >= '0' && field[2] <= '7' {
				cleared = false
			}
		}
	}
	return cleared
}

// BackgroundSequence returns the SGR sequence that sets bg, for callers that
// assemble lines by hand and reassert the plane themselves.
func BackgroundSequence(bg color.Color) string { return backgroundSequence(bg) }

// ReassertBackground re-applies set after every SGR sequence in line that
// clears the background.
func ReassertBackground(line, set string) string { return reassertBackground(line, set) }
