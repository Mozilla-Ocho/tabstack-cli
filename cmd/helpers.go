package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/schemas"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// resolveSchemaArg returns the JSON schema bytes for the schema-driven commands
// (`extract json`, `generate json`). The schema comes from either an inline
// --schema spec (literal, @file, or -) or a --schema-name reference into the
// local schema store; the two are mutually exclusive and exactly one is
// required. Returns coded errors so the caller can return them verbatim.
func resolveSchemaArg(schema, schemaName, storage string) (json.RawMessage, error) {
	switch {
	case schema != "" && schemaName != "":
		return nil, withCode(2, errors.New("pass --schema or --schema-name, not both"))
	case schema == "" && schemaName == "":
		return nil, withCode(2, errors.New("required: --schema (literal, @file, or -) or --schema-name (a pulled schema)"))
	}

	if schemaName == "" {
		raw, err := readJSON(schema)
		if err != nil {
			return nil, withCode(2, err)
		}
		warnUnlikelySchema(warnWriter(), raw)
		return raw, nil
	}

	dir, err := schemaStoreDir(storage)
	if err != nil {
		return nil, withCode(1, err)
	}
	rel, err := schemas.FindLocal(dir, schemaName)
	if err != nil {
		return nil, withCode(2, err)
	}
	data, _, err := schemas.Read(dir, rel)
	if err != nil {
		return nil, withCode(1, err)
	}
	if !json.Valid(data) {
		return nil, withCode(2, fmt.Errorf("stored schema %s is not valid JSON", rel))
	}
	// Library schemas always carry a shape, but a locally edited copy might not,
	// so the stored path gets the same hint as an inline one.
	warnUnlikelySchema(warnWriter(), json.RawMessage(data))
	return json.RawMessage(data), nil
}

// schemaStoreDir resolves the schema store directory: the --storage override
// when set, otherwise the default config location.
func schemaStoreDir(storage string) (string, error) {
	if storage != "" {
		return storage, nil
	}
	return config.SchemasDir()
}

// errNotTerminal signals that an interactive prompt was requested but stdin is
// not a TTY. Callers convert this into a safe non-interactive default rather
// than blocking forever on input that will never arrive.
var errNotTerminal = errors.New("not a terminal")

// promptChoice asks the user to pick one of a set of single-letter options. keys
// is the set of accepted lowercase letters (e.g. "okq"); an empty line returns
// def. Prompts and re-prompts are written to stderr so stdout stays clean for
// piping. It returns errNotTerminal when stdin is not interactive.
func promptChoice(prompt, keys string, def byte) (byte, error) {
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return 0, errNotTerminal
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Fprint(os.Stderr, prompt)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			// EOF (Ctrl-D) or a read error with no input: treat as "couldn't get
			// an interactive answer", same as a non-TTY, so the caller maps it to
			// the exit-2 guidance path instead of a bare "EOF" at exit 1.
			return 0, errNotTerminal
		}
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" && def != 0 {
			return def, nil
		}
		if line != "" && strings.IndexByte(keys, line[0]) >= 0 {
			return line[0], nil
		}
		fmt.Fprintf(os.Stderr, "Please answer with one of [%s].\n", strings.Join(strings.Split(keys, ""), "/"))
	}
}

// promptLine reads one line of free-form input from an interactive stdin. It is
// the picker counterpart to promptChoice, for lists too long to give every entry
// a letter. Like promptChoice it prompts on stderr and returns errNotTerminal
// when there is nobody to ask.
func promptLine(prompt string) (string, error) {
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return "", errNotTerminal
	}
	reader := bufio.NewReader(os.Stdin)
	fmt.Fprint(os.Stderr, prompt)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", errNotTerminal
	}
	return strings.TrimSpace(line), nil
}

// envAny reports whether any of the named environment variables is set to a
// non-empty value.
func envAny(names ...string) bool {
	for _, n := range names {
		if os.Getenv(n) != "" {
			return true
		}
	}
	return false
}

