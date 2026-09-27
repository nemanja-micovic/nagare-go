package tmux

import (
	"reflect"
	"sync"
	"testing"
)

// recorder collects the commands a Queue would have run.
type recorder struct {
	mu   sync.Mutex
	cmds [][]string
}

func (r *recorder) run(args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, args)
	return "", nil
}

func (r *recorder) all() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.cmds...)
}

// flush waits for everything queued so far to run.
func flush(q *Queue) {
	done := make(chan struct{})
	q.Do(func() { close(done) })
	<-done
}

// TestQueueKeepsOrder — keystrokes must reach the pane in the order typed,
// whatever mix of text and named keys they are.
func TestQueueKeepsOrder(t *testing.T) {
	r := &recorder{}
	q := NewQueueWith(r.run)
	block := make(chan struct{})
	// Hold the worker so everything below queues up behind it; that is the
	// situation coalescing exists for.
	q.Do(func() { <-block })
	q.Type("%1", "h")
	q.Type("%1", "i")
	q.Keys("%1", "Enter")
	q.Type("%1", "-x")
	q.Type("%2", "y")
	close(block)
	flush(q)

	want := [][]string{
		{"send-keys", "-l", "-t", "%1", "--", "hi"},
		{"send-keys", "-t", "%1", "Enter"},
		{"send-keys", "-l", "-t", "%1", "--", "-x"},
		{"send-keys", "-l", "-t", "%2", "--", "y"},
	}
	if got := r.all(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands =\n%q\nwant\n%q", got, want)
	}
}

func TestQueuePasteUsesBracketedBuffer(t *testing.T) {
	r := &recorder{}
	q := NewQueueWith(r.run)
	q.Paste("%3", "line one\nline two")
	flush(q)
	got := r.all()
	if len(got) != 1 {
		t.Fatalf("got %d commands, want 1", len(got))
	}
	want := []string{"set-buffer", "-b", "nagare-paste", "--", "line one\nline two",
		";", "paste-buffer", "-p", "-d", "-b", "nagare-paste", "-t", "%3"}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("paste = %q, want %q", got[0], want)
	}
}

func TestArgKeepsTrailingSemicolon(t *testing.T) {
	for in, want := range map[string]string{
		"plain": "plain",
		"ls;":   `ls\;`,
		";":     `\;`,
		`a\;`:   `a\\;`,
		"M-;":   `M-\;`,
		"a; b":  "a; b",
		"":      "",
	} {
		if got := Arg(in); got != want {
			t.Errorf("Arg(%q) = %q, want %q", in, got, want)
		}
	}
}
