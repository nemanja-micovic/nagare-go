Do not mention any AI agent (Claude, Gemini, Crush, OpenCode, etc.) in commit messages.
Do not commit without explicit user permission.

# nagare-go

Go rewrite of [nagare](../nagare) — tmux session manager for AI coding agents.

## Build & Test

```bash
./compile.bash             # build (stripped binary)
go build -o nagare-go .    # build (debug)
go test ./... -v           # run all tests
go vet ./...               # lint
```

## Commands

```bash
nagare-go                  # launch picker (default)
nagare-go pick             # launch picker
nagare-go hook-state       # handle agent hook/plugin/extension events (stdin JSON)
nagare-go setup            # install status reporting + MCP server + slash commands
nagare-go notifs           # notification center TUI
nagare-go popup-notif      # popup notification (called by hooks)
nagare-go new [path]       # create new agent session
nagare-go new <repo> -w <name>  # create a named git worktree and start an agent in it
nagare-go mcp              # run MCP server (stdio, for agent CLIs)
nagare-go tool <name> [json]  # invoke a messaging tool directly (hidden; for pi)
nagare-go deliver-watch <pane> # paste queued messages once the pane is idle (hidden; spawned by delivery)
```

## Architecture

Single binary with cobra subcommands. All code in `internal/` packages.

- `internal/models` — Session, SessionStatus, AgentType (claude, codex, opencode, gemini, crush, pi)
- `internal/config` — TOML config loading + saving
- `internal/tmux` — scanner (list-panes + /proc descendant walk), per-pane paths and worktree resolution, status detection (pane scraping)
- `internal/git` — resolves a directory into branch, repo name, and worktree name (one `rev-parse` per path)
- `internal/state` — state files + session registry
- `internal/hooks` — hook handler (stdin JSON → state files → notifications)
- `internal/notifications` — delivery (toast/bell/os/popup) + persistent store
- `internal/picker` — Bubble Tea TUI (list/grid views, overlays, keybindings, mouse)
- `internal/notifs` — notification center TUI
- `internal/popup` — popup notification TUI
- `internal/session` — session creation + path resolution
- `internal/newsession` — new session + quick prototype forms
- `internal/theme` — 13 themes on a derived design-token layer (see Themes), self-registering via init()
- `internal/setup` — status reporting + MCP + slash command installation for every agent
- `internal/mcp` — MCP server for inter-agent messaging, plus the CLI tool bridge
- `internal/bin` — shared binary finder
- `internal/fsutil` — atomic file writes
- `internal/log` — file logger (~/.local/share/nagare/nagare-go.log)

### Worktrees

Each agent pane resolves its own directory from `pane_current_path`, not the tmux
session path, so panes in different worktrees of one repo get their own path, branch,
and name (`{session}/{worktree}`). Worktree detection is structural — the git common
dir's parent differs from the toplevel — so hand-made worktrees work the same as
Claude Code's `.claude/worktrees/`.

Display-name precedence is: a window name the user set, then the worktree, then
`{agent}_NN`.

Worktree sessions are created by `session.CreateWorktree`. Claude Code has its own
`-w <name>` flag, so it is handed the flag and creates the worktree under
`.claude/worktrees/`; every other agent has no equivalent, so nagare runs
`git worktree add` into `.worktrees/` and opens the pane there. `--tmux` is never
passed to Claude — nagare already made the window.

A worktree pane always joins its repo's existing tmux session as a new window, which is
what lets the picker group them. Sessions are matched to a repo through
`git.MainRoot`, not by literal path, because a session's own directory is often a
worktree.

Because worktrees are windows in one session, Ctrl+x kills the *window* whenever a
session holds more than one agent pane — killing the session would take a repo's other
worktrees with it. On a worktree pane it then offers removal; `git.RemoveWorktree` never
passes `--force`, so git refuses while there is uncommitted work, and the branch is left
intact.

Worktree creation runs as a `tea.Cmd`, never inline: doing it in the update loop froze
the TUI for seconds. A spinner shows until the new agent pane appears in a scan
(`pendingWorktree.satisfiedBy`) rather than until nagare's own work returns, because
`claude -w` needs seconds more to build the worktree and start. Failures surface in the
status line instead of only reaching the log.

