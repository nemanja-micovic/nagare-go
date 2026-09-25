package tmux

// Queue runs tmux commands one at a time, in the order they were issued.
//
// Keystrokes forwarded to a pane have to arrive in the order they were typed.
// Issuing each one as its own tea.Cmd would not guarantee that — commands run on
// goroutines of their own — and running them inline would stall the UI on every
// fork. A single worker gives both: the caller never blocks, and nothing is
// reordered. Snapshots go through the same worker, so a capture taken after a
// keystroke is guaranteed to have been taken after tmux received it.
type Queue struct {
	jobs chan job
	run  Runner
}

// Runner runs one tmux command and returns its output. The real one execs
// tmux; tests substitute one that records.
type Runner func(args ...string) (string, error)

// ExecRunner runs tmux for real.
func ExecRunner(args ...string) (string, error) {
	out, err := Command(args...).Output()
	return string(out), err
}

type job struct {
	target  string
	literal string   // text to type, when non-empty
	keys    []string // named keys for send-keys, when literal is empty
	fn      func()   // arbitrary work, when both are empty
}

// NewQueue starts a queue and its worker.
func NewQueue() *Queue {
	return NewQueueWith(ExecRunner)
}

// NewQueueWith starts a queue whose tmux commands are handed to run instead of
// executed — so tests can see exactly what would have been sent, and in what
// order, without a tmux server.
func NewQueueWith(run Runner) *Queue {
	q := &Queue{jobs: make(chan job, 512), run: run}
	go q.work()
	return q
}

// Type sends text to a pane literally, as if typed.
func (q *Queue) Type(target, text string) {
	if text != "" {
		q.jobs <- job{target: target, literal: text}
	}
}

// Keys sends named keys ("Enter", "C-c", "M-Up") to a pane.
func (q *Queue) Keys(target string, keys ...string) {
	if len(keys) > 0 {
		q.jobs <- job{target: target, keys: keys}
	}
}

// Paste pastes text into a pane through a tmux buffer. The pane gets it wrapped
// in bracketed-paste markers when the program in it asked for them, so a
// multi-line paste lands as one paste rather than as lines submitted one by one.
func (q *Queue) Paste(target, text string) {
	if text == "" {
		return
	}
	q.Do(func() {
		q.run("set-buffer", "-b", "nagare-paste", "--", Arg(text),
			";", "paste-buffer", "-p", "-d", "-b", "nagare-paste", "-t", target)
	})
}

// Fit sizes a pane's window to w×h (see FitWindow), after everything queued.
func (q *Queue) Fit(target string, w, h int, zoom bool) {
	q.Do(func() { fitWindow(q.run, target, w, h, zoom) })
}

// Release hands a fitted window back to tmux (see ReleaseWindow).
func (q *Queue) Release(target string, unzoom bool) {
	q.Do(func() { releaseWindow(q.run, target, unzoom) })
}

// Capture snapshots a pane on the worker — so after every keystroke queued
// before it — and blocks until the snapshot is taken.
func (q *Queue) Capture(target string, scroll, height int) (Screen, error) {
	type result struct {
		sc  Screen
		err error
	}
	reply := make(chan result, 1)
	q.Do(func() {
		sc, err := snapshot(q.run, target, scroll, height)
		reply <- result{sc, err}
	})
	r := <-reply
	return r.sc, r.err
}

// Run runs one tmux command on the worker and waits for its output.
func (q *Queue) Run(args ...string) (string, error) {
	type result struct {
		out string
		err error
	}
	reply := make(chan result, 1)
	q.Do(func() {
		out, err := q.run(args...)
		reply <- result{out, err}
	})
	r := <-reply
	return r.out, r.err
}

// Do runs fn on the worker, after everything queued before it.
func (q *Queue) Do(fn func()) {
	q.jobs <- job{fn: fn}
}

func (q *Queue) work() {
	var carry *job
	for {
		var j job
		if carry != nil {
			j, carry = *carry, nil
		} else {
			j = <-q.jobs
		}

		if j.literal != "" {
			// Typing outruns fork: coalesce whatever text has queued up behind
			// this for the same pane into one send-keys, so a burst of keys costs
			// one tmux call rather than one per character.
			text := j.literal
		drain:
			for {
				select {
				case next := <-q.jobs:
					if next.literal != "" && next.target == j.target {
						text += next.literal
						continue
					}
					carry = &next
					break drain
				default:
					break drain
				}
			}
			q.run("send-keys", "-l", "-t", j.target, "--", Arg(text))
			continue
		}
		if len(j.keys) > 0 {
			args := []string{"send-keys", "-t", j.target}
			for _, k := range j.keys {
				args = append(args, Arg(k))
			}
			q.run(args...)
			continue
		}
		if j.fn != nil {
			j.fn()
		}
	}
}
