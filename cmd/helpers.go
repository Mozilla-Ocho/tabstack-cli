package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-isatty"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/schemas"
)

// resolveSchemaArg returns the JSON schema bytes for the schema-driven commands
// (`extract json`, `generate json`). The schema comes from either an inline
// --schema spec (literal, @file, or -) or a --schema-name reference into the
// local schema store; the two are mutually exclusive and exactly one is
// required. Returns coded errors so the caller can return them verbatim.
func resolveSchemaArg(schema, schemaName, storage string) (json.RawMessage, error) {
	switch {
	case schema != "" && schemaName != "":
		return nil, withCode(2, errors.New("--schema and --schema-name are mutually exclusive"))
	case schema == "" && schemaName == "":
		return nil, withCode(2, errors.New("required: --schema (literal, @file, or -) or --schema-name (a pulled schema)"))
	}

	if schemaName == "" {
		raw, err := readJSON(schema)
		if err != nil {
			return nil, withCode(2, err)
		}
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
			return 0, err
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

// classifyError turns a raw error into a coded one. API errors get code 3,
// everything else gets 1. Usage errors are handled by cobra before we get here.
func classifyError(err error) error {
	if err == nil {
		return nil
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
			return "", fmt.Errorf("read %s: %w", path, err)
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