Removal is confirmed by a centred dialog (`renderConfirmOverlay`) drawn with
`placeOverlay`, like the help and theme overlays; only `y`/`n`/`esc` answer it. Claude
locks the worktrees it creates, so `git.RemoveWorktree` checks cleanliness itself,
unlocks, then removes without `--force` — passing `--force` would override the dirty
guard too.

The detail pane shows outstanding work (`git.WorkStatus`) and warns when two agents
share one directory. Work is cached per path and refreshed per scan: the pane
re-renders every frame for the status-dot pulse, so git must never be called from
render.

List rows show a colored one-cell agent sigil (`C` for Claude Code, `X` for Codex,
etc.) beside the name and the git branch in the right-hand column. Keeping both visible
matters when one tmux session mixes agents whose generated pane names look similar.
`branchFor` suppresses a branch that only repeats the row — Claude names a worktree's
branch `worktree-<name>` — checking the worktree name as well as the label, since a lone
pane in a worktree is labelled with its full `{session}/{worktree}` name.
`splitRowWidth` divides the row: the label takes what it needs but never squeezes the
branch below `minBranchWidth`.

In the list view, sessions sharing a tmux session name are grouped under a single
header row naming the repo, and children show only their own name — so the repo
prefix is not repeated on every row. Lone sessions also get a header and one indented
agent child, keeping the tree and agent sigils aligned across the list. One blank row
separates project blocks. A group takes the position of its most urgent member, so a
waiting worktree lifts its whole repo. Rows are derived per frame by
`picker.buildRows`; the cursor keeps indexing sessions, not rows. Grid view stays flat.

## Agent Integrations

Every agent reports status through one interface: `nagare-go hook-state` reading a JSON
object on stdin with `hook_event_name`, `session_id`, `cwd`, and optionally
`last_assistant_message` / `notification_type`, plus `TMUX_PANE` from the environment.
Agent-native event names are mapped to states centrally in `hooks.EventToState` — never
per-agent elsewhere.

| Agent | Status reporting | Messaging tools |
|-------|------------------|-----------------|
| Claude Code | hooks in `~/.claude/settings.json` | MCP (`~/.claude.json`) |
| Codex | hooks in `~/.codex/hooks.json` | MCP (`~/.codex/config.toml`) |
| Gemini CLI | hooks in `~/.gemini/settings.json` | MCP (`~/.gemini/settings.json`) |
| OpenCode | plugin `~/.config/opencode/plugins/nagare.js` | MCP (`~/.config/opencode/opencode.json`) |
| Crush | none | MCP (`~/.config/crush/crush.json`) |
| pi | extension `~/.pi/agent/extensions/nagare.ts` | `nagare-go tool` bridge (pi has no MCP) |

pi has no MCP client by design, so its extension registers the five nagare tools and
shells out to `nagare-go tool <name> <json>`, which calls the same handlers the MCP
server calls. pi also has no permission prompts, so pi sessions never reach
`waiting_input`.

Generated plugin/extension files are rewritten on every `nagare-go setup`, so edits
belong in `internal/setup`, not in the installed files.

Codex requires newly installed command hooks to be reviewed once with `/hooks`.
Nagare also installs a Codex Agent Skill at `~/.codex/skills/nagare/SKILL.md`.

### Message delivery: push, never poll

Messaging used to work like email: the sender called `list_agents`, then `send_message`,
which refused unless the target was idle and typed "call check_messages()" into its pane;
the recipient then spent a whole turn fetching the message, and a waiting sender polled
every 2s. Each of those steps is a model round trip. Now a message reaches the
recipient's context in one step, and so does the answer:

- **No discovery call.** `resolveSession` accepts a session, repo, worktree, directory,
  or agent type (`codex`), tiered strictest-first. A miss returns the live roster, so a
  wrong guess costs one retry, not a `list_agents` call. The MCP server `instructions`
  carry a startup roster and tell the agent not to call `list_agents` first.