// validateKeyFormat is a sanity check on an API key before it is stored: no
// embedded whitespace or quotes that would corrupt the config file, and long
// enough to be a real credential. It makes no API call.
func validateKeyFormat(key string) error {
	if strings.ContainsAny(key, "\"\n\r\t") {
		return fmt.Errorf("key contains invalid characters (newline, tab, or quote)")
	}
	if strings.TrimSpace(key) != key {
		return fmt.Errorf("key must not have leading or trailing whitespace")
	}
	if len(key) < 8 {
		return fmt.Errorf("key is too short to be valid (got %d characters)", len(key))
	}
	return nil
}

// exitErr wraps an error with a specific process exit code. main.go inspects
// for this so different failure classes map to different codes, which makes the
// CLI scriptable: 1 runtime/network, 2 usage (cobra default), 3 API error.
type exitErr struct {
	code int
	err  error
}

func (e *exitErr) Error() string { return e.err.Error() }
func (e *exitErr) Unwrap() error { return e.err }
func (e *exitErr) Code() int     { return e.code }

// withCode tags an error with an exit code.
func withCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return &exitErr{code: code, err: err}
}

// ErrInterrupted is the error a command returns when the user cancelled it.
// It is exported so main can render it as a plain line rather than fang's red
// ERROR box: Ctrl-C is the user getting what they asked for, not a failure to
// announce. It still exits 1, so scripts can tell it from success.
var ErrInterrupted = errors.New("cancelled")

// classifyError turns a raw error into a coded one. API errors get code 3,
// everything else gets 1. Usage errors are handled by cobra before we get here.
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	// Cancellation reaches here as a wrapped context.Canceled. The only thing
	// that cancels the root context is the signal handler, so this is Ctrl-C
	// or SIGTERM rather than an internal abort.
	if errors.Is(err, context.Canceled) {
		return withCode(1, ErrInterrupted)
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		return withCode(3, err)
	}
	if isTimeoutError(err) {
		return withCode(1, fmt.Errorf("request timed out. Check your network connection, or for non-streaming commands increase --timeout (e.g. --timeout 30s)"))
	}
	return withCode(1, err)
}

// validEffort returns an error if effort is set to an unrecognised value.
// An empty string is allowed (means "use server default").
func validEffort(effort string) error {
	switch effort {
	case "", "min", "standard", "max":
		return nil
	default:
		return fmt.Errorf("invalid effort %q: must be one of: min, standard, max", effort)
	}
}

// validGeo returns an error if geo is set to something that is not an ISO
// 3166-1 alpha-2 country code. The help text has always promised alpha-2, but
// nothing checked, so `--geo GBR` cost a round trip to find out. Empty means
// "no geotargeting" and is allowed.
func validGeo(geo string) error {
	geo = strings.TrimSpace(geo)
	if geo == "" {
		return nil
	}
	if utf8.RuneCountInString(geo) != 2 {
		return fmt.Errorf("invalid geo %q: use a two-letter ISO 3166-1 alpha-2 country code, e.g. GB", geo)
	}
	for _, r := range strings.ToUpper(geo) {
		if r < 'A' || r > 'Z' {
			return fmt.Errorf("invalid geo %q: use a two-letter ISO 3166-1 alpha-2 country code, e.g. GB", geo)
		}
	}
	return nil
}

// validURL checks a URL argument locally so an obvious typo fails immediately
// instead of costing a request that comes back as an opaque API 400. It only
// rejects what is certainly wrong: it must parse, carry an http or https
// scheme, and have a host. Anything past that is the server's call.
func validURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		if u.Scheme == "" {
			return fmt.Errorf("invalid url %q: missing scheme, try https://%s", raw, strings.TrimSpace(raw))
		}
		return fmt.Errorf("invalid url %q: scheme must be http or https, got %q", raw, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid url %q: missing host", raw)
	}
	return nil
}

