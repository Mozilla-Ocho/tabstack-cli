package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
)

// newExtractCmd is the parent grouping for the /extract/* endpoints. It does no
// work itself, it just hosts the json and markdown subcommands.
func newExtractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Fetch a URL and extract structured data or Markdown",
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
	)

	cmd := &cobra.Command{
		Use:   "markdown <url>",
		Short: "Convert a URL's content to clean Markdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validEffort(effort); err != nil {
				return withCode(2, err)
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
			return rootApp.renderer.PrintMarkdown(resp)
		},
	}

	f := cmd.Flags()
	f.StringVar(&effort, "effort", "", "fetch effort: min|standard|max")
	f.StringVar(&geo, "geo", "", "geotarget country code (ISO 3166-1 alpha-2, e.g. GB)")
	f.BoolVar(&metadata, "metadata", false, "include extracted page metadata")
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
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validEffort(effort); err != nil {
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
	f.StringVar(&geo, "geo", "", "geotarget country code (ISO 3166-1 alpha-2, e.g. GB)")
	addNoCacheFlag(f, &nocache)
	_ = cmd.RegisterFlagCompletionFunc("schema-name", completeLocalSchemaNames)

	return cmd
}