- **The content is delivered, not a pointer to it.** `mcp.deliver` picks a path per
  message. If a live in-process listener owns the pane (the pi extension, the OpenCode
  plugin), it goes to `push/<pane>/` and the listener injects it natively. If the pane
  is idle, it is bracketed-pasted in as the next prompt. Otherwise it is queued in
  `push/<pane>/`. A pane showing a permission prompt is never typed into, because the
  keystrokes would answer the prompt.
- **Busy agents take messages mid-turn.** `hook-state` drains `push/$TMUX_PANE` on
  `PostToolUse` and `UserPromptSubmit`, returning it as `additionalContext`. On `Stop` it
  returns `{"decision":"block","reason":…}`, which both Claude Code and Codex continue
  from — so a Stop that delivers keeps the state `working`. `PreToolUse` cannot carry
  context, so it never drains.
- **pi** gets messages through `pi.sendUserMessage`, with `deliverAs: "steer"` while
  it is busy. **OpenCode** gets them through `client.session.promptAsync` on the
  top-level session; subagent sessions (those with a `parentID`) are skipped, and the
  TUI prompt is the fallback.
- **Fallback.** A detached `nagare-go deliver-watch <pane>` pastes whatever is still
  queued once the pane has been idle for two checks. This covers agents without such
  hooks (Gemini, OhMyPi, Crush) and status that went stale. It runs at most once per
  pane (pid lock) and exits when the queue is empty.
- **Replies are pushed back** as a new message carrying `in_reply_to`, which can itself
  be replied to, so two agents can hold a conversation without either polling. A sender
  blocked in `send_message_and_wait` leaves a `msg_<id>.wait` marker. The answer is
  then collected by the waiter, polling every 100ms, instead of being pushed.

A push is claimed by `rename`, so exactly one consumer delivers it however many race.
Replies look messages up by id across all inboxes (`FindMessage`), because a display
name — and so the inbox it maps to — changes when tmux renames a window. Codex filters
MCP server environments, so setup forwards `TMUX_PANE`/`TMUX` with `env_vars`. It also
raises `tool_timeout_sec`, because Codex's 60s default would cut waits short.

## Picker Keybindings

| Key | Action |
|-----|--------|
| Type | Fuzzy search sessions |
| Enter | Jump to selected session |
| Esc | Quit |
| ↑/↓ | Navigate |
| Tab | Toggle list/grid view |
| Ctrl+y | Approve permission |
| Ctrl+a | Approve always |
| Ctrl+f | Toggle star |
| Ctrl+o | Cycle sort mode |
| Ctrl+w | Unload agent pane |
| Ctrl+x | Kill the pane's window (or the session if it is the only agent pane); offers to remove a worktree |
| F2 | Name the selected task |
| F3 | New git worktree for this repo |
| Ctrl+n | New session form |
| Ctrl+r | Quick prototype |
| Ctrl+l | Inline prompt |
| Ctrl+g | Editor prompt ($EDITOR) |
| Ctrl+e | Edit config |
| Ctrl+t | Theme picker |
| Ctrl+b | Mailbox viewer |
| F4 | Jump to the next session waiting on you |
| F1 | Help overlay |

F4 walks the queue: forward from the cursor and wrapping, so repeated presses reach
every waiting session once before repeating. Most-urgent-first would ping-pong
between the same two. Only `waiting_input` counts — offering to jump to a running
agent would train the reflex to interrupt work in progress. It reports "nothing is
waiting" rather than doing nothing silently, and the footer names the queue size,
because three waiting is a different situation from one.

Mouse (`picker.mouse`, default on): click a session to select it, click it again to
jump, wheel to move the selection, click outside the help or theme overlay to close
it. A single click never jumps, so a stray click cannot abandon the picker; modal
dialogs (worktree removal, inline prompt) ignore clicks and want a deliberate answer.

The footer shows only the keys valid for the current mode and selection, trimmed to
one line — the full set is on F1. `hintsFor` lists hints in drop order, so whatever
matters most for the selection survives on a narrow terminal.

### Layout: measure, never assume

