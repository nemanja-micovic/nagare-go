package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/nemke/nagare-go/internal/config"
	"github.com/nemke/nagare-go/internal/demo"
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
	// A demo agent is this binary started under an agent's name; it must not
	// parse nagare's own command line.
	if demo.IsAgent() {
		demo.RunAgent()
		return
	}

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
			return runPicker(cfg)
		},
	}

	var demoSpeed float64
	demoCmd := &cobra.Command{
		Use:   "demo",
		Short: "Try nagare with simulated agents (no real agents needed)",
		Long: "Starts a private tmux server with simulated Claude Code, Codex and OpenCode\n" +
			"agents working in throwaway git repositories, and opens nagare on them.\n" +
			"Nothing touches your real tmux sessions or nagare state; it is all removed on exit.",
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := demo.Start(demoSpeed)
			if err != nil {
				return err
			}
			defer env.Close()
			// A closed terminal sends SIGHUP, which would otherwise end the
			// process without running the deferred cleanup.
			hup := make(chan os.Signal, 1)
			signal.Notify(hup, syscall.SIGHUP)
			go func() {
				<-hup
				env.Close()
				os.Exit(1)
			}()
			return runPicker(cfg)
		},
	}
	demoCmd.Flags().Float64Var(&demoSpeed, "speed", 1, "pace of the simulated agents (2 = twice as fast)")

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

	rootCmd.AddCommand(pickCmd, demoCmd, hookStateCmd, setupCmd, notifsCmd, popupNotifCmd, newCmd, mcpCmd, toolCmd)

	// Default to "pick" when no subcommand given
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return pickCmd.RunE(pickCmd, args)
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runPicker runs the picker, and the forms it hands off to, until the user quits.
func runPicker(cfg config.NagareConfig) error {
	// In focus mode a session created from the picker opens inside it,
	// rather than the form switching the terminal away from nagare.
	stay := cfg.Picker.EnterAction != config.EnterJump
	focusNext := ""
	for {
		m := picker.New()
		if focusNext != "" {
			m = m.FocusWhenReady(focusNext)
			focusNext = ""
		}
		p := tea.NewProgram(m)
		result, err := p.Run()
		pickerModel, ok := result.(picker.Model)
		if ok {
			// Hand any window focus mode resized back to tmux.
			pickerModel.Close()
		}
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}

		switch pickerModel.Result().Action {
		case picker.ActionNew:
			form := newsession.New()
			if stay {
				form = form.Stay()
			}
			res, err := tea.NewProgram(form).Run()
			if err != nil {
				return err
			}
			if f, ok := res.(newsession.Model); ok && stay {
				focusNext = f.Created()
			}
			// Loop back to picker after form closes
			continue
		case picker.ActionQuickProto:
			form := newsession.NewQuick()
			if stay {
				form = form.Stay()
			}
			res, err := tea.NewProgram(form).Run()
			if err != nil {
				return err
			}
			if f, ok := res.(newsession.QuickModel); ok && stay {
				focusNext = f.Created()
			}
			continue
		default:
			return nil
		}
	}
}
