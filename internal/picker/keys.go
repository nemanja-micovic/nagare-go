package picker

// Key binding strings matched against tea.KeyMsg.String()
const (
	keyUp            = "up"
	keyDown          = "down"
	keyEnter         = "enter"
	keyEscape        = "esc"
	keyApprove       = "ctrl+y"
	keyApproveAlways = "ctrl+a"
	keyToggleView    = "tab"
	keyCycleTheme    = "ctrl+t"
	keyHelp          = "f1"
	keyUnload        = "ctrl+w"
	keyKillSession   = "ctrl+x"
	keyStar          = "ctrl+f"
	keyCycleSort     = "ctrl+o"
	keyRename        = "f2"
	keyNewWorktree   = "f3"
	keyNewSession    = "ctrl+n"
	keyQuickProto    = "ctrl+r"
	keyInlinePrompt  = "ctrl+l"
	keyEditPrompt    = "ctrl+g"
	keyEditConfig    = "ctrl+e"
	keyToggleSaved   = "ctrl+s"
	keyNextAttention = "f4"
	// F5, not Ctrl+b: Ctrl+b is tmux's default prefix, and the picker runs
	// inside tmux, so the key would never reach it.
	keyMailbox     = "f5"
	keyMailCleanup = "ctrl+d" // mailbox only: delete old messages
)
