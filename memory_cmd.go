package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nemke/nagare-go/internal/memory"
)

// memoryEntry is a memory as the Neovim plugin reads it (`--json`).
type memoryEntry struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Scope   string   `json:"scope"`
	Status  string   `json:"status"`
	Title   string   `json:"title"`
	Tags    []string `json:"tags,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	Author  string   `json:"author,omitempty"`
	Updated string   `json:"updated,omitempty"`
	Uses    int      `json:"uses,omitempty"`
	Pinned  bool     `json:"pinned,omitempty"`
	File    string   `json:"file"`
	Score   float64  `json:"score,omitempty"`
}

func entry(m memory.Memory, score float64) memoryEntry {
	e := memoryEntry{ID: m.ID, Kind: m.Kind, Scope: m.Scope, Status: m.Status, Title: m.Title(), Tags: m.Tags,
		Paths: m.Paths, Author: m.Author, Uses: m.Uses, Pinned: m.Pinned, File: m.File, Score: score}
	if !m.Updated.IsZero() {
		e.Updated = m.Updated.UTC().Format(time.RFC3339)
	}
	return e
}

func newMemoryCmd() *cobra.Command {
	var cwd string
	var asJSON, all bool
	dir := func() string {
		if cwd != "" {
			return cwd
		}
		d, _ := os.Getwd()
		return d
	}
	print := func(entries []memoryEntry, lines []string) error {
		if asJSON {
			if entries == nil {
				entries = []memoryEntry{}
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(entries)
		}
		fmt.Println(strings.Join(lines, "\n"))
		return nil
	}

	cmd := &cobra.Command{
		Use:   "memory",
		Short: "What agents learned on this repository (remember/recall store)",
		// An error here is an answer ("a similar memory exists"), not misuse.
		SilenceUsage: true,
	}
	cmd.PersistentFlags().StringVar(&cwd, "cwd", "", "repository directory (default: current)")
	cmd.PersistentFlags().BoolVar(&asJSON, "json", false, "JSON output")

	ls := &cobra.Command{
		Use:   "ls",
		Short: "List this repository's memories and the global ones",
		RunE: func(c *cobra.Command, args []string) error {
			key, _ := memory.ProjectKey(dir())
			var entries []memoryEntry
			var lines []string
			now := time.Now()
			for _, m := range memory.Open().Load(key, all) {
				entries = append(entries, entry(m, 0))
				lines = append(lines, memory.Line(m, now))
			}
			return print(entries, lines)
		},
	}
	ls.Flags().BoolVar(&all, "all", false, "include superseded and archived memories")

	search := &cobra.Command{
		Use:   "search <query>",
		Short: "Search memories",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			var entries []memoryEntry
			var lines []string
			now := time.Now()
			for _, h := range memory.Open().Recall(dir(), memory.RecallInput{Query: strings.Join(args, " "), Limit: 20}) {
				entries = append(entries, entry(h.Memory, h.Score))
				lines = append(lines, memory.Line(h.Memory, now))
			}
			return print(entries, lines)
		},
	}

	var kind, scope string
	var force bool
	add := &cobra.Command{
		Use:   "add <text>",
		Short: "Save a memory (first line is its title)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			m, err := memory.Open().Remember(dir(), memory.RememberInput{
				Text: strings.Join(args, " "), Kind: kind, Scope: scope, Force: force,
			}, memory.Who{Author: "user"})
			var dup *memory.DuplicateError
			if errors.As(err, &dup) {
				return err
			}
			if err != nil {
				return err
			}
			if asJSON {
				return print([]memoryEntry{entry(m, 0)}, nil)
			}
			fmt.Printf("Saved [%s] %s\n", m.ID, m.File)
			return nil
		},
	}
	add.Flags().StringVar(&kind, "kind", "fact", "gotcha, decision, convention, fact or preference")
	add.Flags().StringVar(&scope, "scope", "project", "project or global")
	add.Flags().BoolVar(&force, "force", false, "save even if a similar memory exists")

	var budget int
	context := &cobra.Command{
		Use:   "context",
		Short: "Print the digest an agent gets at session start",
		RunE: func(c *cobra.Command, args []string) error {
			fmt.Println(memory.Open().Context(dir(), budget))
			return nil
		},
	}
	context.Flags().IntVar(&budget, "budget", 6000, "maximum bytes")

	path := &cobra.Command{
		Use:   "path",
		Short: "Print where this repository's memories are kept",
		RunE: func(c *cobra.Command, args []string) error {
			key, _ := memory.ProjectKey(dir())
			fmt.Println(filepath.Join(memory.DefaultDir(), "projects", key))
			return nil
		},
	}

	cmd.AddCommand(ls, search, add, context, path)
	for _, sub := range cmd.Commands() {
		sub.SilenceUsage = true
	}
	return cmd
}
