package cmd

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
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
	store    config.CredentialStore
	cfg      *config.Config
	key      config.KeyResolution
	client   *client.Client
	renderer ui.Renderer

	// orgOverride is the organisation id resolved from --org, empty when the
	// flag was not used. It is per-invocation only and never written to config.
	orgOverride string
}

// defaultTimeout bounds non-streaming calls so a wedged request eventually
// fails instead of hanging forever. It is deliberately generous: extract and
// generate against a heavy page are legitimately slow. Streaming calls are
// never bounded by it (see client.WithTimeout), and `--timeout 0` disables it.
const defaultTimeout = 120 * time.Second

// persistent flag values, bound on the root command.
var (
	flagAPIKey  string
	flagBaseURL string
	flagAuthURL string
	flagOrg     string
	flagOutput  string
	flagNoColor bool
	flagTimeout time.Duration
	flagDebug   bool
	flagRetries int
)

// rootApp holds the constructed context for the current invocation.
var rootApp *app

// uiRenderer is a local alias so the auth and keys commands can take a renderer
// without every helper signature reaching for the ui package.
type uiRenderer = ui.Renderer

// NewRootCmd builds the root command tree. main.go calls Execute on it.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tabstack",
		Short: "Command-line client for the Tabstack AI API",
		Long: "tabstack is a CLI for the Tabstack AI API: browser automation, web\n" +
			"research, and structured extraction and generation from any URL.\n" +
			"\n" +
			"New here? Run `tabstack auth login` first.\n" +
			"\n" +
			"CREDENTIALS\n" +
			"  Two, never interchangeable. A session (`auth login`) is user scoped and\n" +
			"  goes only to the console; an API key (`keys create`) is organisation\n" +
			"  scoped and is what product commands send. A key is resolved from, in\n" +
			"  order: --api-key, TABSTACK_API_KEY, the --org override's stored key,\n" +
			"  then the active organisation's.\n" +
			"\n" +
			"ENVIRONMENT\n" +
			"  TABSTACK_API_KEY       API key for product calls; overrides stored keys\n" +
			"  TABSTACK_BASE_URL      product API host (default https://api.tabstack.ai/v1)\n" +
			"  TABSTACK_AUTH_URL      auth and management host (default https://console.tabstack.ai)\n" +
			"  TABSTACK_OAUTH_SCOPES  override the scopes requested at login\n" +
			"  NO_COLOR               disable coloured output, same as --no-color\n" +
			"  XDG_CONFIG_HOME        where config.toml and pulled schemas live\n" +
			"\n" +
			"EXIT CODES\n" +
			"  0  success\n" +
			"  1  runtime or network error\n" +
			"  2  usage error, bad input, or missing configuration\n" +
			"  3  the API rejected the request, or a streamed task failed\n" +
			"\n" +
			"OUTPUT\n" +
			"  Pretty on a terminal, JSON when piped, so `tabstack ... | jq` needs no\n" +
			"  flag. Force it with --output. Streaming commands emit NDJSON, one event\n" +
			"  per line. Results go to stdout; progress, prompts, and warnings go to\n" +
			"  stderr.",
		Example:       "  # Sign in once, then check it works\n  tabstack auth login\n  tabstack extract markdown https://example.com\n\n  # Extract structured data using a schema from the public library\n  tabstack schema pull job-posting\n  tabstack extract json https://example.com/jobs/1 --schema-name job-posting\n\n  # Output is JSON automatically when piped, so jq just works\n  tabstack extract markdown https://example.com | jq -r .content",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		// Build the shared app context before any subcommand runs. We skip the
		// product client for commands that do not need one (auth, keys, schema,
		// help, version) by checking an annotation, so `auth login` works before
		// any credential exists.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Annotations["skipClient"] == "true" {
				return setupRendererOnly()
			}
			return setupApp()
		},
	}

	// Only the two flags every command genuinely uses live on the root. The
	// rest are registered per subtree by the addXFlags helpers below, so help
	// for `schema list` or `config show` stops advertising --api-key and
	// --timeout, which those commands never read. See addProductFlags.
	pf := root.PersistentFlags()
	pf.StringVarP(&flagOutput, "output", "o", "", "output format: pretty|json (default: pretty on a TTY, json when piped)")
	pf.BoolVar(&flagNoColor, "no-color", false, "disable coloured output")
	// --debug stays global even though only product-host calls are instrumented.
	// Cobra decides whether a flag consumes the next argument while it is still
	// looking for the subcommand, and it can only do that for flags it already
	// knows. Registering this boolean per subtree made `tabstack --debug extract
	// markdown URL` swallow "extract" as the flag's value and fail with
	// "Unknown command markdown". Valued flags such as --base-url survive that
	// position by luck; booleans cannot.
	pf.BoolVar(&flagDebug, "debug", false, "print request id, timing, and rate-limit headers to stderr for each API call")
	_ = root.RegisterFlagCompletionFunc("output", fixedCompletions("pretty", "json"))

	agent, extract, generate := newAgentCmd(), newExtractCmd(), newGenerateCmd()
	auth, keys, cfgCmd, mcp := newAuthCmd(), newKeysCmd(), newConfigCmd(), newMCPCmd()

	// Product-host commands: they build a client, so they need a credential, a
	// base URL, a timeout, and can be traced. --org selects which stored key
	// goes out, which only means anything when a key is being sent.
	for _, c := range []*cobra.Command{agent, extract, generate} {
		addProductFlags(c)
		addOrgFlag(c)
	}

	// Auth-host commands talk to the console, so they take --auth-url. keys
	// also takes --org (`keys create --org acme`); under auth only login reads
	// it, so it is registered there rather than on the whole group.
	addAuthHostFlag(auth)
	addAuthHostFlag(keys)
	addOrgFlag(keys)

	// config show prints both hosts as they would resolve, so it accepts both
	// overrides to preview them. It sends nothing anywhere.
	addBaseURLFlag(cfgCmd)
	addAuthHostFlag(cfgCmd)

	// mcp straddles both hosts: product tools use the API key, management
	// tools use the session. It resolves the key without an --org override
	// (see resolveMCPKey), so it does not take that flag.
	addProductFlags(mcp)
	addAuthHostFlag(mcp)

	// schema is deliberately absent: it talks only to raw.githubusercontent.com
	// and the local store, so none of these apply to it.
	root.AddCommand(agent, extract, generate, newSchemaCmd(), auth, keys, cfgCmd, mcp)

	return root
}