Every layout bug found so far has been the same bug — a rendered height or width
derived by arithmetic or by counting string lines, instead of measured with
`lipgloss.Height` / `lipgloss.Width` after wrapping. It is worth stating as a rule
because the failures are invisible: each of the four TUIs clamps its assembled
frame as a safety net, so an over-budget layout does not smear the screen, it
silently loses its bottom row — which is where hint bars and panel borders live.

Two habits follow from it:

1. **Wrap, then measure.** `lipgloss.Height(style.Width(w).Render(s))`, not
   `strings.Count(s, "\n") + 1`. A single string line occupies several rows once
   it is too long for its panel.
2. **Window by rows, not by items.** A list entry may be two rows, or three when
   it wraps. Treating a row budget as an item count over-renders by whatever the
   average entry height is.

Found and fixed by an audit of all four TUIs:

| Where | Assumed | Symptom |
|---|---|---|
| `picker` list header | header is 4 rows | stats line wraps; last rows pushed through the bottom border |
| `picker` help overlay | box is `height*2/3` | ~44 rows of content silently clipped; also pinned the entry animation to y=0 |
| `picker` grid card | header is 2 rows | card one row too tall; **bottom border clipped off** |
| `picker` grid rows | clamp height, keep row count | grid taller than the frame; lower cards cut off |
| `picker` grid card width | `Padding(1)` is 4 cells | separator two cells short |
| `picker` detail panel | `strings.Count("\n")` | narrow panel loses its bottom border |
| `popup` | `height - 7`, `width - 4` | hint bar clipped below ~70 cols; separators short |
| `popup` hint bar | padding floored at 1 | bar wider than the popup, wrapped, then clipped |
| `notifs` list | row budget as item count | 92 rows into a 50-row frame; hint bar never visible |
| `notifs` settings | `height` ignored entirely | overflowed any terminal under ~19 rows |
| `notifs` hint bar | trimmed from the end | dropped "Esc Quit", leaving no visible way out |
| `newsession`, `quickproto` | no frame clamp at all | huh lays out at ~98 cells; every row wrapped on narrower terminals |

A hint bar's exit key is reserved space and never trimmed — the picker footer keeps
`F1 More · Esc Quit`, the notification centre keeps `Esc Quit`. Trimming from the
end takes the way out with it.

Layout tests assert on the **unclamped** frame (`m.view()`, not `m.View()`), since
the clamp is what hides the overflow. `internal/popup/layout_test.go`,
`internal/notifs/layout_test.go` and the box-integrity tests in
`internal/picker/grid_test.go` all check exact frame size across a spread of
terminal sizes, plus that hint bars survive and boxes are closed.

### Activity sparklines

The picker showed what every agent was doing *now* and nothing about what it had
been doing — and those are different questions. An agent grinding for ten minutes
and one that woke up four seconds ago are identical in a list of status dots, and
which is which changes what you do about it.

Each scan appends one sample per session to `Model.history`, so the trace costs
nothing beyond the polling already happening. `internal/picker/spark.go` renders it
as braille: a cell is 2x4 dots, so two samples wide and four levels tall per
character. Nothing else in Unicode is that dense, and unlike the image protocols it
needs no capability negotiation and no tmux passthrough — it is just text.

Status maps to bar height with waiting *above* running, so the tallest bars are the
moments the user was needed, and each cell takes the louder of its two samples: one
moment of waiting inside a long run of work is the thing worth seeing, not something
to average away. Colour repeats the status colours, so the trace reads without a
legend. Shown in the detail pane as a `Recent` row and pinned right of each grid
card's header.

History is trimmed to `sparkSamples` and sessions that disappear are pruned, since
the picker runs for hours.

### Grid cards

Card height arithmetic must be *measured*, never assumed. Three bugs came from
assuming it:

- `previewHeight = cellHeight - 7` assumed the header block was exactly two rows.
  A long path plus a long branch wraps the meta line to a third, making the card a
  row taller than its cell — and `fitBox`'s `MaxHeight` then clipped that row,
  taking the bottom border with it. The card rendered with no bottom edge.
- `cellHeight` was clamped *up* to a minimum without reducing how many rows were
  drawn, so on a short terminal the grid was taller than the frame and the lower
  cards were silently cut off by the clamp in `View`. It now shows fewer rows and
  scrolls to keep the selected card visible, like the list view.
