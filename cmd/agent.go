package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// maxQueryLen mirrors the server-side cap on the research query field. We
// validate locally so an over-length query fails fast with a clear message
// rather than coming back as an opaque API 400.
const maxQueryLen = 10000

// newAgentCmd is the parent for the /automate, /automate/{id}/input, and
// /research endpoints. automate and research stream Server-Sent Events; input
// is a plain request/response.
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run AI browser-automation and research tasks",
	}
	cmd.AddCommand(newAutomateCmd(), newResearchCmd(), newInputCmd())
	return cmd
}

// streamResult carries what the SSE loop learned about how a run ended, so the
// command can map it onto an exit code. The streaming endpoints signal failure
// in-band (a task:completed with success:false, or an error event) rather than
// with an HTTP status, so we have to watch the events to script correctly.
type streamResult struct {
	failed      bool
	failMessage string
	finalAnswer string
	citations   []client.Citation
}

// runStream drives an SSE call: it shows a spinner between events in pretty
// mode, renders each event, and records the outcome. The actual API call is
// passed in as call so this works for both automate and research.
func runStream(call func(fn func(client.Event) error) error) (streamResult, error) {
	r := rootApp.renderer
	var res streamResult

	// Only animate when we are in pretty mode on a real terminal. Piped or JSON
	// output gets clean NDJSON with no spinner noise.
	var sp *ui.Spinner
	usingSpinner := r.Mode == ui.ModePretty && isatty.IsTerminal(os.Stderr.Fd())
	if usingSpinner {
		sp = ui.NewSpinner(os.Stderr, "working")
		sp.Start()
		defer sp.Stop()
	}

	err := call(func(e client.Event) error {
		if sp != nil {
			sp.Pause()
		}

		// Inspect outcome-bearing events for the exit code and final answer.
		switch e.Name {
		case "error":
			res.failed = true
			res.failMessage = extractMessage(e)
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
			if c := extractCitations(e); len(c) > 0 {
				res.citations = c
			}
		}

		if renderErr := r.RenderEvent(e); renderErr != nil {
			return renderErr
		}
		if sp != nil {
			sp.Resume()
		}
		return nil
	})

	return res, err
}

// finalText pulls the report/answer text out of a terminal event. The automate
// stream carries it as "finalAnswer"; the research stream's complete event uses
// a different key, so we try the names both endpoints plausibly use and fall
// back to a bare JSON-string payload. First non-empty field wins.
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

	// The payload may be a bare JSON string rather than an object.
	var s string
	if json.Unmarshal(e.Data, &s) == nil && strings.TrimSpace(s) != "" {
		return s
	}
	return ""
}

// extractCitations pulls the source list out of a research "complete" event.
// Sources live in metadata.citedPages, ordered by first citation appearance, so
// the 1-based position is the [n] marker used in the report.
func extractCitations(e client.Event) []client.Citation {
	var d struct {
		Metadata struct {
			CitedPages []struct {
				Title string `json:"title"`
				URL   string `json:"url"`
			} `json:"citedPages"`
		} `json:"metadata"`
	}
	if e.Decode(&d) != nil {
		return nil
	}

	var out []client.Citation
	for i, p := range d.Metadata.CitedPages {
		if p.URL == "" && p.Title == "" {
			continue
		}
		out = append(out, client.Citation{Number: i + 1, Title: p.Title, URL: p.URL})
	}
	return out
}