// addProductFlags registers the flags that only mean something when a command
// builds a product-API client.
func addProductFlags(cmd *cobra.Command) {
	pf := cmd.PersistentFlags()
	pf.StringVar(&flagAPIKey, "api-key", "", "key for the product API (overrides env and stored keys)")
	pf.StringVar(&flagAPIKey, "key", "", "alias for --api-key")
	pf.DurationVar(&flagTimeout, "timeout", defaultTimeout, "request timeout for non-streaming calls; 0 disables (e.g. 30s)")
	pf.IntVar(&flagRetries, "retries", client.DefaultRetries, "retry transient failures (408, 409, 429, 5xx) this many times; 0 disables")
	// --key is the documented short form in the credential precedence; keep the
	// help output to one entry rather than two that mean the same thing.
	_ = pf.MarkHidden("key")
	addBaseURLFlag(cmd)
}

// addBaseURLFlag registers --base-url on its own, for config show, which
// displays the resolved product host without ever calling it.
func addBaseURLFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&flagBaseURL, "base-url", "", "product API base URL")
}

// addAuthHostFlag registers --auth-url for commands that reach the console.
func addAuthHostFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&flagAuthURL, "auth-url", "", "auth and management host URL")
}

// addOrgFlag registers the one-shot organisation override.
func addOrgFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&flagOrg, "org", "", "act as this organisation for one command (id, name, or unique prefix)")
	_ = cmd.RegisterFlagCompletionFunc("org", completeOrgs)
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

