package client

import (
	"context"
)

// AutomateRequest is the body for POST /automate. The endpoint always streams
// Server-Sent Events. Task is the only required field; everything else tunes
// the run. Data is freeform context (e.g. form values) so it stays as any and
// is omitted when nil.
type AutomateRequest struct {
	Task                  string     `json:"task"`
	Data                  any        `json:"data,omitempty"`
	GeoTarget             *GeoTarget `json:"geo_target,omitempty"`
	Guardrails            string     `json:"guardrails,omitempty"`
	MaxIterations         int        `json:"maxIterations,omitempty"`
	MaxValidationAttempts int        `json:"maxValidationAttempts,omitempty"`
	URL                   string     `json:"url,omitempty"`
}

// Automate runs an AI browser-automation task, invoking fn for each streamed
// event (start, agent:processing, browser:navigated, agent:extracted,
// task:completed, complete, done). Cancellation flows through ctx.
func (c *Client) Automate(ctx context.Context, req AutomateRequest, fn func(Event) error) error {
	return c.doStream(ctx, "/automate", req, fn)
}

// AutomateInputRequest is the body for POST /automate/{requestID}/input. It
// supplies a response when an in-flight automation asks for input. The payload
// is freeform, mirroring whatever the task requested.
type AutomateInputRequest struct {
	Data any `json:"data,omitempty"`
}

// AutomateInput submits an input response for a running automation task. This
// endpoint is a plain request/response, not a stream.
func (c *Client) AutomateInput(ctx context.Context, requestID string, req AutomateInputRequest) error {
	return c.doJSON(ctx, "/automate/"+requestID+"/input", req, nil)
}

// Citation is a single source backing a research report. The report text refers
// to sources by number ([1], [2], ...); Number carries that key when the API
// supplies it, otherwise the renderer falls back to list position.
type Citation struct {
	Number int    `json:"number,omitempty"`
	Title  string `json:"title,omitempty"`
	URL    string `json:"url,omitempty"`
}

// ResearchMode selects how much work the research endpoint does.
//   - "fast": quick answers, minimal searches
//   - "balanced": standard multi-iteration research (default)
type ResearchMode string

const (
	ResearchFast     ResearchMode = "fast"
	ResearchBalanced ResearchMode = "balanced"
)

// ResearchRequest is the body for POST /research. Always streams SSE.
type ResearchRequest struct {
	Query        string       `json:"query"`
	FetchTimeout int          `json:"fetch_timeout,omitempty"`
	Mode         ResearchMode `json:"mode,omitempty"`
	NoCache      bool         `json:"nocache,omitempty"`
}

// Research runs an AI research query, invoking fn for each streamed event
// (phase, progress, complete, error).
func (c *Client) Research(ctx context.Context, req ResearchRequest, fn func(Event) error) error {
	return c.doStream(ctx, "/research", req, fn)
}
