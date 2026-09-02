package cmd

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
)

// newExtractCmd is the parent grouping for the /extract/* endpoints. It does no
// work itself, it just hosts the json and markdown subcommands.
func newExtractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "extract",
		Short:   "Fetch a URL and extract structured data or Markdown",
		Example: "  tabstack extract markdown https://example.com\n  tabstack extract json https://example.com --schema-name job-posting",
	}
	cmd.AddCommand(newExtractMarkdownCmd(), newExtractJSONCmd())
	return cmd
}

// shared flags for the extract subcommands. effort and geo recur across the
// fetch-based endpoints, so we wire them per-command but with identical names.
func newExtractMarkdownCmd() *cobra.Command {
	var (
		effort   string
		geo      string
		metadata bool
		nocache  bool
		raw      bool
	)

	cmd := &cobra.Command{
		Use:     "markdown <url>",
		Short:   "Convert a URL's content to clean Markdown",
		Example: "  # Clean Markdown for a page\n  tabstack extract markdown https://example.com\n\n  # Include the title, author, and other page metadata\n  tabstack extract markdown https://example.com --metadata\n\n  # Skip the cache, fetch via a UK exit node, work harder at it\n  tabstack extract markdown https://example.com --no-cache --geo GB --effort max\n\n  # Save the Markdown itself to a file (without --raw a redirect gets JSON)\n  tabstack extract markdown https://example.com --raw > page.md\n\n  # Same thing without the flag, by pulling the field out of the envelope\n  tabstack extract markdown https://example.com | jq -r .content > page.md",
		Args:    exactArgsNamed("<url>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validURL(args[0]); err != nil {
				return withCode(2, err)
			}
			if err := validFetchFlags(effort, geo); err != nil {
				return withCode(2, err)
			}
			// --raw is defined as "the document body and nothing else", so a
			// metadata header would contradict it. Refuse rather than silently
			// dropping whichever flag the user meant.
			if raw && metadata {
				return withCode(2, errors.New("cannot combine --raw with --metadata: --raw prints the document body only"))
			}
			req := client.ExtractMarkdownRequest{
				URL:       args[0],
				Effort:    client.Effort(effort),
				GeoTarget: geoTarget(geo),
				Metadata:  metadata,
				NoCache:   nocache,
			}

			resp, err := rootApp.client.ExtractMarkdown(context.Background(), req)
			if err != nil {
				return classifyError(err)
			}
			if raw {
				return rootApp.renderer.PrintRaw(resp.Content)
			}
			return rootApp.renderer.PrintMarkdown(resp)
		},
	}

	f := cmd.Flags()
	f.StringVar(&effort, "effort", "", "fetch effort: min|standard|max")
	_ = cmd.RegisterFlagCompletionFunc("effort", fixedCompletions("min", "standard", "max"))
	f.StringVar(&geo, "geo", "", "geotarget country code (ISO 3166-1 alpha-2, e.g. GB)")
	f.BoolVar(&metadata, "metadata", false, "include extracted page metadata")
	f.BoolVar(&raw, "raw", false, "print only the Markdown body, no header or JSON envelope (for redirecting to a file)")
	addNoCacheFlag(f, &nocache)

	return cmd
}

func newExtractJSONCmd() *cobra.Command {
	var (
		schema     string
		schemaName string
		storage    string
		effort     string
		geo        string
		nocache    bool
	)

	cmd := &cobra.Command{
		Use:   "json <url>",
		Short: "Extract structured data from a URL using a JSON schema",
		Long: "Fetch a URL and extract data shaped by a JSON schema.\n\n" +
			"Provide the schema inline with --schema (a literal string, @file, or -\n" +
			"for stdin), or use --schema-name to reference a schema you pulled with\n" +
			"`tabstack schema pull` (a bare name or full repo path).",
		Example: "  # Inline schema\n  tabstack extract json https://example.com --schema '{\"type\":\"object\",\"properties\":{\"title\":{\"type\":\"string\"}}}'\n\n  # Schema from a file, or from stdin\n  tabstack extract json https://example.com --schema @schema.json\n  cat schema.json | tabstack extract json https://example.com --schema -\n\n  # A schema you pulled from the library\n  tabstack schema pull job-posting\n  tabstack extract json https://example.com/jobs/1 --schema-name job-posting",
		Args:    exactArgsNamed("<url>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validURL(args[0]); err != nil {
				return withCode(2, err)
			}
			if err := validFetchFlags(effort, geo); err != nil {
				return withCode(2, err)
			}

			schemaJSON, err := resolveSchemaArg(schema, schemaName, storage)
			if err != nil {
				return err
			}

			req := client.ExtractJSONRequest{
				JSONSchema: schemaJSON,
				URL:        args[0],
				Effort:     client.Effort(effort),
				GeoTarget:  geoTarget(geo),
				NoCache:    nocache,
			}

			out, err := rootApp.client.ExtractJSON(context.Background(), req)
			if err != nil {
				return classifyError(err)
			}
			return rootApp.renderer.PrintJSON(out)
		},
	}

	f := cmd.Flags()
	f.StringVar(&schema, "schema", "", "schema as JSON: literal, @file, or - for stdin")
	f.StringVar(&schemaName, "schema-name", "", "name of a pulled schema to use (see `tabstack schema pull`)")
	f.StringVar(&storage, "storage", "", "schema store directory for --schema-name (default: config dir)")
	f.StringVar(&effort, "effort", "", "fetch effort: min|standard|max")
	_ = cmd.RegisterFlagCompletionFunc("effort", fixedCompletions("min", "standard", "max"))
	f.StringVar(&geo, "geo", "", "geotarget country code (ISO 3166-1 alpha-2, e.g. GB)")
	addNoCacheFlag(f, &nocache)
	_ = cmd.RegisterFlagCompletionFunc("schema-name", completeLocalSchemaNames)

	return cmd
}