// extractMessage pulls a human message out of an error event payload, falling
// back to the raw data when there is no obvious field.
func extractMessage(e client.Event) string {
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

func newAutomateCmd() *cobra.Command {
	var (
		url           string
		dataSpec      string
		guardrails    string
		maxIter       int
		maxValidation int
		geo           string
		interactive   bool
	)

	cmd := &cobra.Command{
		Use:   "automate <task>",
		Short: "Run an AI browser-automation task (streams progress)",
		Long: "Execute a browser-automation task described in natural language.\n\n" +
			"The task runs server-side and streams progress events as it works:\n" +
			"planning, navigation, extraction, and a final answer. Use --data to\n" +
			"pass JSON context (literal, @file, or -) for form filling or complex\n" +
			"workflows.\n\n" +
			"Pass --interactive to let the task pause and request input mid-run; when\n" +
			"it does, respond with `tabstack agent input <request-id>`.",
		Args: exactArgsNamed("<task>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validGeo(geo); err != nil {
				return withCode(2, err)
			}
			// --url is optional; only check it when the caller supplied one.
			if url != "" {
				if err := validURL(url); err != nil {
					return withCode(2, err)
				}
			}
			if maxIter != 0 && (maxIter < 1 || maxIter > 100) {
				return withCode(2, fmt.Errorf("max iterations must be between 1 and 100 (--max-iterations got %d)", maxIter))
			}
			if maxValidation != 0 && (maxValidation < 1 || maxValidation > 10) {
				return withCode(2, fmt.Errorf("max validation attempts must be between 1 and 10 (--max-validation-attempts got %d)", maxValidation))
			}
			req := client.AutomateRequest{
				Task:                  args[0],
				Guardrails:            guardrails,
				MaxIterations:         maxIter,
				MaxValidationAttempts: maxValidation,
				URL:                   url,
				GeoTarget:             geoTarget(geo),
				Interactive:           interactive,
			}

			// --data is optional freeform JSON context.
			if dataSpec != "" {
				raw, err := readJSON(dataSpec)
				if err != nil {
					return withCode(2, err)
				}
				req.Data = raw
			}

			res, err := runStream(func(fn func(client.Event) error) error {
				return rootApp.client.Automate(context.Background(), req, fn)
			})
			if err != nil {
				return classifyError(err)
			}

			rootApp.renderer.PrintFinalAnswer(res.finalAnswer)
			rootApp.renderer.PrintCitations(res.citations)
			if res.failed {
				return withCode(3, failureError("automation", res.failMessage))
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&url, "url", "", "starting URL for the task")
	f.StringVar(&dataSpec, "data", "", "context as JSON: literal, @file, or - for stdin")
	f.StringVar(&guardrails, "guardrails", "", "safety constraints for execution")
	f.IntVar(&maxIter, "max-iterations", 0, "maximum task iterations (1-100)")
	f.IntVar(&maxValidation, "max-validation-attempts", 0, "maximum validation attempts (1-10)")
	f.StringVar(&geo, "geo", "", "geotarget country code (ISO 3166-1 alpha-2, e.g. GB)")
	f.BoolVar(&interactive, "interactive", false, "allow the task to pause and request input (answer with `agent input`)")

	return cmd
}

func newResearchCmd() *cobra.Command {
	var (
		mode         string
		fetchTimeout int
		nocache      bool
	)

	cmd := &cobra.Command{
		Use:   "research <query>",
		Short: "Run an AI research query over the web (streams progress)",
		Long: "Search the web, analyse sources, and synthesise an answer to a query.\n\n" +
			"Progress streams through phases as the research runs. --mode fast\n" +
			"(default) returns quick answers; balanced does deeper multi-source work.",
		Args: exactArgsNamed("<query>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validResearchMode(mode); err != nil {
				return withCode(2, err)
			}
			if err := checkLen("query", args[0], maxQueryLen); err != nil {
				return err
			}
			req := client.ResearchRequest{
				Query:        args[0],
				Mode:         client.ResearchMode(mode),
				FetchTimeout: fetchTimeout,
				NoCache:      nocache,
			}

			res, err := runStream(func(fn func(client.Event) error) error {
				return rootApp.client.Research(context.Background(), req, fn)
			})
			if err != nil {
				return classifyError(err)
			}

			rootApp.renderer.PrintFinalAnswer(res.finalAnswer)
			rootApp.renderer.PrintCitations(res.citations)
			if res.failed {
				return withCode(3, failureError("research", res.failMessage))
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&mode, "mode", "", "research mode: fast|balanced")
	f.IntVar(&fetchTimeout, "fetch-timeout", 0, "fetch timeout per page, in seconds")
	addNoCacheFlag(f, &nocache)

	return cmd
}

func newInputCmd() *cobra.Command {
	var dataSpec string

	cmd := &cobra.Command{
		Use:   "input <request-id>",
		Short: "Submit an input response to a running automation task",
		Long: "When an automation task pauses to ask for input, submit the response\n" +
			"with this command using the request ID from the automation stream.\n\n" +
			"The --data payload must be a JSON object:\n" +
			"  {\"fields\":[{\"ref\":\"field1\",\"value\":\"answer\"}]}  to provide values\n" +
			"  {\"cancelled\":true}                                to decline",
		Args: exactArgsNamed("<request-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataSpec == "" {
				return withCode(2, fmt.Errorf("the --data flag is required: a JSON object with \"fields\", or {\"cancelled\":true}"))
			}
			raw, err := readJSON(dataSpec)
			if err != nil {
				return withCode(2, err)
			}

			var req client.AutomateInputRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return withCode(2, fmt.Errorf("invalid --data: %w", err))
			}
			if len(req.Fields) == 0 && !req.Cancelled {
				return withCode(2, fmt.Errorf(
					"the --data payload must set \"fields\" (to submit values) or \"cancelled\":true (to decline); "+
						"got neither, unknown keys are ignored by the API",
				))
			}

			if err := rootApp.client.AutomateInput(context.Background(), args[0], req); err != nil {
				return classifyError(err)
			}

			if rootApp.renderer.Mode == ui.ModeJSON {
				out, _ := json.Marshal(map[string]bool{"submitted": true})
				fmt.Fprintln(rootApp.renderer.Out, string(out))
			} else {
				fmt.Fprintln(rootApp.renderer.Out, rootApp.renderer.Styles.Success.Render("input submitted"))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dataSpec, "data", "", "input payload as JSON: {\"fields\":[{\"ref\":\"...\",\"value\":\"...\"}]} or {\"cancelled\":true} (required)")
	return cmd
}

// failureError builds an error for an in-band stream failure, using the
// server's message when one was provided.
func failureError(kind, msg string) error {
	if msg != "" {
		return fmt.Errorf("%s failed: %s", kind, msg)
	}
	return fmt.Errorf("%s reported failure", kind)
}