// Positional-argument validators.
//
// These replace cobra.ExactArgs and friends throughout the tree for two
// reasons. Their messages name the argument rather than saying "accepts 1
// arg(s), received 0", and, more importantly, they carry exit code 2 on the
// error itself. main used to infer that code by string-matching Cobra's
// message prefixes, which is a contract-level risk on any Cobra bump: the exit
// codes are documented public behaviour.
//
// All of them avoid leading with cmd.CommandPath(), because fang title-cases
// the first word of a rendered error and would print "Tabstack extract ...".
// A format string starting with %s hides that from the copy lint, so it has to
// be watched by hand here.

// exactArgsNamed accepts exactly len(names) arguments.
func exactArgsNamed(names ...string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == len(names) {
			return nil
		}
		want := strings.Join(names, " ")
		if len(args) < len(names) {
			return withCode(2, fmt.Errorf("missing %s; run `%s --help`", want, cmd.CommandPath()))
		}
		return withCode(2, fmt.Errorf("too many arguments for `%s`: expected %s, got %d; quote the value if it contains spaces",
			cmd.CommandPath(), want, len(args)))
	}
}

// noArgsNamed accepts no positional arguments.
func noArgsNamed() cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return withCode(2, fmt.Errorf("`%s` takes no arguments, but got %q; run `%s --help`",
			cmd.CommandPath(), args[0], cmd.CommandPath()))
	}
}

// maxArgsNamed accepts at most n arguments, where name describes the optional
// one, e.g. "[key-id]".
func maxArgsNamed(n int, name string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) <= n {
			return nil
		}
		return withCode(2, fmt.Errorf("too many arguments for `%s`: expected at most %s, got %d; quote the value if it contains spaces",
			cmd.CommandPath(), name, len(args)))
	}
}

// unknownSubcommandArgs is the validator for commands that only group others.
// Cobra's default lets a stray argument fall through to the group, which then
// prints help and exits 0, so `tabstack extract nope` looked like success.
func unknownSubcommandArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	msg := fmt.Sprintf("unknown command %q for `%s`", args[0], cmd.CommandPath())
	if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
		msg += ". Did you mean: " + strings.Join(suggestions, ", ")
	}
	return withCode(2, errors.New(msg))
}

// applyGroupBehaviour walks the tree and makes every grouping command reject a
// stray argument with exit 2 instead of printing help and exiting 0.
//
// It has to set both Args and RunE, for two reasons that only show up in
// Cobra's internals. Find calls its own legacyArgs **only when Args is nil**,
// so setting Args is what stops the uncoded default error; and execute()
// returns flag.ErrHelp for a non-runnable command **before** it reaches
// ValidateArgs, so without a RunE the validator would never be consulted at
// all. Together they mean a group is runnable, validates, and still prints
// help when given nothing.
//
// The skipClient annotation comes along because ValidateArgs runs before
// PersistentPreRunE: on the no-argument help path the pre-run would otherwise
// fire and demand an API key just to show `tabstack extract --help` output.
// Annotations are not inherited, so leaves keep their own behaviour.
func applyGroupBehaviour(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		applyGroupBehaviour(sub)
	}
	if cmd.Runnable() || !cmd.HasSubCommands() || cmd.Args != nil {
		return
	}

	cmd.Args = unknownSubcommandArgs
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations["skipClient"] = "true"
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if len(args) == 0 {
			return c.Help()
		}
		return unknownSubcommandArgs(c, args)
	}
}

// minArgsNamed accepts n or more arguments, the variadic counterpart to
// exactArgsNamed.
func minArgsNamed(n int, name string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= n {
			return nil
		}
		return withCode(2, fmt.Errorf("missing %s; run `%s --help`", name, cmd.CommandPath()))
	}
}

// validResearchMode returns an error if mode is set to an unrecognised value.
// An empty string is allowed (means "use server default").
func validResearchMode(mode string) error {
	switch mode {
	case "", "fast", "balanced":
		return nil
	default:
		return fmt.Errorf("invalid mode %q: must be one of: fast, balanced", mode)
	}
}

