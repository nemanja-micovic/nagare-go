package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/nemke/nagare-go/internal/usage"
)

func newUsageCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "usage <transcript.jsonl>...",
		Short:        "Tokens, context fill and API-equivalent cost of agent sessions",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			out := map[string]usage.Usage{}
			for _, path := range args {
				u, err := usage.File(path)
				if err != nil {
					continue // a transcript that has gone is not an error for the caller
				}
				out[path] = u
			}
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(out)
			}
			for path, u := range out {
				fmt.Printf("%s\n  %s · %d turns · %d%% of context · $%.2f\n", path, u.Model, u.Turns, u.ContextPct, u.Cost)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output keyed by path")
	return cmd
}
