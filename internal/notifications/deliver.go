package notifications

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/nemanja-micovic/nagare-go/internal/bin"
	"github.com/nemanja-micovic/nagare-go/internal/tmux"
)

// BuildToastMessage creates a human-readable notification message.
func BuildToastMessage(sessionName, eventType, notificationType string) string {
	switch eventType {
	case "needs_input":
		if notificationType == "permission_prompt" {
			return fmt.Sprintf("🔴 %s needs permission", sessionName)
		}
		return fmt.Sprintf("🔴 %s needs input", sessionName)
	case "task_complete":
		return fmt.Sprintf("✅ %s finished", sessionName)
	default:
		return fmt.Sprintf("📢 %s: %s", sessionName, eventType)
	}
}

// SendToast sends a tmux status bar message.
func SendToast(message string, durationMs int) {
	client := tmux.RunTmux("list-clients", "-F", "#{client_name}")
	lines := strings.Split(client, "\n")
	if len(lines) == 0 || lines[0] == "" {
		return
	}
	tmux.RunTmux("display-message", "-t", lines[0], "-d", fmt.Sprintf("%d", durationMs), message)
}

// SendBell sends a terminal bell character.
func SendBell() {
	tmux.RunTmux("run-shell", `printf '\a'`)
}

// SendOsNotify sends a native OS notification.
func SendOsNotify(title, message string) {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		if path, err := exec.LookPath("wsl-notify-send"); err == nil {
			exec.Command(path, title, message).Start()
		}
		return
	}

	if path, err := exec.LookPath("notify-send"); err == nil {
		exec.Command(path, title, message).Start()
		return
	}

	if path, err := exec.LookPath("osascript"); err == nil {
		script := fmt.Sprintf(`display notification "%s" with title "%s"`, message, title)
		exec.Command(path, "-e", script).Start()
	}
}

// Deliver dispatches a pre-built message through the enabled channels.
func Deliver(message string, toast, bell, osNotify bool, durationMs int) {
	if toast {
		SendToast(message, durationMs)
	}
	if bell {
		SendBell()
	}
	if osNotify {
		SendOsNotify("nagare", message)
	}
}

// SendPopup opens a popup notification in a tmux split.
func SendPopup(sessionName, eventType, message string, workingSeconds, popupTimeout int) {
	nagareBin := bin.FindSelf()
	args := []string{
		"popup-notif",
		"--session", sessionName,
		"--event", eventType,
		"--message", message,
		"--timeout", fmt.Sprintf("%d", popupTimeout),
	}
	if workingSeconds > 0 {
		args = append(args, "--duration", fmt.Sprintf("%d", workingSeconds))
	}

	// Get active pane to split from
	paneID := tmux.RunTmux("display-message", "-p", "#{pane_id}")
	if paneID == "" {
		return
	}
	cmd := strings.Join(append([]string{nagareBin}, args...), " ")
	tmux.RunTmux("split-window", "-t", paneID, "-v", "-l", "30%", cmd)
}