- `innerWidth = cellWidth - 6` over-subtracted: `Padding(1)` is one cell per side,
  so two, not four. The separator fell two cells short of its card.

A card's header is budgeted against the **text column** beside the agent art, not
the full card width. Sizing it against the card and then rendering it into the
narrower column is what wrapped the header — a latent bug that only surfaced once
the sparkline made the header wide enough to hit it. `gridModel` in the tests seeds
activity history for exactly that reason, so the 50-case card test covers the path.

The header is kept to one row by truncating the name, as list rows do; the meta
line may wrap and the preview budget is derived from its measured height. If even
one row of preview will not fit, the header block is clamped by *rendered rows* —
clamping string lines does not work, because one line can occupy several rows.

`TestGridCardsAreClosedBoxes` checks all four corners of every card across five
terminal sizes, five session counts and both content shapes; the frame-size test
covers the arithmetic feeding through to the whole frame.

### Animation

Four animations, on two clocks — one transient at 30fps for motion, one slow at
10fps for anything continuous.

**Overlay entry** rises into place on a harmonica spring (`internal/picker/anim.go`),
~233ms over 8 frames at 30fps, transient. Off with `picker.animations = false`. The
spring stops as soon as its offset *rounds* to zero — a terminal has no sub-cell
vertical positioning, so stepping past that point re-renders identical frames; the
first tuning wasted 6 such frames out of 18.

**Status-dot breath** (`breathStep`/`breathFactor`) walks a running or waiting dot's
colour toward the surface behind it and back, over 2.2s at 10fps on a cosine-eased
curve. It replaced a `Faint` toggle flipping once a second, which read as a blink:
two states, and an instant transition between them.

**Row flash** (`internal/picker/flash.go`) tints a row for 900ms when a session
starts waiting (toward Warning) or finishes working (toward Success), fading on a
front-loaded curve so it arrives bright and lets go gently. It fires on exactly the
two transitions the notification layer fires on, and never on first sight of a
session — otherwise every waiting agent flashes the moment the picker opens. Grid
cards flash their *border* rather than their fill: a card is large enough that
tinting all of it would shout, and its border already carries focus.

**Selection slide** (`selectionSlide` in `anim.go`) crossfades the row tint between
the row the cursor left and the one it arrived at — 130ms, four frames on the
transient clock, the two tints always summing to a whole so the highlight neither
dims nor doubles mid-move. It is the only animation that fires on ordinary use, and
that is the point: an overlay spring is invisible to anyone who never opens an
overlay. It is skipped when the list length changed (a refilter renumbers rows, so
the previous index no longer refers to the same session), in grid view (selection
there is a card border, which does not crossfade legibly), and while an overlay has
the screen.

**Nothing about a grid card animates.** Three attempts were made and all three were
rejected on looks: a staggered arrival when the grid is first shown, a dimming border
trail on the card being left behind, and a border flash when a card's agent changed
state. A card is a large object, and moving or lighting one pulls the eye to the card
rather than to what is written on it — a row is thin enough that motion reads as a
hint, a card is not. Do not re-add any of them.

The status dot inside a card still breathes; that is the only moving part in grid
view. `TestGridCardsDoNotAnimate` holds the breath phase still and asserts the frame
is byte-identical over three seconds of animation ticks, with a slide and flashes
forced on. It starts the slide directly rather than through a key press, because
`startSlide` refuses in grid view and going through it left the render side untested
— the first version of the test passed with a card trail re-added.

**Colour is the only thing a terminal can fade.** There is no opacity, so an effect
that would dissolve in a GUI has to walk its colour toward the background instead —
which is why `theme.Mix` is exported and why both the breath and the flash are
colour interpolations rather than character or position tricks.

**Slow animation is cheap; fast animation is not.** A 2.2s breath needs nothing like
30 samples to look continuous, because the colour moves so little between frames. At
10fps a 30-session frame is ~3% of a core, so the breath and the flash share one
10fps clock — and it stops itself entirely when no session is breathing and no row
is fading, so a settled list costs nothing. The scan handler restarts it, since a
clock that stops has to be woken by whatever makes it relevant again. That balance
is the whole reason the earlier "no always-on animation" conclusion was wrong: the
constraint was never the frame cost alone, it was frame cost times sample rate.

