package demo

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/paths"
	"github.com/nemke/nagare-go/internal/state"
)

// A simulated coding agent, for the demo.
//
// It is the nagare binary itself, started through a symlink named after the
// agent it plays (claude, codex, opencode) so the scanner detects it exactly as
// it detects the real thing. It reports status through the same state files the
// real hooks write, edits real files in a real git repository, and stops at
// permission prompts that wait for an answer — so everything nagare shows about
// it is produced the way it would be for a real agent.

// Env vars the demo sets for its agents.
const (
	agentEnv    = "NAGARE_DEMO"          // set in every demo process
	scenarioEnv = "NAGARE_DEMO_SCENARIO" // which script an agent plays
	speedEnv    = "NAGARE_DEMO_SPEED"    // time multiplier; 2 runs twice as fast
	parentEnv   = "NAGARE_DEMO_PARENT"   // pid of the nagare that started the demo
)

// IsAgent reports whether this process was started as a demo agent.
func IsAgent() bool {
	if os.Getenv(agentEnv) == "" {
		return false
	}
	_, ok := looks[filepath.Base(os.Args[0])]
	return ok
}

// look is how an agent draws itself, loosely after the real one.
type look struct {
	banner string // first line on start
	bullet string // marks an assistant message
	accent string // SGR colour for the spinner and banner
	spin   []string
	verb   string
}

var looks = map[string]look{
	"claude": {
		banner: "✻ Welcome to Claude Code!",
		bullet: "\x1b[38;5;114m⏺\x1b[0m",
		accent: "\x1b[38;5;209m",
		spin:   []string{"·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"},
		verb:   "Thinking",
	},
	"codex": {
		banner: ">_ OpenAI Codex (research preview)",
		bullet: "\x1b[1m•\x1b[0m",
		accent: "\x1b[38;5;75m",
		spin:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		verb:   "Working",
	},
	"opencode": {
		banner: "opencode",
		bullet: "\x1b[38;5;180m▍\x1b[0m",
		accent: "\x1b[38;5;180m",
		spin:   []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
		verb:   "Generating",
	},
}

// agent is one running simulated agent.
type agent struct {
	look  look
	in    *bufio.Reader
	speed float64
	pane  string
	cwd   string
	id    string
	last  string // last assistant message, for the hook state
}

// RunAgent plays the scenario named in the environment until stdin closes.
func RunAgent() {
	name := filepath.Base(os.Args[0])
	speed, _ := strconv.ParseFloat(os.Getenv(speedEnv), 64)
	if speed <= 0 {
		speed = 1
	}
	cwd, _ := os.Getwd()
	pane := os.Getenv("TMUX_PANE")
	a := &agent{
		look:  looks[name],
		in:    bufio.NewReader(os.Stdin),
		speed: speed,
		pane:  pane,
		cwd:   cwd,
		id:    "demo-" + strings.TrimPrefix(pane, "%") + "-" + name,
	}
	go exitWithParent()

	sc, ok := scenarios[os.Getenv(scenarioEnv)]
	if !ok {
		sc = scenario{task: "Look around the repository", steps: followUp("Look around the repository")}
	}

	fmt.Printf("%s\x1b[1m%s\x1b[0m\n", a.look.accent, a.look.banner)
	fmt.Printf("\x1b[38;5;244m  cwd: %s\x1b[0m\n\n", cwd)
	time.Sleep(a.dur(sc.delay))

	a.userSays(sc.task)
	a.run(sc.steps)
	for {
		text, ok := a.prompt()
		if !ok {
			return
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		a.run(followUp(text))
	}
}

// dur scales a nominal duration by the demo speed.
func (a *agent) dur(d time.Duration) time.Duration {
	return time.Duration(float64(d) / a.speed)
}

// hookStates are the state strings hooks write, which are not the status
// constants: a working agent is "working" on disk and StatusRunning in memory.
var hookStates = map[models.SessionStatus]string{
	models.StatusRunning:      "working",
	models.StatusWaitingInput: "waiting_input",
	models.StatusIdle:         "idle",
}

// report writes the agent's state the way the real hooks do.
func (a *agent) report(status models.SessionStatus, event string) {
	state.WriteState(filepath.Join(paths.Data(), "states"), models.SessionState{
		State:       hookStates[status],
		SessionID:   a.id,
		Cwd:         a.cwd,
		PaneID:      a.pane,
		Event:       event,
		LastMessage: a.last,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	})
}

func (a *agent) width() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w < 20 {
		return 80
	}
	return w
}

func (a *agent) userSays(text string) {
	fmt.Printf("\x1b[38;5;244m>\x1b[0m %s\n\n", text)
}

// prompt shows the input line and waits for the user.
func (a *agent) prompt() (string, bool) {
	a.report(models.StatusIdle, "Stop")
	rule := strings.Repeat("─", a.width())
	fmt.Printf("\x1b[38;5;240m%s\x1b[0m\n❯ ", rule)
	line, err := a.in.ReadString('\n')
	if err != nil {
		return "", false
	}
	fmt.Println()
	return strings.TrimRight(line, "\r\n"), true
}

// step is one beat of a scenario.
type step struct {
	think  time.Duration // spinner for this long
	say    string        // assistant message
	tool   string        // tool call, e.g. Read(path)
	result string        // tool result line
	edit   *edit         // file change
	ask    string        // permission prompt; waits for an answer
}

