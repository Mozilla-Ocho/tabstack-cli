package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/pflag"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// defaultConcurrency is deliberately modest. The batch commands exist for CI,
// where several invocations may already be running against the same rate limit,
// so the default should not itself be the thing that trips one.
const defaultConcurrency = 4

// maxOutputNameBase caps the readable part of a generated filename, leaving
// room for the hash and extension inside every filesystem's limit.
const maxOutputNameBase = 100

// stdinReader is where a "-" URL list is read from. A package var so the whole
// path is testable without a real pipe, matching how login.go makes openBrowser
// swappable.
var stdinReader io.Reader = os.Stdin

// batchError is a per-URL failure, carrying the exit code the same failure
// would have produced on its own so a consumer can tell an API rejection from
// a network problem without parsing the message.
type batchError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// batchItem is one URL's outcome. Result is the command's normal JSON payload;
// body is what gets written to a file under --output-dir, which differs by
// command (the Markdown body, or the JSON itself).
type batchItem struct {
	URL    string          `json:"url"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *batchError     `json:"error,omitempty"`

	body []byte
	// file is where the result was written, when --output-dir was used.
	file string
}

// fetchFunc performs one URL's request. It returns the JSON payload for the
// envelope and the bytes to write to a file, which are the same thing for
// schema-shaped output but not for Markdown.
type fetchFunc func(ctx context.Context, target string) (result json.RawMessage, body []byte, err error)

// resolveURLs expands the positional arguments into the list to fetch.
//
// A bare "-" reads a newline-delimited list from stdin, so a batch can be
// piped in. It may appear at most once and is expanded in place, so mixing it
// with literal URLs works. stdinTaken names a flag already reading stdin, if
// any: only one thing per invocation can, and silently picking a winner would
// be worse than refusing.
func resolveURLs(args []string, stdinTaken string) ([]string, error) {
	seenDash := false
	var out []string

	for _, a := range args {
		if a != "-" {
			out = append(out, a)
			continue
		}
		if seenDash {
			return nil, withCode(2, errors.New("the URL list can only be read from stdin once, but - was given twice"))
		}
		seenDash = true
		if stdinTaken != "" {
			return nil, withCode(2, fmt.Errorf(
				"cannot read both the URL list and %s from stdin; pass %s as a literal string or @file",
				stdinTaken, stdinTaken))
		}
		lines, err := readURLList(stdinReader)
		if err != nil {
			return nil, withCode(1, err)
		}
		if len(lines) == 0 {
			return nil, withCode(2, errors.New("no URLs on stdin"))
		}
		out = append(out, lines...)
	}

	// Duplicates would fetch the same page twice and, under --output-dir, race
	// two writers onto one filename, since names are a function of the URL.
	return dedupe(out), nil
}

// readURLList parses a newline-delimited URL list. Blank lines and comments are
// skipped so a list can be checked into a repo with notes in it; a URL cannot
// begin with "#", so the comment rule is unambiguous.
func readURLList(r io.Reader) ([]string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read URL list from stdin: %w", err)
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

// dedupe removes repeats, keeping first-seen order.
func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// validateURLs checks every URL before any request goes out. Discovering a typo
// after forty paid requests is worse than refusing the whole batch up front,
// and it means a run can only ever end 0 or 3, never 2.
func validateURLs(urls []string) error {
	for _, u := range urls {
		if err := validURL(u); err != nil {
			return withCode(2, err)
		}
	}
	return nil
}

// runBatch fetches every URL with at most concurrency requests in flight,
// calling onResult in **input order** as results become available.
//
// Input order rather than completion order: these commands are aimed at CI,
// where output gets diffed, and nondeterministic line order makes that
// painful. Emitting from a sliding window rather than collecting everything
// first means a long batch still reports progress as it goes.
func runBatch(ctx context.Context, urls []string, concurrency int, fetch fetchFunc, after func(*batchItem), onResult func(batchItem)) []batchItem {
	if concurrency < 1 {
		concurrency = 1
	}

	items := make([]batchItem, len(urls))
	done := make([]bool, len(urls))
	next := 0

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for i, target := range urls {
		wg.Add(1)
		go func(i int, target string) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				// Cancelled before this one started; record it so the summary
				// accounts for every URL.
				mu.Lock()
				items[i] = batchItem{URL: target, Error: &batchError{Code: 1, Message: ctx.Err().Error()}}
				done[i] = true
				flushReady(items, done, &next, onResult)
				mu.Unlock()
				return
			}
			defer func() { <-sem }()

			item := batchItem{URL: target}
			result, body, err := fetch(ctx, target)
			if err != nil {
				item.Error = newBatchError(err)
			} else {
				item.OK = true
				item.Result = result
				item.body = body
			}
			// Runs unlocked and in the worker, so files are written as each
			// result lands rather than behind a slow predecessor.
			if after != nil {
				after(&item)
			}

			mu.Lock()
			items[i] = item
			done[i] = true
			flushReady(items, done, &next, onResult)
			mu.Unlock()
		}(i, target)
	}

	wg.Wait()
	return items
}

// flushReady emits every finished item from next onwards. The caller holds the
// lock, which also serialises onResult so writers never interleave.
func flushReady(items []batchItem, done []bool, next *int, onResult func(batchItem)) {
	for *next < len(items) && done[*next] {
		onResult(items[*next])
		*next++
	}
}

// newBatchError records a per-URL failure with the exit code that failure would
// have carried on its own.
func newBatchError(err error) *batchError {
	return &batchError{Code: codeForError(err), Message: err.Error()}
}

// codeForError extracts the exit code a single-URL run would have used.
func codeForError(err error) int {
	coded := classifyError(err)
	var e *exitErr
	if errors.As(coded, &e) {
		return e.Code()
	}
	return 1
}

// outputFileName derives a stable filename from a URL.
//
// The hash is always appended, not only on collision, so a name is a pure
// function of its URL. Adding a URL to a list therefore never renames the
// files belonging to the others, which is what makes repeat runs in CI
// idempotent. The readable prefix is there so a directory listing is legible.
func outputFileName(rawURL, ext string) string {
	sum := sha256.Sum256([]byte(rawURL))
	hash := hex.EncodeToString(sum[:])[:8]

	base := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		base = u.Host + u.Path
	}
	base = sanitiseFileName(base)
	if base == "" {
		base = "url"
	}
	if len(base) > maxOutputNameBase {
		base = strings.Trim(base[:maxOutputNameBase], "-_.")
	}
	return base + "-" + hash + ext
}

// sanitiseFileName reduces a host and path to something safe on every
// filesystem: lowercase, path separators to underscores, anything else outside
// a conservative set to a dash, with runs collapsed.
func sanitiseFileName(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
			lastDash = false
		case r == '/':
			b.WriteRune('_')
			lastDash = false
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-_.")
}

// writeOutputFile writes one result. An existing file is refused rather than
// clobbered unless --force is given, matching what --force already means on
// `schema pull`: overwrite a local file.
func writeOutputFile(dir, rawURL, ext string, body []byte, force bool) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}
	path := filepath.Join(dir, outputFileName(rawURL, ext))

	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("refusing to overwrite %s; pass --force to replace it", path)
		}
	}
	// Results are documents, not secrets, so 0644 like the schema store rather
	// than the 0600 the config file gets.
	if err := os.WriteFile(path, withTrailingNewline(body), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// withTrailingNewline gives a written file exactly one, so the output is a
// well-formed text file. Matches Renderer.PrintRaw.
func withTrailingNewline(body []byte) []byte {
	trimmed := strings.TrimRight(string(body), "\n")
	if trimmed == "" {
		return nil
	}
	return []byte(trimmed + "\n")
}

// emitBatchItem renders one result. JSON mode emits the NDJSON envelope; pretty
// mode prints a styled URL header followed by whatever prettyBody returns, or a
// styled failure line.
func emitBatchItem(r uiRenderer, item batchItem, prettyBody func(batchItem) string) {
	if r.Mode == ui.ModeJSON {
		raw, err := json.Marshal(item)
		if err != nil {
			fmt.Fprintf(r.Err, "could not encode the result for %s: %v\n", item.URL, err)
			return
		}
		fmt.Fprintln(r.Out, string(raw))
		return
	}

	fmt.Fprintln(r.Out, r.Styles.Label.Render(item.URL))
	if !item.OK {
		fmt.Fprintf(r.Out, "%s %s\n\n", r.Styles.ErrorTag.Render("!"), item.Error.Message)
		return
	}
	if item.file != "" {
		fmt.Fprintf(r.Out, "%s %s\n\n", r.Styles.Success.Render("✓"), item.file)
		return
	}
	if body := prettyBody(item); body != "" {
		fmt.Fprintln(r.Out, body)
	}
	fmt.Fprintln(r.Out)
}

// batchOutcome summarises a run and returns the process exit code: 0 only when
// every URL succeeded, otherwise 3, with successful results already written.
//
// One code for "the run did not fully succeed" rather than distinguishing
// network from API failures: the per-URL detail is already in the output, and
// two codes for partial failure would be harder to branch on than one.
func batchOutcome(r uiRenderer, items []batchItem) error {
	var failed []batchItem
	for _, item := range items {
		if !item.OK {
			failed = append(failed, item)
		}
	}
	if len(failed) == 0 {
		return nil
	}

	// Only the per-URL detail is printed here. The count is the returned
	// error, which the caller renders, so the two do not say the same thing
	// twice.
	fmt.Fprintln(r.Err, "\nfailed URLs:")
	for _, item := range failed {
		fmt.Fprintf(r.Err, "  %s  %s\n", item.URL, item.Error.Message)
	}
	return withCode(3, fmt.Errorf("%d of %d URLs failed", len(failed), len(items)))
}

// batchOptions groups the knobs the batch runner needs from a command.
type batchOptions struct {
	concurrency int
	outputDir   string
	ext         string
	force       bool
}

// addBatchFlags registers the flags shared by the batch-capable commands.
func addBatchFlags(f *pflag.FlagSet, concurrency *int, outputDir *string, batch, force *bool) {
	f.IntVar(concurrency, "concurrency", defaultConcurrency, "how many URLs to fetch at once")
	f.StringVar(outputDir, "output-dir", "", "write one file per URL into this directory instead of stdout")
	f.BoolVar(batch, "batch", false, "always use the per-URL envelope, even for a single URL, so the output shape is stable")
	f.BoolVar(force, "force", false, "overwrite existing files in --output-dir")
}

// checkRawBatch rejects the one combination that has no sensible meaning:
// concatenating several documents onto stdout with no separator between them.
func checkRawBatch(raw bool, count int, outputDir string) error {
	if raw && count > 1 && outputDir == "" {
		return withCode(2, errors.New(
			"cannot use --raw with more than one URL on stdout, because the documents would run together; pass --output-dir to write one file per URL"))
	}
	return nil
}

// runExtractBatch is the shared tail of the batch-capable commands: fetch
// everything, write or print each result as it becomes ready, then summarise.
func runExtractBatch(ctx context.Context, urls []string, opts batchOptions, fetch fetchFunc, prettyBody func(batchItem) string) error {
	r := rootApp.renderer

	// Files are written by the worker as each result lands, so a slow URL
	// early in the list does not hold back everyone else's output.
	write := func(item *batchItem) {
		if opts.outputDir == "" || !item.OK {
			return
		}
		path, err := writeOutputFile(opts.outputDir, item.URL, opts.ext, item.body, opts.force)
		if err != nil {
			item.OK = false
			item.Error = &batchError{Code: 1, Message: err.Error()}
			return
		}
		item.file = path
	}

	items := runBatch(ctx, urls, opts.concurrency, fetch, write, func(item batchItem) {
		emitBatchItem(r, item, prettyBody)
	})
	return batchOutcome(r, items)
}

// indentJSON pretty-prints a result for the human-facing batch output. Invalid
// JSON is passed through rather than swallowed: the caller's schema decides the
// shape, and this is display only.
func indentJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}