The animation clock only runs while something is moving, and that is not
negotiable: a frame costs **1.8–5.4 ms** to assemble (measured — 1.8ms at 8
sessions/120x30, 4.3ms at 30 sessions/200x50, 5.4ms in grid view), so a continuous
30fps clock would burn 5–15% of a core for as long as the picker is open. Eight
transient frames cost ~32ms. This is why the status-dot pulse stays a 1Hz Faint
toggle instead of breathing smoothly, and why any future always-on animation needs
the render path made cheaper first.

Those figures are after one optimisation pass; `internal/picker/bench_test.go`
keeps them measurable. What that pass did, and what is left:

- `fadingRule` wrote one `Style.Render` per cell. Render re-measures its input with
  full grapheme segmentation, which for a one-cell string is pure overhead, and a
  rule can be 200 cells wide several times a frame. It now emits SGR directly via
  `fgSeq`. Help overlay: 1.89ms → 1.30ms.
- `renderNameWithMatches` rendered per *rune*. It now batches into runs, since a
  fuzzy match is a handful of runs, not a rune-length list. Filtered list: 4.97ms →
  3.84ms.
- Row segments were each wrapped in a second `Render` purely to carry the row
  background — nine calls a row. Each segment now sets its own background, and the
  row is plain concatenation.
- The whole-frame `MaxWidth`/`MaxHeight` clamp in `View` was 18% of a frame by
  itself. Only the height half survives (`clampHeight`, which needs no
  measurement); width is established where content is built and asserted by
  `TestFrameIsExactlyTerminalSized`.

Net: **4.69ms → 3.39ms** at 30 sessions on 200x50, 6.60ms → 4.71ms in grid view,
4.97ms → 2.98ms filtered. Still not enough for an always-on 30fps clock at 30
sessions (10% of a core). The remaining cost is structural: `Style.Render` is 60%
of a frame and `stringWidth` 41%, nearly all of it the per-panel `fitBox` Render
measuring an entire panel to pin it. Going further means composing panels manually
— padding each line to width and drawing borders ourselves — which is a much larger
and riskier change than any of the above.

Verify a rendering change did not alter output before trusting a speedup:
`frameFingerprint` in `render_test.go` hashes the *cell attributes* of a frame
(character plus every colour and style in effect), so it is insensitive to how the
escape sequences are arranged but catches any visible difference. Both of the first
two optimisations above were confirmed cell-identical that way; the third was not,
and the difference turned out to be a latent bug it exposed — the selected row's
tint broke either side of the name, because the name and branch were rendered
without the row background and `onPlane` then filled the gap with the panel surface
instead. Fixed by giving every segment the row background explicitly.

Overlay entry is started centrally, by `Update` noticing that no overlay was open
before a keypress and one is open after, so a new overlay cannot forget to animate.
`overlayRect` and `placeOverlay` both take the animated offset, so a click lands on
the dialog where it currently appears rather than where it will rest.

### Help overlay

Two columns, sized to its content and capped to the frame, not to a fraction of the
terminal. The single-column version ran to ~44 rows: it overflowed the box on any
shorter terminal and was silently clipped, and because an oversized dialog's
centered position clamps to y=0 it also defeated the entry animation.
`TestHelpOverlayCoversEveryBinding` checks every constant in `keys.go` is
documented, since the footer defers to this screen.

## State Files

Compatible with Python version. Same paths, same JSON schema:
- `~/.local/share/nagare/states/*.json`
- `~/.local/share/nagare/sessions.json`
- `~/.local/share/nagare/notifications.json`
- `~/.local/share/nagare/messages/` (MCP inter-agent)
- `~/.local/share/nagare/nagare-go.log`
- `~/.config/nagare/config.toml`

## Themes

13 themes with light/dark support: tokyonight (default), aura, catppuccin, dracula,
flexoki, gruvbox, kanagawa, monokai, nord, onedark, onedarkpro, rosepine, vesper.

