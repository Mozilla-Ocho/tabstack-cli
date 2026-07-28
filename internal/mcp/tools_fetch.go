package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
)

// addFetchTools registers the request/response product endpoints: the two
// extract variants and generate. These are plain doJSON calls, so they map
// cleanly onto a single tool call and result.
func addFetchTools(s *sdk.Server, d Deps) {
	sdk.AddTool(s, &sdk.Tool{
		Name:        "extract_markdown",
		Description: "Fetch a URL and return its main content as clean Markdown, optionally with page metadata.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in extractMarkdownIn) (*sdk.CallToolResult, extractMarkdownOut, error) {
		eff, err := parseEffort(in.Effort)
		if err != nil {
			return nil, extractMarkdownOut{}, err
		}
		res, err := d.Product.ExtractMarkdown(ctx, client.ExtractMarkdownRequest{
			URL:       in.URL,
			Effort:    eff,
			GeoTarget: geo(in.Country),
			Metadata:  in.Metadata,
			NoCache:   in.NoCache,
		})
		if err != nil {
			return nil, extractMarkdownOut{}, err
		}
		return nil, extractMarkdownOut{Content: res.Content, URL: res.URL, Metadata: res.Metadata}, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "extract_json",
		Description: "Fetch a URL and extract structured data shaped by a JSON schema. Provide either an inline `schema` (a JSON Schema string) or `schema_name` (a schema pulled into the local store).",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in extractJSONIn) (*sdk.CallToolResult, any, error) {
		schema, err := d.resolveSchema(in.Schema, in.SchemaName)
		if err != nil {
			return nil, nil, err
		}
		eff, err := parseEffort(in.Effort)
		if err != nil {
			return nil, nil, err
		}
		out, err := d.Product.ExtractJSON(ctx, client.ExtractJSONRequest{
			JSONSchema: schema,
			URL:        in.URL,
			Effort:     eff,
			GeoTarget:  geo(in.Country),
			NoCache:    in.NoCache,
		})
		if err != nil {
			return nil, nil, err
		}
		return rawResult(out)
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "generate_json",
		Description: "Fetch a URL, then transform its content with AI per free-text instructions into structured data shaped by a JSON schema. Provide either an inline `schema` or a `schema_name`.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in generateJSONIn) (*sdk.CallToolResult, any, error) {
		if len(in.Instructions) > maxInstructionsLen {
			return nil, nil, tooLong("instructions", len(in.Instructions), maxInstructionsLen)
		}
		schema, err := d.resolveSchema(in.Schema, in.SchemaName)
		if err != nil {
			return nil, nil, err
		}
		eff, err := parseEffort(in.Effort)
		if err != nil {
			return nil, nil, err
		}
		out, err := d.Product.GenerateJSON(ctx, client.GenerateJSONRequest{
			Instructions: in.Instructions,
			JSONSchema:   schema,
			URL:          in.URL,
			Effort:       eff,
			GeoTarget:    geo(in.Country),
			NoCache:      in.NoCache,
		})
		if err != nil {
			return nil, nil, err
		}
		return rawResult(out)
	})
}

// maxInstructionsLen mirrors the server-side cap enforced by `generate json`.
const maxInstructionsLen = 20000

type extractMarkdownIn struct {
	URL      string `json:"url" jsonschema:"the URL to fetch and convert to Markdown"`
	Effort   string `json:"effort,omitempty" jsonschema:"extraction effort: min, standard, or max (default: server decides)"`
	Country  string `json:"country,omitempty" jsonschema:"ISO 3166-1 alpha-2 country code to geo-target the fetch, e.g. GB"`
	Metadata bool   `json:"metadata,omitempty" jsonschema:"include page metadata (title, author, etc.) in the result"`
	NoCache  bool   `json:"nocache,omitempty" jsonschema:"bypass the cache and force a fresh fetch"`
}

type extractMarkdownOut struct {
	Content  string           `json:"content"`
	URL      string           `json:"url"`
	Metadata *client.Metadata `json:"metadata,omitempty"`
}

type extractJSONIn struct {
	URL        string `json:"url" jsonschema:"the URL to fetch and extract from"`
	Schema     string `json:"schema,omitempty" jsonschema:"an inline JSON Schema describing the shape to extract; mutually exclusive with schema_name"`
	SchemaName string `json:"schema_name,omitempty" jsonschema:"the name of a schema in the local store; mutually exclusive with schema"`
	Effort     string `json:"effort,omitempty" jsonschema:"extraction effort: min, standard, or max"`
	Country    string `json:"country,omitempty" jsonschema:"ISO 3166-1 alpha-2 country code to geo-target the fetch"`
	NoCache    bool   `json:"nocache,omitempty" jsonschema:"bypass the cache and force a fresh fetch"`
}

type generateJSONIn struct {
	URL          string `json:"url" jsonschema:"the URL to fetch as source content"`
	Instructions string `json:"instructions" jsonschema:"free-text instructions describing the transformation to perform"`
	Schema       string `json:"schema,omitempty" jsonschema:"an inline JSON Schema describing the output shape; mutually exclusive with schema_name"`
	SchemaName   string `json:"schema_name,omitempty" jsonschema:"the name of a schema in the local store; mutually exclusive with schema"`
	Effort       string `json:"effort,omitempty" jsonschema:"extraction effort: min, standard, or max"`
	Country      string `json:"country,omitempty" jsonschema:"ISO 3166-1 alpha-2 country code to geo-target the fetch"`
	NoCache      bool   `json:"nocache,omitempty" jsonschema:"bypass the cache and force a fresh fetch"`
}
