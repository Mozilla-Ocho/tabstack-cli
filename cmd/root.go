package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// version is overridable at build time with -ldflags "-X ...cmd.version=...".
var version = "dev"

// Version returns the build version stamped into the binary. main uses it to
// hand the version to fang, which renders it in styled help and --version.
func Version() string { return version }

// app is the shared context every subcommand leans on. It is populated once in
// the root command's PersistentPreRunE, so leaf commands never re-resolve
// config or rebuild the client. This is the small bit of glue that keeps each
// command file focused on just its own request.
type app struct {
	cfg      config.Config
	client   *client.Client
	renderer ui.Renderer
}

// persistent flag values, bound on the root command.
var (
	flagAPIKey  string
	flagBaseURL string
	flagOutput  string
	flagNoColor bool
	flagTimeout time.Duration
)

// rootApp holds the constructed context for the current invocation.
var rootApp *app

// NewRootCmd builds the root command tree. main.go calls Execute on it.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tabstack",
		Short: "Command-line client for the Tabstack AI API",
		Long: "tabstack is a CLI for the Tabstack AI API: browser automation, web " +
			"research, and structured extraction and generation from any URL.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		// Build the shared app context before any subcommand runs. We skip this
		// for commands that do not need an API client (auth, help, version) by
		// checking an annotation, so `auth login` works before a key exists.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Annotations["skipClient"] == "true" {
				return setupRendererOnly()
			}
			return setupApp()
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&flagAPIKey, "api-key", "", "API key (overrides env and config file)")
	pf.StringVar(&flagBaseURL, "base-url", "", "API base URL")
	pf.StringVarP(&flagOutput, "output", "o", "", "output format: pretty|json (default: pretty on a TTY, json when piped)")
	pf.BoolVar(&flagNoColor, "no-color", false, "disable coloured output")
	pf.DurationVar(&flagTimeout, "timeout", 0, "request timeout for non-streaming calls (e.g. 30s)")

	root.AddCommand(
		newAgentCmd(),
		newExtractCmd(),
		newGenerateCmd(),
		newSchemaCmd(),
		newAuthCmd(),
	)

	return root
}

// resolveMode decides the output mode. An explicit --output wins; otherwise we
// default to pretty when stdout is a terminal and json when it is piped, so
// `tabstack ... | jq` just works without a flag.
func resolveMode() (ui.OutputMode, error) {
	switch flagOutput {
	case "json":
		return ui.ModeJSON, nil
	case "pretty":
		return ui.ModePretty, nil
	case "":
		if isatty.IsTerminal(os.Stdout.Fd()) {
			return ui.ModePretty, nil
		}
		return ui.ModeJSON, nil
	default:
		return "", fmt.Errorf("invalid output format %q: must be one of: pretty, json", flagOutput)
	}
}

// newRenderer constructs the renderer from the resolved flags.
func newRenderer() (ui.Renderer, error) {
	mode, err := resolveMode()
	if err != nil {
		return ui.Renderer{}, err
	}
	return ui.Renderer{
		Out:    os.Stdout,
		Err:    os.Stderr,
		Mode:   mode,
		Styles: ui.NewStyles(flagNoColor),
	}, nil
}

// setupApp resolves config, validates the key, and builds the client. It is the
// pre-run for every command that talks to the API.
func setupApp() error {
	cfg, err := config.Resolve(flagAPIKey, flagBaseURL)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if cfg.APIKey == "" {
		// Missing key is a configuration error, not transient: exit 2 so agents
		// treat it as non-retryable rather than retrying with backoff (exit 1).
		return withCode(2, fmt.Errorf("no API key found. Set one with `tabstack auth login`, the %s environment variable, or --api-key", "TABSTACK_API_KEY"))
	}

	renderer, err := newRenderer()
	if err != nil {
		return withCode(2, err)
	}

	var opts []client.Option
	if flagTimeout > 0 {
		opts = append(opts, client.WithTimeout(flagTimeout))
	}

	rootApp = &app{
		cfg:      cfg,
		client:   client.New(cfg.APIKey, cfg.BaseURL, opts...),
		renderer: renderer,
	}
	return nil
}

// setupRendererOnly builds just the renderer for commands that do not need a
// client (auth status/login). Config is still resolved so `auth status` can
// report the key source.
func setupRendererOnly() error {
	cfg, err := config.Resolve(flagAPIKey, flagBaseURL)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	renderer, err := newRenderer()
	if err != nil {
		return withCode(2, err)
	}
	rootApp = &app{
		cfg:      cfg,
		renderer: renderer,
	}
	return nil
}
