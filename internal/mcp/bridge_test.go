package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/nemke/nagare-go/internal/memory"
)

func TestToolNamesCoversEveryMCPTool(t *testing.T) {
	want := []string{"check_messages", "get_memory", "list_agents", "recall", "remember", "reply",
		"send_message", "send_message_and_wait", "update_memory"}
	got := ToolNames()
	if len(got) != len(want) {
		t.Fatalf("ToolNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ToolNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunToolUnknownName(t *testing.T) {
	_, err := RunTool(context.Background(), "nope", nil)
	if err == nil {
		t.Fatal("expected an error for an unknown tool")
	}
	// The error should tell the caller what is available.
	if !strings.Contains(err.Error(), "list_agents") {
		t.Errorf("error should list available tools, got %q", err)
	}
}

func TestRunToolInvalidJSON(t *testing.T) {
	_, err := RunTool(context.Background(), "send_message", []byte("{not json"))
	if err == nil {
		t.Fatal("expected an error for malformed arguments")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error should mention invalid JSON, got %q", err)
	}
}

// Tools with no required arguments must work with no arguments at all, since
// pi's extension passes "{}" and a shell caller may pass nothing.
func TestDecodeArgsEmptyIsNotAnError(t *testing.T) {
	var input SendMessageInput
	for _, args := range []string{"", "   ", "{}"} {
		if err := decodeArgs([]byte(args), &input); err != nil {
			t.Errorf("decodeArgs(%q) = %v, want nil", args, err)
		}
	}
}

func TestDecodeArgsPopulatesInput(t *testing.T) {
	var input SendMessageAndWaitInput
	if err := decodeArgs([]byte(`{"target":"api","message":"hi","timeout":5}`), &input); err != nil {
		t.Fatal(err)
	}
	if input.Target != "api" || input.Message != "hi" || input.Timeout != 5 {
		t.Errorf("decoded = %+v", input)
	}
}

func TestMemoryToolsThroughTheBridge(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	orig := memoryStore
	memoryStore = func() *memory.Store { return &memory.Store{Root: root} }
	defer func() { memoryStore = orig }()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	out, err := RunTool(context.Background(), "remember", []byte(`{"text":"Integration tests need docker compose up first","kind":"gotcha"}`))
	if err != nil || !strings.HasPrefix(out, "Saved as [m") {
		t.Fatalf("remember: %q %v", out, err)
	}
	id := out[len("Saved as [") : len("Saved as [")+6]
	out, _ = RunTool(context.Background(), "recall", []byte(`{"query":"integration tests docker"}`))
	if !strings.Contains(out, id) || !strings.Contains(out, "Integration tests need docker") {
		t.Errorf("recall: %q", out)
	}
	out, _ = RunTool(context.Background(), "get_memory", []byte(`{"ids":["`+id+`"]}`))
	if !strings.Contains(out, "docker compose up first") {
		t.Errorf("get_memory: %q", out)
	}
	out, _ = RunTool(context.Background(), "update_memory", []byte(`{"id":"`+id+`","status":"archived"}`))
	if !strings.Contains(out, "archived") {
		t.Errorf("update_memory: %q", out)
	}
	out, _ = RunTool(context.Background(), "recall", []byte(`{"query":"docker"}`))
	if !strings.HasPrefix(out, "No memories match") {
		t.Errorf("archived memory still recalled: %q", out)
	}
}
