package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/nemke/nagare-go/internal/config"
	"github.com/nemke/nagare-go/internal/hooks"
	"github.com/nemke/nagare-go/internal/log"
	"github.com/nemke/nagare-go/internal/mcp"
	"github.com/nemke/nagare-go/internal/newsession"
	"github.com/nemke/nagare-go/internal/notifs"
	"github.com/nemke/nagare-go/internal/picker"
	"github.com/nemke/nagare-go/internal/popup"
	"github.com/nemke/nagare-go/internal/session"
	"github.com/nemke/nagare-go/internal/setup"
	"github.com/nemke/nagare-go/internal/theme"
)

func main() {
	log.Init()
	defer log.Close()

	// Load theme from config
	cfg, _ := config.Load()
	theme.Set(cfg.Appearance.Theme)
	log.Info("starting nagare-go, theme=%s", cfg.Appearance.Theme)

	rootCmd := &cobra.Command{
		Use:   "nagare-go",
		Short: "tmux session manager for AI coding agents",
	}

	pickCmd := &cobra.Command{
		Use:   "pick",
		Short: "Launch session picker TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			for {
				m := picker.New()
				p := tea.NewProgram(m)
				result, err := p.Run()
				if err != nil {
					return err
				}

				pickerModel, ok := result.(picker.Model)
				if !ok {
					return nil
				}

				switch pickerModel.Result().Action {
				case picker.ActionNew:
					form := tea.NewProgram(newsession.New())
					if _, err := form.Run(); err != nil {
						return err
					}
					// Loop back to picker after form closes
					continue
				case picker.ActionQuickProto:
					form := tea.NewProgram(newsession.NewQuick())
					if _, err := form.Run(); err != nil {
						return err
					}
					continue
				default:
					return nil
				}
			}
		},
	}

	hookStateCmd := &cobra.Command{
		Use:   "hook-state",
		Short: "Handle agent hook events from stdin",
		Run: func(cmd *cobra.Command, args []string) {
			hooks.Handle()
		},
	}

	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "Install hooks, plugins, MCP server and slash commands for all agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setup.Run()
		},
	}

	newCmd := &cobra.Command{
		Use:   "new [path]",
		Short: "Create a new agent session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, _ := cmd.Flags().GetString("agent")
			name, _ := cmd.Flags().GetString("name")
			cont, _ := cmd.Flags().GetBool("continue")
			worktree, _ := cmd.Flags().GetString("worktree")

			if worktree != "" {
				if name != "" {
					return fmt.Errorf("--worktree and --name mean different names; pass only one")
				}
				if len(args) == 0 {
					return fmt.Errorf("--worktree needs a repository path: nagare-go new <repo> -w %s", worktree)
				}
				sessionName, err := session.CreateWorktree(args[0], worktree, agent)
				if err != nil {
					return err
				}
				session.SwitchToSession(sessionName)
				return nil
			}

			if len(args) == 0 {
				// No path: launch interactive form
				p := tea.NewProgram(newsession.New())
				_, err := p.Run()
				return err
			}

			// Direct creation
			sessionName, err := session.Create(args[0], name, agent, cont)
			if err != nil {
				return err
			}
			session.SwitchToSession(sessionName)
			return nil
		},
	}
	newCmd.Flags().StringP("agent", "a", "claude", "Agent type (claude, codex, opencode, gemini, crush, pi, omp)")
	newCmd.Flags().StringP("name", "n", "", "Session name (default: path basename)")
	newCmd.Flags().BoolP("continue", "c", true, "Continue previous session")
	newCmd.Flags().StringP("worktree", "w", "", "Create a named git worktree and start the agent in it")

	notifsCmd := &cobra.Command{
		Use:   "notifs",
		Short: "View notification history and settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			p := tea.NewProgram(notifs.New())
			_, err := p.Run()
			return err
		},
	}

	popupNotifCmd := &cobra.Command{
		Use:   "popup-notif",
		Short: "Show popup notification for a session",
		RunE: func(cmd *cobra.Command, args []string) error {
			session, _ := cmd.Flags().GetString("session")
			event, _ := cmd.Flags().GetString("event")
			message, _ := cmd.Flags().GetString("message")
			timeout, _ := cmd.Flags().GetInt("timeout")
			duration, _ := cmd.Flags().GetInt("duration")
			p := tea.NewProgram(popup.New(session, event, message, timeout, duration))
			_, err := p.Run()
			return err
		},
	}
	popupNotifCmd.Flags().String("session", "", "Session name")
	popupNotifCmd.Flags().String("event", "", "Event type (needs_input or task_complete)")
	popupNotifCmd.Flags().String("message", "", "Notification message")
	popupNotifCmd.Flags().Int("timeout", 10, "Auto-dismiss timeout in seconds")
	popupNotifCmd.Flags().Int("duration", 0, "Working seconds (for task_complete)")

	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run MCP server for inter-agent messaging",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcp.RunServer()
		},
	}

	// Bridge for agents without an MCP client (pi). Called by the nagare pi
	// extension, not by users.
	toolCmd := &cobra.Command{
		Use:          "tool <name> [json]",
		Short:        "Invoke a nagare messaging tool directly (for agents without MCP)",
		Args:         cobra.RangeArgs(1, 2),
		Hidden:       true,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var toolArgs []byte
			if len(args) == 2 {
				toolArgs = []byte(args[1])
			}
			out, err := mcp.RunTool(cmd.Context(), args[0], toolArgs)
			if err != nil {
				return err
			}
			cmd.Println(out)
			return nil
		},
	}

	// Detached helper started by message delivery: pastes queued messages into
	// a pane once its agent goes idle. Not for users.
	deliverWatchCmd := &cobra.Command{
		Use:    "deliver-watch <pane-id>",
		Short:  "Deliver queued messages to a pane once its agent is idle",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			mcp.WatchPane(args[0])
		},
	}

	rootCmd.AddCommand(pickCmd, hookStateCmd, setupCmd, notifsCmd, popupNotifCmd, newCmd, mcpCmd, toolCmd, deliverWatchCmd)

	// Default to "pick" when no subcommand given
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return pickCmd.RunE(pickCmd, args)
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
