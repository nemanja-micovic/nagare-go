package mcp

import "time"

// State is where a message is in its life, as the mailbox shows it. It is
// derived from the stored status plus facts the file does not hold — a queued
// push copy, the message's age — and lives here, beside the code that moves
// messages along, so tests can drive a delivery and check exactly what the
// user will see.
type State int

const (
	StateQueued     State = iota // waiting for a busy recipient
	StateLost                    // never delivered, and nothing left to deliver it
	StateNeedsReply              // delivered, and the sender asked for an answer
	StateDelivered               // delivered; no answer was asked for
	StateAnswered                // answered
)

// LostAfter is how long a message may sit undelivered with no queued copy
// before it counts as lost. A send stores the message a moment before it
// delivers it, so a fresh pending message is merely in transit.
const LostAfter = 2 * time.Minute

// State derives the message's state at now.
func (e Envelope) State(now time.Time) State {
	switch {
	case e.Status == StatusCompleted:
		return StateAnswered
	case e.ExpectsReply && (e.Status == StatusRead || e.Status == StatusDelivered):
		return StateNeedsReply
	case e.Status == StatusRead || e.Status == StatusDelivered:
		return StateDelivered
	case e.Queued || now.Sub(e.Sent()) < LostAfter:
		return StateQueued
	}
	return StateLost
}