// checkLen enforces a server-side character cap locally. It counts runes, not
// bytes, so multibyte input (CJK, emoji, accents) is measured the way the limit
// is documented. Returns a usage error (exit 2) when over the cap.
func checkLen(field, value string, max int) error {
	if n := utf8.RuneCountInString(value); n > max {
		return withCode(2, fmt.Errorf("%s exceeds the %d character limit (got %d)", field, max, n))
	}
	return nil
}

// isTimeoutError detects context deadline exceeded and net-level timeouts.
func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// readInput resolves a value that may be a literal string, an @file reference,
// or "-" for stdin. This is the same ergonomics curl uses for -d, and it keeps
// large JSON schemas out of the shell. An empty spec returns an empty string.
func readInput(spec string) (string, error) {
	switch {
	case spec == "":
		return "", nil
	case spec == "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(data), nil
	case strings.HasPrefix(spec, "@"):
		path := strings.TrimPrefix(spec, "@")
		data, err := os.ReadFile(path)
		if err != nil {
			// os.ReadFile's error already names the path, so wrapping with the
			// path again produced "read /x: open /x: no such file or directory".
			return "", fmt.Errorf("read input file: %w", err)
		}
		return string(data), nil
	default:
		return spec, nil
	}
}

// readJSON resolves an input spec and validates that it is well-formed JSON,
// returning it as json.RawMessage. We validate up front so a malformed schema
// fails locally with a clear message instead of as an opaque API 400.
func readJSON(spec string) (json.RawMessage, error) {
	raw, err := readInput(spec)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("expected JSON input but got nothing")
	}
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("input is not valid JSON")
	}
	return json.RawMessage(raw), nil
}

// geoTarget builds a *GeoTarget from a country flag, returning nil when unset
// so the field is omitted from the request body entirely.
func geoTarget(country string) *client.GeoTarget {
	if strings.TrimSpace(country) == "" {
		return nil
	}
	return &client.GeoTarget{Country: strings.ToUpper(country)}
}

// addNoCacheFlag registers the cache-bypass flag shared by the fetch-based
// commands. The canonical spelling is --no-cache, matching --no-color rather
// than sitting one hyphen away from it; --nocache was the original spelling and
// keeps working as a hidden alias so existing scripts do not break. Hiding it
// keeps one entry in help instead of two that mean the same thing, the same
// treatment --key gets against --api-key.
func addNoCacheFlag(f *pflag.FlagSet, p *bool) {
	f.BoolVar(p, "no-cache", false, "bypass the cache and fetch fresh")
	f.BoolVar(p, "nocache", false, "alias for --no-cache")
	_ = f.MarkHidden("nocache")
}

// validFetchFlags runs the local checks the fetch-based commands share, so
// `extract`, `generate`, and `automate` reject the same bad input the same way
// rather than each spending a round trip to find out.
func validFetchFlags(effort, geo string) error {
	if err := validEffort(effort); err != nil {
		return err
	}
	return validGeo(geo)
}

