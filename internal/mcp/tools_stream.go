package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
)

// maxQueryLen mirrors the server-side cap on the research query field.
const maxQueryLen = 10000

// addStreamTools registers the SSE endpoints (automate, research). Each streams
// progress events; we forward them as MCP progress notifications and aggregate
// the final answer plus in-band success/failure into the result. These runs are
// long-lived and carry no client-side timeout: cancellation flows through the
// request context, so a client that cancels stops the stream.
func addStreamTools(s *sdk.Server, d Deps) {
	sdk.AddTool(s, &sdk.Tool{
		Name:        "automate",
		Description: "Run an AI browser-automation task described in natural language. Streams progress; returns the final answer. This can take a while.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in automateIn) (*sdk.CallToolResult, any, error) {
		if in.MaxIterations != 0 && (in.MaxIterations < 1 || in.MaxIterations > 100) {
			return nil, nil, fmt.Errorf("max_iterations must be between 1 and 100 (got %d)", in.MaxIterations)
		}
		areq := client.AutomateRequest{
			Task:          in.Task,
			URL:           in.URL,
			Guardrails:    in.Guardrails,
			MaxIterations: in.MaxIterations,
			GeoTarget:     geo(in.Country),
		}
		res, err := runStream(ctx, req, func(fn func(client.Event) error) error {
			return d.Product.Automate(ctx, areq, fn)
		})
		if err != nil {
			return nil, nil, err
		}
		return res.toolResult("automation")
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "research",
		Description: "Search the web, analyse sources, and synthesise a cited answer to a query. Streams progress; returns the final report. Mode fast (default) is quick; balanced does deeper multi-source work.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in researchIn) (*sdk.CallToolResult, any, error) {
		if len(in.Query) > maxQueryLen {
			return nil, nil, tooLong("query", len(in.Query), maxQueryLen)
		}
		if in.Mode != "" && in.Mode != "fast" && in.Mode != "balanced" {
			return nil, nil, fmt.Errorf("invalid mode %q: must be fast or balanced", in.Mode)
		}
		rreq := client.ResearchRequest{
			Query:        in.Query,
			Mode:         client.ResearchMode(in.Mode),
			FetchTimeout: in.FetchTimeout,
			NoCache:      in.NoCache,
		}
		res, err := runStream(ctx, req, func(fn func(client.Event) error) error {
			return d.Product.Research(ctx, rreq, fn)
		})
		if err != nil {
			return nil, nil, err
		}
		return res.toolResult("research")
	})
}

type automateIn struct {
	Task          string `json:"task" jsonschema:"the automation task, described in natural language"`
	URL           string `json:"url,omitempty" jsonschema:"starting URL for the task"`
	Guardrails    string `json:"guardrails,omitempty" jsonschema:"safety constraints for execution"`
	MaxIterations int    `json:"max_iterations,omitempty" jsonschema:"maximum task iterations (1-100)"`
	Country       string `json:"country,omitempty" jsonschema:"ISO 3166-1 alpha-2 country code to geo-target the fetch"`
}

type researchIn struct {
	Query        string `json:"query" jsonschema:"the research question"`
	Mode         string `json:"mode,omitempty" jsonschema:"research mode: fast (default) or balanced"`
	FetchTimeout int    `json:"fetch_timeout,omitempty" jsonschema:"per-page fetch timeout in seconds"`
	NoCache      bool   `json:"nocache,omitempty" jsonschema:"skip the cache and force fresh research"`
}

// streamOutcome is what the SSE loop learned about how a run ended. The
// streaming endpoints report failure in-band (a completion event with
// success:false, or an error event) rather than via HTTP status.
type streamOutcome struct {
	failed      bool
	failMessage string
	finalAnswer string
}

// runStream drives an SSE call, forwarding each event as an MCP progress
// notification (when the client supplied a progress token) and recording the
// outcome.
func runStream(ctx context.Context, req *sdk.CallToolRequest, call func(fn func(client.Event) error) error) (streamOutcome, error) {
	var res streamOutcome
	token := req.Params.GetProgressToken()
	var n float64

	err := call(func(e client.Event) error {
		switch e.Name {
		case "error":
			res.failed = true
			res.failMessage = eventMessage(e)
		case "task:completed", "complete", "done":
			var d struct {
				Success *bool `json:"success"`
			}
			if e.Decode(&d) == nil && d.Success != nil && !*d.Success {
				res.failed = true
			}
			if t := finalText(e); t != "" {
				res.finalAnswer = t
			}
		}

		if token != nil {
			n++
			msg := e.Name
			if m := eventMessage(e); m != "" && m != e.DataString() {
				msg = e.Name + ": " + m
			}
			// Best-effort: a failed notification must not abort the run.
			_ = req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{
				ProgressToken: token,
				Message:       msg,
				Progress:      n,
			})
		}
		return nil
	})
	return res, err
}

// toolResult maps a finished stream onto a CallToolResult. An in-band failure
// becomes a tool error so the client sees it as a failed call.
func (o streamOutcome) toolResult(kind string) (*sdk.CallToolResult, any, error) {
	if o.failed {
		msg := o.failMessage
		if msg == "" {
			msg = kind + " reported failure"
		} else {
			msg = kind + " failed: " + msg
		}
		return nil, nil, fmt.Errorf("%s", msg)
	}
	answer := o.finalAnswer
	if strings.TrimSpace(answer) == "" {
		answer = "(" + kind + " completed with no final answer)"
	}
	return textResult(answer), nil, nil
}

// finalText pulls the report/answer out of a terminal event, trying the field
// names the automate and research streams plausibly use, then a bare JSON
// string. First non-empty wins. Mirrors cmd/agent.go.
func finalText(e client.Event) string {
	var d struct {
		FinalAnswer string `json:"finalAnswer"`
		Answer      string `json:"answer"`
		Report      string `json:"report"`
		Result      string `json:"result"`
		Summary     string `json:"summary"`
		Content     string `json:"content"`
	}
	if e.Decode(&d) == nil {
		for _, s := range []string{d.FinalAnswer, d.Answer, d.Report, d.Result, d.Summary, d.Content} {
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	var s string
	if json.Unmarshal(e.Data, &s) == nil && strings.TrimSpace(s) != "" {
		return s
	}
	return ""
}

// eventMessage pulls a human message out of an event payload, falling back to
// the raw data.
func eventMessage(e client.Event) string {
	var d struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if e.Decode(&d) == nil {
		if d.Error != "" {
			return d.Error
		}
		if d.Message != "" {
			return d.Message
		}
	}
	return e.DataString()
}
