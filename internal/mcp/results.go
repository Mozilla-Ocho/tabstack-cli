package mcp

import (
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// rawResult wraps a caller-defined JSON payload (from a schema-driven endpoint)
// as a tool result. The response shape is defined by the supplied JSON schema,
// not by us, so it is returned verbatim as text content and the tool declares
// no output schema (Out is any).
func rawResult(raw json.RawMessage) (*sdk.CallToolResult, any, error) {
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}},
	}, nil, nil
}

// textResult returns a plain-text tool result.
func textResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: text}},
	}
}

// tooLong is the error for an input that exceeds a known server-side cap. We
// reject it locally so the model gets a clear message instead of an opaque API
// 400.
func tooLong(field string, got, max int) error {
	return fmt.Errorf("%s is %d characters, over the %d limit", field, got, max)
}
