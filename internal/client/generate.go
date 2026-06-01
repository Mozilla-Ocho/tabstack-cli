package client

import (
	"context"
	"encoding/json"
)

// GenerateJSONRequest is the body for POST /generate/json. It fetches a URL,
// extracts content, then transforms it with AI per the instructions. The
// output is shaped by JSONSchema. Instructions caps at 20,000 characters server
// side, so we let the command layer validate length and just pass it through.
type GenerateJSONRequest struct {
	Instructions string          `json:"instructions"`
	JSONSchema   json.RawMessage `json:"json_schema"`
	URL          string          `json:"url"`
	Effort       Effort          `json:"effort,omitempty"`
	GeoTarget    *GeoTarget      `json:"geo_target,omitempty"`
	NoCache      bool            `json:"nocache,omitempty"`
}

// GenerateJSON fetches and transforms content into the caller's schema. As with
// extract/json, the response shape is caller-defined so we return raw JSON.
func (c *Client) GenerateJSON(ctx context.Context, req GenerateJSONRequest) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.doJSON(ctx, "/generate/json", req, &out); err != nil {
		return nil, err
	}
	return out, nil
}
