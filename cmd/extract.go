package cmd

import (
	"context"
	"encoding/json"
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
		effort      string
		geo         string
		metadata    bool
		nocache     bool
		raw         bool
		concurrency int
		outputDir   string
		batch       bool
		force       bool
	)

	cmd := &cobra.Command{
		Use:     "markdown <url>...",
		Short:   "Convert a URL's content to clean Markdown",
		Example: "  # Clean Markdown for a page\n  tabstack extract markdown https://example.com\n\n  # Include the title, author, and other page metadata\n  tabstack extract markdown https://example.com --metadata\n\n  # Skip the cache, fetch via a UK exit node, work harder at it\n  tabstack extract markdown https://example.com --no-cache --geo GB --effort max\n\n  # Save the Markdown itself to a file (without --raw a redirect gets JSON)\n  tabstack extract markdown https://example.com --raw > page.md\n\n  # Same thing without the flag, by pulling the field out of the envelope\n  tabstack extract markdown https://example.com | jq -r .content > page.md",
		Args:    minArgsNamed(1, "<url>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validFetchFlags(effort, geo); err != nil {
				return withCode(2, err)
			}
			// --raw is defined as "the document body and nothing else", so a
			// metadata header would contradict it. Refuse rather than silently
			// dropping whichever flag the user meant.
			if raw && metadata {
				return withCode(2, errors.New("cannot combine --raw with --metadata: --raw prints the document body only"))
			}

			// extract markdown has no flag that reads stdin, so a bare "-"
			// can only mean the URL list.
			urls, err := resolveURLs(args, "")
			if err != nil {
				return err
			}
			if err := validateURLs(urls); err != nil {
				return err
			}
			if err := checkRawBatch(raw, len(urls), outputDir); err != nil {
				return err
			}

			build := func(target string) client.ExtractMarkdownRequest {
				return client.ExtractMarkdownRequest{
					URL:       target,
					Effort:    client.Effort(effort),
					GeoTarget: geoTarget(geo),
					Metadata:  metadata,
					NoCache:   nocache,
				}
			}

			// One URL and no batch flags: the original single-result output,
			// unchanged, so existing scripts parsing it keep working.
			if len(urls) == 1 && !batch && outputDir == "" {
				resp, err := rootApp.client.ExtractMarkdown(cmd.Context(), build(urls[0]))
				if err != nil {
					return classifyError(err)
				}
				if raw {
					return rootApp.renderer.PrintRaw(resp.Content)
				}
				return rootApp.renderer.PrintMarkdown(resp)
			}

			fetch := func(ctx context.Context, target string) (json.RawMessage, []byte, error) {
				resp, err := rootApp.client.ExtractMarkdown(ctx, build(target))
				if err != nil {
					return nil, nil, err
				}
				result, err := json.Marshal(resp)
				if err != nil {
					return nil, nil, err
				}
				return result, []byte(resp.Content), nil
			}

			return runExtractBatch(cmd.Context(), urls, batchOptions{
				concurrency: concurrency,
				outputDir:   outputDir,
				ext:         ".md",
				force:       force,
			}, fetch, func(item batchItem) string {
				return string(item.body)
			})
		},
	}

	f := cmd.Flags()
	f.StringVar(&effort, "effort", "", "fetch effort: min|standard|max")
	_ = cmd.RegisterFlagCompletionFunc("effort", fixedCompletions("min", "standard", "max"))
	f.StringVar(&geo, "geo", "", "geotarget country code (ISO 3166-1 alpha-2, e.g. GB)")
	f.BoolVar(&metadata, "metadata", false, "include extracted page metadata")
	f.BoolVar(&raw, "raw", false, "print only the Markdown body, no header or JSON envelope (for redirecting to a file)")
	addNoCacheFlag(f, &nocache)
	addBatchFlags(f, &concurrency, &outputDir, &batch, &force)

	return cmd
}

func newExtractJSONCmd() *cobra.Command {
	var (
		schema      string
		schemaName  string
		storage     string
		effort      string
		geo         string
		nocache     bool
		concurrency int
		outputDir   string
		batch       bool
		force       bool
	)

	cmd := &cobra.Command{
		Use:   "json <url>...",
		Short: "Extract structured data from a URL using a JSON schema",
		Long: "Fetch a URL and extract data shaped by a JSON schema.\n\n" +
			"Provide the schema inline with --schema (a literal string, @file, or -\n" +
			"for stdin), or use --schema-name to reference a schema you pulled with\n" +
			"`tabstack schema pull` (a bare name or full repo path).",
		Example: "  # Inline schema\n  tabstack extract json https://example.com --schema '{\"type\":\"object\",\"properties\":{\"title\":{\"type\":\"string\"}}}'\n\n  # Schema from a file, or from stdin\n  tabstack extract json https://example.com --schema @schema.json\n  cat schema.json | tabstack extract json https://example.com --schema -\n\n  # A schema you pulled from the library\n  tabstack schema pull job-posting\n  tabstack extract json https://example.com/jobs/1 --schema-name job-posting",
		Args:    minArgsNamed(1, "<url>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validFetchFlags(effort, geo); err != nil {
				return withCode(2, err)
			}

			// Only one thing per invocation may read stdin. Resolve the URL
			// list first so the conflict is reported before the schema is
			// consumed, otherwise the list would arrive empty and the real
			// cause would be invisible.
			stdinTaken := ""
			if schema == "-" {
				stdinTaken = "--schema"
			}
			urls, err := resolveURLs(args, stdinTaken)
			if err != nil {
				return err
			}
			if err := validateURLs(urls); err != nil {
				return err
			}

			// Resolved once for the whole batch, so `--schema -` reads stdin a
			// single time and the shape hint fires once rather than per URL.
			schemaJSON, err := resolveSchemaArg(schema, schemaName, storage)
			if err != nil {
				return err
			}

			build := func(target string) client.ExtractJSONRequest {
				return client.ExtractJSONRequest{
					JSONSchema: schemaJSON,
					URL:        target,
					Effort:     client.Effort(effort),
					GeoTarget:  geoTarget(geo),
					NoCache:    nocache,
				}
			}

			if len(urls) == 1 && !batch && outputDir == "" {
				out, err := rootApp.client.ExtractJSON(cmd.Context(), build(urls[0]))
				if err != nil {
					return classifyError(err)
				}
				return rootApp.renderer.PrintJSON(out)
			}

			fetch := func(ctx context.Context, target string) (json.RawMessage, []byte, error) {
				out, err := rootApp.client.ExtractJSON(ctx, build(target))
				if err != nil {
					return nil, nil, err
				}
				return out, out, nil
			}

			return runExtractBatch(cmd.Context(), urls, batchOptions{
				concurrency: concurrency,
				outputDir:   outputDir,
				ext:         ".json",
				force:       force,
			}, fetch, func(item batchItem) string {
				return indentJSON(item.Result)
			})
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
	addBatchFlags(f, &concurrency, &outputDir, &batch, &force)
	_ = cmd.RegisterFlagCompletionFunc("schema-name", completeLocalSchemaNames)

	return cmd
}
