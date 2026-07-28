package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/schemas"
)

// parseEffort validates the shared effort selector. An empty value means "let
// the server decide" and is passed through unset.
func parseEffort(s string) (client.Effort, error) {
	switch s {
	case "":
		return "", nil
	case string(client.EffortMin), string(client.EffortStandard), string(client.EffortMax):
		return client.Effort(s), nil
	default:
		return "", fmt.Errorf("invalid effort %q: must be one of min, standard, max", s)
	}
}

// geo turns a country code into a GeoTarget, or nil when unset, matching the
// CLI's geoTarget helper.
func geo(country string) *client.GeoTarget {
	if strings.TrimSpace(country) == "" {
		return nil
	}
	return &client.GeoTarget{Country: country}
}

// resolveSchema turns the schema/schema_name argument pair into a raw JSON
// schema, mirroring the CLI's resolveSchemaArg: exactly one must be set, inline
// JSON is validated locally, and a name is resolved against the local store
// only (never the network).
func (d Deps) resolveSchema(schema, schemaName string) (json.RawMessage, error) {
	switch {
	case schema != "" && schemaName != "":
		return nil, fmt.Errorf("schema and schema_name are mutually exclusive")
	case schema == "" && schemaName == "":
		return nil, fmt.Errorf("one of schema (inline JSON) or schema_name (a pulled schema) is required")
	}

	if schemaName == "" {
		if !json.Valid([]byte(schema)) {
			return nil, fmt.Errorf("schema is not valid JSON")
		}
		return json.RawMessage(schema), nil
	}

	if d.SchemasDir == "" {
		return nil, fmt.Errorf("no local schema store available; pass an inline schema instead")
	}
	rel, err := schemas.FindLocal(d.SchemasDir, schemaName)
	if err != nil {
		return nil, err
	}
	data, _, err := schemas.Read(d.SchemasDir, rel)
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("stored schema %s is not valid JSON", rel)
	}
	return json.RawMessage(data), nil
}
