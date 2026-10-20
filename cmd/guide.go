package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	tabstack "github.com/Mozilla-Ocho/tabstack-cli"
)

// newAgentGuideCmd prints the embedded agent guide. The output is the raw
// Markdown in every output mode: the readers are LLM agents, for whom Markdown
// is already the native format, and wrapping it in JSON would only add escaping.
func newAgentGuideCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent-guide",
		Short: "Print the guide for driving tabstack from an AI agent",
		Long: "Print the reference for using tabstack from an AI agent: when to use each " +
			"command, every flag, output shapes, rate limits, and exit codes. It is " +
			"embedded in the binary, so it always matches the installed version.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), tabstack.AgentGuide)
			return err
		},
	}
}