type edit struct {
	path  string
	lines []string // appended to the file and shown as a diff
}

func (a *agent) run(steps []step) {
	a.report(models.StatusRunning, "UserPromptSubmit")
	for _, s := range steps {
		if s.think > 0 {
			a.spin(s.think)
		}
		if s.say != "" {
			a.last = s.say
			fmt.Printf("%s %s\n\n", a.look.bullet, s.say)
		}
		if s.tool != "" {
			fmt.Printf("%s \x1b[1m%s\x1b[0m\n", a.look.bullet, s.tool)
			if s.ask == "" && s.result != "" {
				fmt.Printf("  \x1b[38;5;244m⎿  %s\x1b[0m\n\n", s.result)
			}
		}
		if s.edit != nil {
			a.apply(*s.edit)
		}
		if s.ask != "" {
			if !a.permission(s.tool, s.ask) {
				a.last = "Stopped: permission declined."
				fmt.Printf("  \x1b[38;5;203m⎿  Declined. Tell me what to do instead.\x1b[0m\n\n")
				return
			}
			a.spin(1200 * time.Millisecond)
			if s.result != "" {
				fmt.Printf("  \x1b[38;5;244m⎿  %s\x1b[0m\n\n", s.result)
			}
		}
	}
}

// spin animates the working line for d, then clears it.
func (a *agent) spin(d time.Duration) {
	a.report(models.StatusRunning, "PreToolUse")
	d = a.dur(d)
	start := time.Now()
	for i := 0; time.Since(start) < d; i++ {
		frame := a.look.spin[i%len(a.look.spin)]
		secs := int(time.Since(start).Seconds())
		fmt.Printf("\r\x1b[K%s%s %s…\x1b[0m \x1b[38;5;244m(%ds · esc to interrupt)\x1b[0m",
			a.look.accent, frame, a.look.verb, secs)
		time.Sleep(90 * time.Millisecond)
	}
	fmt.Print("\r\x1b[K")
}

// apply appends an edit's lines to the file and prints them as a diff.
func (a *agent) apply(e edit) {
	path := filepath.Join(a.cwd, e.path)
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintln(f, strings.Join(e.lines, "\n"))
		f.Close()
	}
	start := 10 + rand.IntN(40)
	fmt.Printf("%s \x1b[1mUpdate\x1b[0m(%s)\n", a.look.bullet, e.path)
	fmt.Printf("  \x1b[38;5;244m⎿  Updated %s with %d additions\x1b[0m\n", e.path, len(e.lines))
	for i, l := range e.lines {
		// Tabs expanded, as real agents show diffs: a tab inside a highlighted
		// run renders at whatever width the terminal likes.
		l = strings.ReplaceAll(l, "\t", "    ")
		fmt.Printf("     \x1b[48;2;34;60;38m %3d + %-60s\x1b[0m\n", start+i, l)
	}
	fmt.Println()
}

// permission draws a permission dialog and waits for the answer: Enter, 1 or y
// approves, 2 approves always, anything else declines.
func (a *agent) permission(tool, question string) bool {
	a.last = "Permission needed: " + tool
	a.report(models.StatusWaitingInput, "Notification")
	b := "\x1b[38;5;75m"
	r := "\x1b[0m"
	inner := 50
	pad := func(s string) string {
		n := inner - len([]rune(s))
		if n < 0 {
			n = 0
		}
		return s + strings.Repeat(" ", n)
	}
	fmt.Printf("%s╭%s╮%s\n", b, strings.Repeat("─", inner+2), r)
	fmt.Printf("%s│%s \x1b[1m%s\x1b[0m %s│%s\n", b, r, pad("Bash command"), b, r)
	fmt.Printf("%s│%s   %s %s│%s\n", b, r, pad(question)[:inner-2], b, r)
	fmt.Printf("%s│%s %s %s│%s\n", b, r, pad(""), b, r)
	fmt.Printf("%s│%s %s %s│%s\n", b, r, pad("Do you want to proceed?"), b, r)
	fmt.Printf("%s│ ❯ 1. Yes%s%s %s│%s\n", b, r, strings.Repeat(" ", inner-8), b, r)
	fmt.Printf("%s│%s %s %s│%s\n", b, r, pad("  2. Yes, and don't ask again this session"), b, r)
	fmt.Printf("%s│%s %s %s│%s\n", b, r, pad("  3. No, and tell me what to do (esc)"), b, r)
	fmt.Printf("%s╰%s╯%s\n", b, strings.Repeat("─", inner+2), r)
	line, err := a.in.ReadString('\n')
	if err != nil {
		os.Exit(0)
	}
	a.report(models.StatusRunning, "PostToolUse")
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "", "1", "y", "yes", "2":
		return true
	}
	return false
}

// exitWithParent ends the agent once the nagare that started the demo is gone,
// however it went — a kill -9 runs no cleanup. When every agent has exited, tmux
// has no sessions left and stops the demo server by itself, so an interrupted
// demo leaves nothing running.
func exitWithParent() {
	pid, err := strconv.Atoi(os.Getenv(parentEnv))
	if err != nil || pid <= 0 {
		return
	}
	for range time.Tick(time.Second) {
		if syscall.Kill(pid, 0) != nil {
			os.Exit(0)
		}
	}
}