// fixedCompletions returns a completion func offering a static value set. Used
// for the enum flags (--effort, --mode, --output, --api-key-setup), whose
// legal values are already spelled out in their help text and validated
// locally, so leaving them on default file completion helped nobody.
func fixedCompletions(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeOrgs offers the organisations already in the local config, for --org
// and `auth switch`. It reads config only and never calls the management API:
// completion runs on every tab press, and --org is defined to resolve against
// local config anyway, so a network round trip here would be both slow and
// wrong. Names are offered alongside ids because both are valid selectors.
func completeOrgs(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	store, err := newStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := store.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, o := range orgRefsFromConfig(cfg) {
		if o.Name != "" {
			out = append(out, o.Name)
		}
		out = append(out, o.ID)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// confirmDestructive gates an irreversible remote action behind an explicit
// yes. what describes the action in the imperative ("revoke key abc123").
//
// The risk model was previously inverted: `schema pull` prompted before
// overwriting a local file you could re-pull in a second, while revoking a key
// (which breaks every service using it, with no undo) went through silently.
//
// Returns (false, nil) when the user declines, which callers treat as success
// and exit 0, matching how `schema pull`'s [q]uit already behaves. On a
// non-TTY, refusing is an exit-2 usage error naming --yes rather than a hang:
// there is nobody to ask, and silently proceeding is the whole problem.
func confirmDestructive(r uiRenderer, what string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	answer, err := promptChoice(
		fmt.Sprintf("About to %s. This cannot be undone. Continue? [y/N] ", what), "yn", 'n')
	if errors.Is(err, errNotTerminal) {
		return false, withCode(2, fmt.Errorf("cannot confirm on a non-interactive terminal; pass --yes to %s without prompting", what))
	}
	if err != nil {
		return false, withCode(1, err)
	}
	if answer != 'y' {
		fmt.Fprintln(r.Err, "Cancelled, nothing was changed.")
		return false, nil
	}
	return true, nil
}

// emitJSON writes v as one JSON object on stdout. The management and config
// commands render human text in pretty mode and call this in JSON mode, so
// `--output json` means the same thing everywhere instead of being a no-op on
// a third of the tree. The types passed in are declared structs, not ad-hoc
// maps, so the wire shape is reviewable and stable.
func emitJSON(r uiRenderer, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return withCode(1, err)
	}
	return r.PrintJSON(raw)
}

// jsonMode reports whether the caller asked for machine-readable output.
func jsonMode(r uiRenderer) bool { return r.Mode == ui.ModeJSON }

// schemaShapeKeywords are the JSON Schema keywords whose presence means the
// caller clearly intended a schema. Anything here suppresses the hint below.
//
// The set is deliberately wider than just "type" and "properties": a schema of
// {"$ref": "#/$defs/Job"}, {"enum": [...]}, or {"oneOf": [...]} is perfectly
// legitimate and carries neither, and a hint that cries wolf on valid input is
// worse than no hint. "$schema" is excluded on purpose, being metadata rather
// than structure, so {"$schema": "...", "title": "string"} is still caught.
var schemaShapeKeywords = []string{
	"type", "properties", "$ref", "allOf", "anyOf", "oneOf", "not",
	"enum", "const", "items", "prefixItems", "$defs", "definitions",
	"patternProperties", "additionalProperties",
}

// warnUnlikelySchema prints a hint when a supplied schema looks like example
// data rather than a JSON Schema. The classic first mistake is passing
// {"title": "string"}, describing the value wanted, where the API expects
// {"type": "object", "properties": {...}}, describing the shape. Without this
// the only feedback is an opaque API 400.
//
// It is deliberately a hint and not an error. Schemas are server-validated, and
// a local heuristic must never block a request that would have worked, so this
// writes to stderr, leaves stdout untouched, and cannot affect the exit code.
func warnUnlikelySchema(w io.Writer, raw json.RawMessage) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Not a JSON object. `true` and `false` are valid schemas, and anything
		// else is the server's call, so say nothing either way.
		return
	}
	for _, k := range schemaShapeKeywords {
		if _, ok := obj[k]; ok {
			return
		}
	}
	fmt.Fprintln(w, `hint: this schema has no "type" or "properties" key, so it may describe example`)
	fmt.Fprintln(w, `      values rather than a shape. A JSON Schema looks like`)
	fmt.Fprintln(w, `      {"type":"object","properties":{"title":{"type":"string"}}}, not {"title":"string"}.`)
	fmt.Fprintln(w, "      Ready-made schemas: `tabstack schema list`. Design guide:")
	fmt.Fprintln(w, "      https://github.com/Mozilla-Ocho/tabstack-schemas")
	fmt.Fprintln(w, "      Sending it as given; the server decides.")
}

// warnWriter is where advisory diagnostics go. It tolerates an unpopulated
// rootApp so helpers stay callable from tests that never build one.
func warnWriter() io.Writer {
	if rootApp == nil {
		return io.Discard
	}
	return rootApp.renderer.Err
}
