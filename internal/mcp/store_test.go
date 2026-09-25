package mcp

import (
	"os"
	"testing"
	"time"
)

func TestAllMessagesReportsQueuedAndWaiting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now().UTC().Format(time.RFC3339)
	for _, m := range []Message{
		{ID: "a", FromSession: "x", ToSession: "y", Status: StatusPending, CreatedAt: now},
		{ID: "b", FromSession: "y", ToSession: "x/pane", Status: StatusRead, CreatedAt: now},
	} {
		if err := WriteMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	Enqueue("%3", Push{ID: "a", Text: "t"})
	os.WriteFile(waitPath("y", "a"), []byte(time.Now().Add(time.Minute).UTC().Format(time.RFC3339)), 0644)
	os.WriteFile(waitPath("x/pane", "b"), []byte(time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)), 0644)

	envs, err := AllMessages()
	if err != nil || len(envs) != 2 {
		t.Fatalf("AllMessages = %v, %v", envs, err)
	}
	byID := map[string]Envelope{}
	for _, e := range envs {
		byID[e.ID] = e
	}
	if !byID["a"].Queued || byID["b"].Queued {
		t.Error("queued flag wrong")
	}
	// An expired marker belongs to a sender that is no longer waiting.
	if !byID["a"].Waiting || byID["b"].Waiting {
		t.Error("waiting flag wrong")
	}
	if byID["b"].Inbox != "x__pane" || byID["a"].Size == 0 {
		t.Errorf("envelope metadata = %+v", byID["b"])
	}
}

func TestDeleteMessageLeavesNothingBehind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	WriteMessage(Message{ID: "a", ToSession: "y", Status: StatusPending})
	Enqueue("%3", Push{ID: "a", Text: "t"})
	os.WriteFile(waitPath("y", "a"), []byte("x"), 0644)
	envs, _ := AllMessages()
	if err := DeleteMessage(envs[0]); err != nil {
		t.Fatal(err)
	}
	if Pending("%3") != 0 {
		t.Error("queued copy survived; the deleted message would still be delivered")
	}
	if _, err := os.Stat(InboxDir("y")); !os.IsNotExist(err) {
		t.Error("empty inbox directory left behind")
	}
}
