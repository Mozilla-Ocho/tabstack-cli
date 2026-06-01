package client

import (
	"context"
	"encoding/json"
)

// GeoTarget is the optional geotargeting block shared by several endpoints.
// Country is an ISO 3166-1 alpha-2 code, e.g. "US", "GB", "JP".
type GeoTarget struct {
	Country string `json:"country,omitempty"`
}

// Effort controls the speed/capability tradeoff on fetch-based endpoints.
//   - "min": fastest, no fallback (1-5s)
//   - "standard": balanced, default (3-15s)
//   - "max": full browser rendering for JS-heavy sites (15-60s)
type Effort string

const (
	EffortMin      Effort = "min"
	EffortStandard Effort = "standard"
	EffortMax      Effort = "max"
)

// ExtractJSONRequest is the body for POST /extract/json. JSONSchema is an
// arbitrary JSON schema object describing the data to pull out, so we keep it
// as json.RawMessage and let the caller supply it verbatim from a file.
type ExtractJSONRequest struct {
	JSONSchema json.RawMessage `json:"json_schema"`
	URL        string          `json:"url"`
	Effort     Effort          `json:"effort,omitempty"`
	GeoTarget  *GeoTarget      `json:"geo_target,omitempty"`
	NoCache    bool            `json:"nocache,omitempty"`
}

// ExtractJSON fetches a URL and extracts structured data per the schema. The
// response shape is dictated by the caller's schema, so we return the raw JSON.
func (c *Client) ExtractJSON(ctx context.Context, req ExtractJSONRequest) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.doJSON(ctx, "/extract/json", req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ExtractMarkdownRequest is the body for POST /extract/markdown.
type ExtractMarkdownRequest struct {
	URL       string     `json:"url"`
	Effort    Effort     `json:"effort,omitempty"`
	GeoTarget *GeoTarget `json:"geo_target,omitempty"`
	Metadata  bool       `json:"metadata,omitempty"`
	NoCache   bool       `json:"nocache,omitempty"`
}

// Metadata is the optional page metadata block returned when Metadata is
// requested. Every field is optional and absent fields stay zero valued.
type Metadata struct {
	Author      string   `json:"author,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	Creator     string   `json:"creator,omitempty"`
	Description string   `json:"description,omitempty"`
	Image       string   `json:"image,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	ModifiedAt  string   `json:"modified_at,omitempty"`
	PageCount   int      `json:"page_count,omitempty"`
	PDFVersion  string   `json:"pdf_version,omitempty"`
	Producer    string   `json:"producer,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	SiteName    string   `json:"site_name,omitempty"`
	Subject     string   `json:"subject,omitempty"`
	Title       string   `json:"title,omitempty"`
	Type        string   `json:"type,omitempty"`
	URL         string   `json:"url,omitempty"`
}

// ExtractMarkdownResponse is the body for a successful /extract/markdown call.
type ExtractMarkdownResponse struct {
	Content  string    `json:"content"`
	URL      string    `json:"url"`
	Metadata *Metadata `json:"metadata,omitempty"`
}

// ExtractMarkdown fetches a URL and converts it to clean Markdown.
func (c *Client) ExtractMarkdown(ctx context.Context, req ExtractMarkdownRequest) (ExtractMarkdownResponse, error) {
	var out ExtractMarkdownResponse
	err := c.doJSON(ctx, "/extract/markdown", req, &out)
	return out, err
}
