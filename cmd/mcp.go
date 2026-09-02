package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	tsmcp "github.com/Mozilla-Ocho/tabstack-cli/internal/mcp"
)

// newMCPCmd builds the `tabstack mcp` command: a local Model Context Protocol
// server that exposes the product API as tools over stdio.
//
// It carries the skipClient annotation so the pre-run does not require a
// resolvable API key (this command tolerates a missing key and can mint one
// from the session). stdio is the JSON-RPC transport, so nothing here may write
// to stdout; the shared renderer is unused and all diagnostics go to stderr.
func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run a local MCP server exposing Tabstack tools over stdio",
		Long: "Run a local Model Context Protocol server over stdio, exposing Tabstack\n" +
			"as tools an MCP client (Claude Desktop, an IDE, etc.) can call: extract,\n" +
			"generate, automate, research, the local schema store, and read-only\n" +
			"account context.\n\n" +
			"Product calls use the org-scoped API key (resolved like every other\n" +
			"command); if none is stored but you are signed in, one is created for the\n" +
			"active organisation on startup. Sign in first with `tabstack auth login`,\n" +
			"or set TABSTACK_API_KEY for a non-interactive setup.\n\n" +
			"stdout carries the JSON-RPC protocol; logs go to stderr.",
		Example:      "  # Run the server (an MCP client normally launches this for you)\n  tabstack mcp\n\n  # Claude Desktop / IDE config entry:\n  #   \"tabstack\": { \"command\": \"tabstack\", \"args\": [\"mcp\"] }",
		Args:         cobra.NoArgs,
		Annotations:  map[string]string{"skipClient": "true"},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCP(cmd.Context())
		},
	}
}

// runMCP resolves credentials, builds the server, and serves stdio until the
// client disconnects or the process is signalled.
func runMCP(ctx context.Context) error {
	apiKey, err := resolveMCPKey(ctx)
	if err != nil {
		return err
	}

	var opts []client.Option
	if flagTimeout > 0 {
		opts = append(opts, client.WithTimeout(flagTimeout))
	}
	opts = append(opts, client.WithRetries(flagRetries))
	// Diagnostics go to stderr, which is safe here: the stdio invariant only
	// reserves stdout for JSON-RPC frames.
	if flagDebug {
		opts = append(opts, client.WithDebug(debugSink(rootApp.renderer, nil)))
	}
	product := client.New(apiKey, rootApp.cfg.ResolveBaseURL(flagBaseURL), opts...)

	// Console client for the management tools. It may be session-less, in which
	// case those tools return a sign-in error; the product tools still work.
	cons, _ := consoleClient()

	schemasDir, err := config.SchemasDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tabstack mcp: schema store unavailable (schema tools disabled): %v\n", err)
		schemasDir = ""
	}

	srv := tsmcp.NewServer(tsmcp.Deps{
		Product:    product,
		Console:    cons,
		SchemasDir: schemasDir,
		ActiveOrg:  rootApp.cfg.ActiveOrg,
		Version:    version,
	})

	// Stop cleanly when the host signals us.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "tabstack mcp: serving on stdio (version %s)\n", version)
	if err := srv.Run(ctx, &sdk.StdioTransport{}); err != nil && !isCleanShutdown(err) {
		return withCode(1, err)
	}
	return nil
}

// isCleanShutdown reports whether a Run error is just the client going away
// rather than a real failure: a signal (context.Canceled), a closed stdin
// (io.EOF), or the SDK's graceful jsonrpc2 "server is closing" (code -32004,
// which wraps EOF with %v so errors.Is cannot see it, and lives in an internal
// package we cannot import).
func isCleanShutdown(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return true
	}
	return strings.Contains(err.Error(), "server is closing")
}

// resolveMCPKey returns the product API key to serve with. It resolves like
// every other command, but tolerates a missing key: if none is configured and a
// session exists, it mints one for the active organisation and stores it, so
// the next startup skips this step. A missing key with no session is a
// configuration error the user must fix.
func resolveMCPKey(ctx context.Context) (string, error) {
	res, err := rootApp.cfg.ResolveAPIKey(config.KeyRequest{Flag: flagAPIKey})
	if err == nil {
		return res.APIKey, nil
	}
	if !errors.Is(err, config.ErrNoAPIKey) {
		return "", withCode(2, err)
	}

	if rootApp.cfg.Session == nil || rootApp.cfg.Session.AccessToken == "" {
		return "", withCode(2, fmt.Errorf(
			"no API key and not signed in. Run `tabstack auth login`, or set %s", config.EnvAPIKey))
	}
	orgID := rootApp.cfg.ActiveOrg
	if orgID == "" {
		return "", withCode(2, errors.New(
			"signed in but no active organisation. Run `tabstack auth switch <org>`"))
	}

	cons, _ := consoleClient()
	fmt.Fprintf(os.Stderr, "tabstack mcp: no API key stored for %s, creating one\n",
		rootApp.cfg.OrgName(orgID))
	key, err := cons.CreateAPIKey(ctx, orgID, defaultKeyName())
	if err != nil {
		return "", classifyConsoleError(err)
	}
	if key.APIKey == "" {
		return "", withCode(3, errors.New("the console created a key but returned no plaintext"))
	}

	org := rootApp.cfg.UpsertOrg(orgID, "")
	org.APIKey = key.APIKey
	org.APIKeyID = key.ID
	org.APIKeyName = key.Name
	if err := rootApp.store.Save(rootApp.cfg); err != nil {
		return "", withCode(1, fmt.Errorf("save config: %w", err))
	}
	return key.APIKey, nil
}