// setupApp loads config, resolves the product credential, and builds the client.
// It is the pre-run for every command that talks to the product API.
func setupApp() error {
	base, err := setupBase()
	if err != nil {
		return err
	}

	if flagOrg != "" {
		id, err := resolveOrgLocal(base.cfg, flagOrg)
		if err != nil {
			return withCode(2, err)
		}
		base.orgOverride = id
	}

	res, err := base.cfg.ResolveAPIKey(config.KeyRequest{Flag: flagAPIKey, OrgOverride: base.orgOverride})
	if err != nil {
		// A missing or unusable credential is a configuration error, not a
		// transient one: exit 2 so agents treat it as non-retryable rather than
		// retrying with backoff (exit 1).
		if errors.Is(err, config.ErrNoAPIKey) {
			return withCode(2, fmt.Errorf("no API key found. Run `tabstack auth login`, or set %s, or pass --api-key", config.EnvAPIKey))
		}
		return withCode(2, err)
	}
	base.key = res

	var opts []client.Option
	if flagTimeout > 0 {
		opts = append(opts, client.WithTimeout(flagTimeout))
	}
	opts = append(opts, client.WithRetries(flagRetries))
	if sink := retrySink(base.renderer, flagDebug); sink != nil {
		opts = append(opts, client.WithRetryNotify(sink))
	}
	if flagDebug {
		opts = append(opts, client.WithDebug(debugSink(base.renderer, nil)))
	}
	base.client = client.New(res.APIKey, base.cfg.ResolveBaseURL(flagBaseURL), opts...)

	// When --org is in play, say which organisation we are acting as. It goes to
	// stderr so it shows up in logs and terminals without contaminating piped
	// stdout.
	if base.orgOverride != "" {
		fmt.Fprintf(base.renderer.Err, "acting as organisation %s (%s)\n",
			base.cfg.OrgName(base.orgOverride), base.orgOverride)
	}

	rootApp = base
	return nil
}

// setupRendererOnly builds the renderer and loads config for commands that do
// not need a product client (auth, keys, schema). No credential is required, so
// these still work on a fresh install.
func setupRendererOnly() error {
	base, err := setupBase()
	if err != nil {
		return err
	}
	rootApp = base
	return nil
}

// setupBase does the work common to both setups: renderer, credential store,
// and config load.
func setupBase() (*app, error) {
	renderer, err := newRenderer()
	if err != nil {
		return nil, withCode(2, err)
	}

	store, err := newStore()
	if err != nil {
		return nil, fmt.Errorf("locate configuration: %w", err)
	}
	cfg, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}

	return &app{store: store, cfg: cfg, renderer: renderer}, nil
}

// newStore builds the credential store. It is a package var so tests can point
// the whole command tree at a temporary config file.
var newStore = func() (config.CredentialStore, error) {
	return config.NewFileStore()
}

// consoleClient builds a client for the auth host with the current session
// attached. Every command that talks to /cli/* goes through this so the session
// is refreshed and persisted in one place.
func consoleClient() (*console.Client, *console.SessionManager) {
	c := console.New(rootApp.cfg.ResolveAuthURL(flagAuthURL))
	sm := c.AttachSession(rootApp.store, rootApp.cfg)
	return c, sm
}

// requireSession returns a console client only when a session is stored, so
// commands can fail with the login hint before making a request.
func requireSession() (*console.Client, *console.SessionManager, error) {
	if rootApp.cfg.Session == nil || rootApp.cfg.Session.AccessToken == "" {
		return nil, nil, withCode(2, errors.New("not signed in. Run: tabstack auth login"))
	}
	c, sm := consoleClient()
	return c, sm, nil
}