Styles are functions (not cached) — theme switches take effect immediately.

### Design tokens

`theme.Colors` is a token layer: every color is named for the *role* it plays, and
nothing outside `internal/theme` reaches for a raw hex value. Four groups —
surfaces, text, accents, status — documented on the struct.

A theme file declares only the palette it has an opinion about. `Register` runs
`normalize`, which derives the rest, so a new theme is four lines of hex rather than
fourteen and all 13 get the same sense of depth. An explicitly set token is never
overwritten.

Colors are `theme.Pair{Dark, Light}` rather than `compat.AdaptiveColor` so that
derivation can run *per mode*: elevating a surface means something different on a
`#1a1b26` canvas than on a `#d5d6db` one. `Pair` still satisfies `color.Color`, so
lipgloss consumes it directly.

Elevation steps HCL *lightness* rather than blending toward white, which is what
keeps a lifted tokyonight panel blue-grey instead of drifting to grey. The derived
values land within a few hex of the upstream palettes they imitate.

### Depth planes

Three planes, back to front, and choosing the wrong one is visible — a fill left on
the canvas plane inside a panel punches a hole straight through it:

| Token | Used for |
|-------|----------|
| `Background` | the canvas: the help bar and the gaps between grid cards |
| `Surface` | **every** panel and everything inside one, so a panel reads as one lifted slab |
| `Overlay` | dialogs, plus `Shadow` for what they cast |

**Every panel gets `Surface` — including the preview.** Two other planes were tried
for the preview well and both were wrong: a sunken `Recessed` tier to sell "a window
onto another terminal", then the canvas plane to put foreign ANSI back on the ground
the agent drew it against. Each argument is defensible in isolation and each looked
like a bug, because the preview sat directly below the detail panel in a different
color for a reason the eye cannot infer. Panels are panels. `Recessed` was removed
from `theme.Colors` rather than left unused.

Grid cards are uniform for the same reason: header, meta and preview all on the card
surface.

**Content entering a panel must be wrapped in `onPlane(content, plane)`.** A style
that sets only a foreground ends its run with a full SGR reset, which clears the
background for the rest of that line, and captured pane output is foreign ANSI that
resets whenever it likes. Both leave cells on the terminal's own background. That was
invisible while panels shared the terminal's background and became visible holes the
moment panels were lifted onto their own plane. Wrapping content in an outer
`Background` style is the bug, not the cure — the background has to be re-established
after each reset, which is what `onPlane` does. It keeps any background the content
brought with it, so a preview does not lose the agent's own highlighting.

Grid view is the exception worth remembering: it composes straight onto the canvas —
the cards *are* the panels — so its search bar and bottom padding must fill their own
rows, and `onPlane` must not be run over card rows or it would inject the canvas
inside them. `TestNoDefaultBackgroundCells` walks every cell of every view and fails
on any left on the default background.

Focus is carried by a gradient: `primaryPanelStyle` and the selected grid card use
`BorderForegroundBlend(GradientFrom, GradientTo)`, which sweeps the blend around the
border perimeter. Dialogs take the same gradient, which is what ties an overlay to
the panel it was summoned from.

Overlays are composited with lipgloss v2 `Layer`/`Compositor` (`placeOverlay`), not
by splicing strings: layers carry real Z-order — ground, backdrop, shadow, dialog —
and the same layer set can answer a mouse hit test later. `placeOverlay` clamps to
the width and height it was passed; the shadow clamps to whatever room is left, so a
dialog taller than the frame still casts to the side.

Not adopted, deliberately: styled (curly) underlines. lipgloss v2 emits a separate
SGR run per grapheme once one is set, which turned a 23-character status error into
958 bytes.

## Conventions

- Follow Effective Go (go.dev/doc/effective_go)
- Use `gofmt`
- Tests colocated: `foo_test.go` next to `foo.go`
- No underscores in names — MixedCaps for exported, mixedCaps for unexported
- Always check errors
- Atomic writes for shared state files (write-to-temp-then-rename)
- Tokyonight color palette: idle=#00D26A, running=#e0af68, waiting=#db4b4b, dead=#565f89
