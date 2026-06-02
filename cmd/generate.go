package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
)

// maxInstructionsLen mirrors the server-side cap on the instructions field. We
// validate locally so an over-length prompt fails fast with a clear message
// rather than coming back as an opaque API 400.
const maxInstructionsLen = 20000

// newGenerateCmd is the parent for the /generate/* endpoints. Right now that is
// just generate/json, but the grouping keeps the tree consistent with the docs
// and leaves room for future generate methods.
func newGenerateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Fetch a URL and transform its content with AI",
	}
	cmd.AddCommand(newGenerateJSONCmd())
	return cmd
}

func newGenerateJSONCmd() *cobra.Command {
	var (
		instructions string
		schema       string
		effort       string
		geo          string
		nocache      bool
	)

	cmd := &cobra.Command{
		Use:   "json <url>",
		Short: "Transform a URL's content into structured JSON",
		Long: "Fetch a URL, extract its content, then transform it with AI per your\n" +
			"instructions into the shape described by a JSON schema.\n\n" +
			"Both --instructions and --schema accept a literal string, @file, or -\n" +
			"for stdin. Only one of them can read stdin in a single invocation.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if instructions == "-" && schema == "-" {
				return withCode(2, fmt.Errorf(
					"--schema and --instructions cannot both read from stdin (-); "+
						"pass one as a literal string or @file",
				))
			}
			instr, err := readInput(instructions)
			if err != nil {
				return withCode(2, err)
			}
			if instr == "" {
				return withCode(2, fmt.Errorf("--instructions is required"))
			}
			if len(instr) > maxInstructionsLen {
				return withCode(2, fmt.Errorf("instructions exceed the %d character limit (got %d)", maxInstructionsLen, len(instr)))
			}

			schemaJSON, err := readJSON(schema)
			if err != nil {
				return withCode(2, err)
			}

			if err := validEffort(effort); err != nil {
				return withCode(2, err)
			}

			req := client.GenerateJSONRequest{
				Instructions: instr,
				JSONSchema:   schemaJSON,
				URL:          args[0],
				Effort:       client.Effort(effort),
				GeoTarget:    geoTarget(geo),
				NoCache:      nocache,
			}

			out, err := rootApp.client.GenerateJSON(context.Background(), req)
			if err != nil {
				return classifyError(err)
			}
			return rootApp.renderer.PrintJSON(out)
		},
	}

	f := cmd.Flags()
	f.StringVar(&instructions, "instructions", "", "transform instructions: literal, @file, or - (required)")
	f.StringVar(&schema, "schema", "", "output JSON schema: literal, @file, or - (required)")
	f.StringVar(&effort, "effort", "", "fetch effort: min|standard|max")
	f.StringVar(&geo, "geo", "", "geotarget country code (ISO 3166-1 alpha-2, e.g. GB)")
	f.BoolVar(&nocache, "nocache", false, "bypass cache and fetch fresh")

	return cmd
}
