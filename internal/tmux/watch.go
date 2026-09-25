package tmux

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// Watcher reports which panes have printed output, as it happens.
//
// Focus mode captures a pane to draw it. Without a signal it can only guess
// when to capture again, and guessing means polling: fast enough to feel live
// costs CPU while an agent sits idle, and slow enough to be cheap adds latency to
// every keystroke's echo. tmux already knows the moment a pane prints — a
// control-mode client (tmux -C) is told with a %output line — so the watcher
// attaches one per session with a tiled pane and forwards the pane ids. The
// capture itself is unchanged: tmux still renders the screen, and nagare needs
// no terminal emulator of its own.
//
// The clients attach with ignore-size, so they never take part in sizing
// windows, and they only listen: nothing is ever sent to them.
type Watcher struct {
	mu      sync.Mutex
	clients map[string]*controlClient // by session name
	events  chan string
	healthy atomic.Bool
	closed  bool
}

type controlClient struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

// NewWatcher returns a watcher watching nothing.
func NewWatcher() *Watcher {
	return &Watcher{clients: map[string]*controlClient{}, events: make(chan string, 512)}
}

// Events delivers the id of each pane that printed output. Bursts are not
// deduplicated here; when the buffer is full, events are dropped, which loses
// nothing — the pane will be captured anyway.
func (w *Watcher) Events() <-chan string { return w.events }

// Healthy reports whether any control client has completed its handshake, so
// output events can be relied on.
func (w *Watcher) Healthy() bool { return w.healthy.Load() }

// Watch makes the watched sessions exactly these, attaching and detaching
// control clients as needed.
func (w *Watcher) Watch(sessions []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	want := map[string]bool{}
	for _, s := range sessions {
		if s != "" {
			want[s] = true
		}
	}
	for s, c := range w.clients {
		if !want[s] {
			c.stop()
			delete(w.clients, s)
		}
	}
	for s := range want {
		if _, ok := w.clients[s]; ok {
			continue
		}
		if c := w.start(s); c != nil {
			w.clients[s] = c
		}
	}
	if len(w.clients) == 0 {
		w.healthy.Store(false)
	}
}

// Close detaches every control client.
func (w *Watcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for s, c := range w.clients {
		c.stop()
		delete(w.clients, s)
	}
	w.closed = true
	w.healthy.Store(false)
}

func (w *Watcher) start(session string) *controlClient {
	// "=" makes the target an exact session name, not a prefix match.
	cmd := Command("-C", "attach-session", "-t", "="+session, "-f", "ignore-size")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	c := &controlClient{cmd: cmd, stdin: stdin}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			switch pane, kind := parseControlLine(sc.Text()); kind {
			case controlOutput:
				select {
				case w.events <- pane:
				default:
				}
			case controlReady:
				w.healthy.Store(true)
			}
		}
		cmd.Wait()
		// The session ended, or the client was stopped. Forget it, so a later
		// Watch for the same name starts a fresh one.
		w.mu.Lock()
		if w.clients[session] == c {
			delete(w.clients, session)
		}
		w.mu.Unlock()
	}()
	return c
}

// stop detaches the client: a control client exits when its stdin closes.
func (c *controlClient) stop() {
	c.stdin.Close()
}

type controlKind int

const (
	controlOther controlKind = iota
	controlOutput
	controlReady
)

// parseControlLine reads one line of control-mode output. Only two things
// matter: a pane printed something, or the client is up (its first %begin).
func parseControlLine(line string) (pane string, kind controlKind) {
	switch {
	case strings.HasPrefix(line, "%output "):
		rest := line[len("%output "):]
		if i := strings.IndexByte(rest, ' '); i > 0 {
			rest = rest[:i]
		}
		if strings.HasPrefix(rest, "%") {
			return rest, controlOutput
		}
	case strings.HasPrefix(line, "%extended-output "):
		f := strings.Fields(line)
		if len(f) > 1 && strings.HasPrefix(f[1], "%") {
			return f[1], controlOutput
		}
	case strings.HasPrefix(line, "%begin "):
		return "", controlReady
	}
	return "", controlOther
}
