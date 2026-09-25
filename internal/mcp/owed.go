package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nemke/nagare-go/internal/fsutil"
	"github.com/nemke/nagare-go/internal/models"
)

// Automatic replies.
//
// Answering with the reply tool costs the recipient a whole extra model step:
// it writes its answer as a tool call, reads the result, then writes a closing
// message nobody needed. But every agent nagare supports reports the text of
// its final message when a turn ends — Claude Code and Codex in the Stop hook,
// pi in agent_end, OpenCode through its SDK — and for a turn that was about a
// message, that text *is* the answer.
//
// So a message that expects a reply is recorded as owed by the pane it was
// delivered to, and when that pane's turn ends, its final message is sent
// back as the reply. The recipient just answers, as it would answer a person.
// An explicit reply() still works and settles the debt first; a prompt the
// user types in between cancels it, since the next answer is for the user.

// owedTTL bounds how long a debt is held. A turn that never ends cleanly — an
// interrupt, a crash — must not have some unrelated answer hours later sent
// as its reply.
const owedTTL = 30 * time.Minute

// autoReplyAgents are the agents whose integration reports the final message
// of a turn, so the answer can be sent back without a tool call.
var autoReplyAgents = map[models.AgentType]bool{
	models.AgentClaude:   true,
	models.AgentCodex:    true,
	models.AgentPi:       true,
	models.AgentOpenCode: true,
	models.AgentOhMyPi:   true,
}

// AutoReplies reports whether an agent's final message is sent back as its
// reply.
func AutoReplies(agent models.AgentType) bool {
	return autoReplyAgents[agent]
}

type owedReply struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

func owedPath(paneID string) string {
	return filepath.Join(dataDir(), "owed", paneKey(paneID)+".json")
}

// withOwed runs fn on a pane's debts under an exclusive lock, and saves what
// fn returns. Hooks and deliveries run as separate processes and can land at
// the same moment.
func withOwed(paneID string, fn func([]owedReply) []owedReply) {
	if paneID == "" {
		return
	}
	path := owedPath(paneID)
	withFileLock(path+".lock", func() error {
		var list []owedReply
		if data, err := os.ReadFile(path); err == nil {
			json.Unmarshal(data, &list)
		}
		list = fn(list)
		if len(list) == 0 {
			os.Remove(path)
			return nil
		}
		data, err := json.Marshal(list)
		if err != nil {
			return err
		}
		return fsutil.AtomicWrite(path, data, 0644)
	})
}

// Owe records that a pane owes replies to these messages.
func Owe(paneID string, ids ...string) {
	if len(ids) == 0 {
		return
	}
	now := time.Now()
	withOwed(paneID, func(list []owedReply) []owedReply {
		for _, id := range ids {
			list = append(list, owedReply{ID: id, At: now})
		}
		return list
	})
}

// ForgetOwed drops a pane's debts: the user has spoken to the agent, so its
// next answer is for the user, not for whoever messaged it.
func ForgetOwed(paneID string) {
	if _, err := os.Stat(owedPath(paneID)); err != nil {
		return // the common case, and no lock needed to see it
	}
	withOwed(paneID, func([]owedReply) []owedReply { return nil })
}

// SettleOwed sends content — the final message of the pane's turn — as the
// reply to every message the pane still owes one. It returns how many were
// answered. It is cheap when nothing is owed, which is nearly always, since
// hooks call it at the end of every turn.
func SettleOwed(paneID, content string) int {
	content = strings.TrimSpace(content)
	if content == "" {
		return 0
	}
	if _, err := os.Stat(owedPath(paneID)); err != nil {
		return 0
	}
	var due []owedReply
	withOwed(paneID, func(list []owedReply) []owedReply {
		due = list
		return nil
	})

	answered := 0
	for _, o := range due {
		if time.Since(o.At) > owedTTL {
			continue
		}
		msg, err := FindMessage(o.ID)
		if err != nil || msg.Status == StatusCompleted {
			continue // answered with reply(), or deleted
		}
		// The replier is named as the message addressed it, which is how the
		// sender knows it; the pane is the one the turn ran in.
		answer(msg, content, msg.ToSession, paneID, true)
		answered++
	}
	return answered
}

// IsNagarePrompt reports whether a prompt is a message nagare delivered,
// rather than something the user typed. It looks anywhere in the prompt, not
// only at its start: an agent may wrap a delivered message — Claude Code
// frames one arriving on its inbox socket — and a false "the user spoke"
// would throw away the reply the message was owed.
func IsNagarePrompt(prompt string) bool {
	return strings.Contains(prompt, pushPrefix+" Message from") ||
		strings.Contains(prompt, pushPrefix+" Reply from")
}
