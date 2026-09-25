package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nemke/nagare-go/internal/trust"
	"github.com/nemke/nagare-go/internal/verify"
)

// nagareFiles are the repository files that act on the user's behalf and
// so need approving.
var nagareFiles = []string{verify.FileName, ".nagare/policy"}

func newTrustCmd() *cobra.Command {
	var cwd string
	var check, revoke, quiet bool
	cmd := &cobra.Command{
		Use:   "trust [file...]",
		Short: "Approve a repository's .nagare/verify and .nagare/policy (they act only once approved)",
		Long: `A repository's .nagare/verify runs when an agent stops, and .nagare/policy
approves tool calls for you. Because they come with the repository, they act
only after you approve their exact contents; any change needs approving again.

Without file arguments, the .nagare files of the repository in --cwd are used.`,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			files := args
			if len(files) == 0 {
				dir := cwd
				if dir == "" {
					dir, _ = os.Getwd()
				}
				for _, name := range nagareFiles {
					if f := verify.FindFile(dir, name); f != "" {
						files = append(files, f)
					}
				}
			}
			if len(files) == 0 {
				if !quiet {
					fmt.Println("No .nagare/verify or .nagare/policy here.")
				}
				return nil
			}
			s := trust.Open()
			var untrusted []string
			for _, f := range files {
				switch {
				case check:
					if !s.Trusted(f) {
						untrusted = append(untrusted, f)
					}
				case revoke:
					if err := s.Revoke(f); err != nil {
						return err
					}
					if !quiet {
						fmt.Println("Revoked", f)
					}
				default:
					data, err := os.ReadFile(f)
					if err != nil {
						return err
					}
					if err := s.Allow(f); err != nil {
						return err
					}
					if !quiet {
						fmt.Printf("Approved %s:\n%s\n", f, indent(string(data)))
					}
				}
			}
			if check && len(untrusted) > 0 {
				if !quiet {
					fmt.Println("Not approved:", strings.Join(untrusted, ", "))
				}
				return errors.New("untrusted")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cwd, "cwd", "", "repository directory (default: current)")
	cmd.Flags().BoolVar(&check, "check", false, "exit non-zero if any file is not approved")
	cmd.Flags().BoolVar(&revoke, "revoke", false, "withdraw approval")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "no output")
	return cmd
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}
