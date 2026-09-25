package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestParseControlLine(t *testing.T) {
	cases := []struct {
		line string
		pane string
		kind controlKind
	}{
		{`%output %12 hello\015\012`, "%12", controlOutput},
		{`%output %3 `, "%3", controlOutput},
		{`%extended-output %7 250 : data`, "%7", controlOutput},
		{`%begin 1790371947 268 0`, "", controlReady},
		{`%end 1790371947 268 0`, "", controlOther},
		{`%session-changed $0 w1`, "", controlOther},
		{`%output garbage`, "", controlOther},
	}
	for _, c := range cases {
		pane, kind := parseControlLine(c.line)
		if pane != c.pane || kind != c.kind {
			t.Errorf("parseControlLine(%q) = %q, %d; want %q, %d", c.line, pane, kind, c.pane, c.kind)
		}
	}
}

// TestWatcherReportsOutput runs a real control client against a private tmux
// server — never the user's — and checks that output in a pane arrives as an
// event, and that watching nothing detaches the client.
func TestWatcherReportsOutput(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sock := fmt.Sprintf("nagare-test-%d", os.Getpid())
	t.Setenv(SocketEnv, sock)
	t.Cleanup(func() { exec.Command("tmux", "-L", sock, "kill-server").Run() })
	if out, err := Command("new-session", "-d", "-s", "w", "-x", "80", "-y", "20", "cat").CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, out)
	}
	pane := RunTmux("display-message", "-p", "-t", "w", "#{pane_id}")

	w := NewWatcher()
	defer w.Close()
	w.Watch([]string{"w"})
	deadline := time.Now().Add(3 * time.Second)
	for !w.Healthy() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !w.Healthy() {
		t.Fatal("the control client never came up")
	}
	if size := RunTmux("display-message", "-p", "-t", "w", "#{window_width}x#{window_height}"); size != "80x20" {
		t.Errorf("attaching the watcher resized the window to %s", size)
	}

	RunTmux("send-keys", "-t", "w", "-l", "hello")
	select {
	case got := <-w.Events():
		if got != pane {
			t.Errorf("event for %q, want %q", got, pane)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no output event")
	}

	w.Watch(nil)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if RunTmux("list-clients", "-F", "#{client_control_mode}") == "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("watching nothing left the control client attached")
}
