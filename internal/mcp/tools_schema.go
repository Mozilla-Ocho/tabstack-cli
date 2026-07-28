package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/schemas"
)

// addSchemaTools registers read-only access to the local schema store, so a
// client can discover pulled schemas and feed them into extract_json /
// generate_json via schema_name. These never touch the network or the product
// bearer; pulling new schemas stays a CLI operation.
func addSchemaTools(s *sdk.Server, d Deps) {
	sdk.AddTool(s, &sdk.Tool{
		Name:        "schema_list",
		Description: "List the extraction schemas available in the local store (usable as schema_name in extract_json / generate_json).",
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, schemaListOut, error) {
		if d.SchemasDir == "" {
			return nil, schemaListOut{}, fmt.Errorf("no local schema store available")
		}
		names, err := schemas.ListLocal(d.SchemasDir)
		if err != nil {
			return nil, schemaListOut{}, err
		}
		return nil, schemaListOut{Schemas: names}, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "schema_resolve",
		Description: "Resolve a schema selector against the local store and return its path and contents.",
	}, func(_ context.Context, _ *sdk.CallToolRequest, in schemaResolveIn) (*sdk.CallToolResult, schemaResolveOut, error) {
		if d.SchemasDir == "" {
			return nil, schemaResolveOut{}, fmt.Errorf("no local schema store available")
		}
		rel, err := schemas.FindLocal(d.SchemasDir, in.Name)
		if err != nil {
			return nil, schemaResolveOut{}, err
		}
		data, _, err := schemas.Read(d.SchemasDir, rel)
		if err != nil {
			return nil, schemaResolveOut{}, err
		}
		if !json.Valid(data) {
			return nil, schemaResolveOut{}, fmt.Errorf("stored schema %s is not valid JSON", rel)
		}
		return nil, schemaResolveOut{
			Name:   rel,
			Path:   schemas.LocalPath(d.SchemasDir, rel),
			Schema: json.RawMessage(data),
		}, nil
	})
}

type schemaListOut struct {
	Schemas []string `json:"schemas"`
}

type schemaResolveIn struct {
	Name string `json:"name" jsonschema:"a schema selector: bare name, category, or full store path"`
}

type schemaResolveOut struct {
	Name   string          `json:"name"`
	Path   string          `json:"path"`
	Schema json.RawMessage `json:"schema"`
}
