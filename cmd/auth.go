package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/term"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// newAuthCmd groups credential management. These commands carry the skipClient
// annotation so the root pre-run does not require a key before one exists,
// which is what lets `auth login` work on a fresh install.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage your Tabstack session and API credentials",
	}
	cmd.AddCommand(
		newAuthLoginCmd(),
		newAuthLogoutCmd(),
		newAuthStatusCmd(),
		newAuthSessionsCmd(),
		newAuthSwitchCmd(),
	)
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var (
		keyFlag      string
		keySetupFlag string
		noKeyFlag    bool
		orgFlag      string
		noBrowser    bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in through your browser and set up an API key",
		Long: "Signs in to the Tabstack console in your browser (OAuth 2.1 with PKCE) " +
			"and stores the resulting session. It then offers to create a new API key, " +
			"adopt an existing one, or do nothing.\n\n" +
			"Use --key to store an API key directly without signing in.",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			// --key keeps the original paste-a-key path working with no server call.
			if keyFlag != "" {
				return saveKeyDirectly(keyFlag)
			}

			mode, err := resolveKeySetupMode(keySetupFlag, noKeyFlag)
			if err != nil {
				return withCode(2, err)
			}

			r := rootApp.renderer
			authURL := rootApp.cfg.AuthURL
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			result, err := console.Login(ctx, authURL, console.DefaultScopes, orgFlag, !noBrowser)
			if err != nil {
				return withCode(1, err)
			}

			fc, err := config.LoadFile()
			if err != nil {
				return withCode(1, err)
			}
			fc.AuthURL = authURL
			fc.BaseURL = rootApp.cfg.BaseURL
			applyToken(&fc, result.Token)

			// The org chosen on the consent screen comes back from /cli/me.
			api := console.NewClient(authURL, result.Token.AccessToken)
			me, err := api.Me(ctx)
			if err != nil {
				return withCode(1, fmt.Errorf("signed in, but could not read your account: %w", err))
			}
			fc.DefaultOrg = me.DefaultOrg

			if err := config.SaveFile(fc); err != nil {
				return withCode(1, fmt.Errorf("save config: %w", err))
			}

			fmt.Fprintf(r.Out, "%s signed in as %s\n", r.Styles.Success.Render("✓"), me.User.Email)
			if org := findOrg(me.Organizations, me.DefaultOrg); org != nil {
				fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("organization:"), org.Name)
			}
			warnMismatchedHosts(r, authURL, rootApp.cfg.BaseURL)

			if err := setupAPIKey(ctx, api, &fc, mode); err != nil {
				return withCode(1, err)
			}
			if path, err := config.ConfigPath(); err == nil {
				fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("config:"), path)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&keyFlag, "key", "", "store this API key directly and skip signing in")
	cmd.Flags().StringVar(&keySetupFlag, "api-key-setup", "", "non-interactive key setup: create|existing|skip")
	cmd.Flags().StringVar(&keyIDSelection, "api-key-id", "", "with --api-key-setup=existing, adopt this key id")
	cmd.Flags().BoolVar(&noKeyFlag, "no-key", false, "sign in only; do not set up an API key")
	cmd.Flags().StringVar(&orgFlag, "org", "", "preselect this organization on the consent screen")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening a browser")
	return cmd
}

// keySetupMode is what `auth login` should do about a product API key once the
// session exists.
type keySetupMode string

const (
	keySetupPrompt   keySetupMode = "prompt"
	keySetupCreate   keySetupMode = "create"
	keySetupExisting keySetupMode = "existing"
	keySetupSkip     keySetupMode = "skip"
)

func resolveKeySetupMode(flagValue string, noKey bool) (keySetupMode, error) {
	if noKey {
		return keySetupSkip, nil
	}
	switch flagValue {
	case "":
		// Only prompt when there is a human to answer.
		if isatty.IsTerminal(os.Stdin.Fd()) {
			return keySetupPrompt, nil
		}
		return keySetupSkip, nil
	case "create":
		return keySetupCreate, nil
	case "existing":
		return keySetupExisting, nil
	case "skip":
		return keySetupSkip, nil
	default:
		return "", fmt.Errorf("invalid --api-key-setup %q: must be create, existing, or skip", flagValue)
	}
}

// setupAPIKey handles the create / adopt / skip decision after sign-in. It keeps
// a key the CLI already holds for this org rather than piling up new ones.
func setupAPIKey(ctx context.Context, api *console.Client, fc *config.File, mode keySetupMode) error {
	r := rootApp.renderer

	if mode == keySetupSkip {
		fmt.Fprintln(r.Out, r.Styles.Muted.Render("No API key set up. Run `tabstack keys create` when you need one."))
		return nil
	}
	if fc.DefaultOrg == "" {
		fmt.Fprintln(r.Out, r.Styles.Muted.Render("No organization selected, so no API key was set up."))
		return nil
	}

	existing, err := api.ListAPIKeys(ctx, fc.DefaultOrg)
	if err != nil {
		return fmt.Errorf("list API keys: %w", err)
	}

	// Already holding a key that still exists server-side: leave it alone.
	if fc.APIKey != "" && fc.APIKeyID != "" && containsKey(existing, fc.APIKeyID) {
		fmt.Fprintf(r.Out, "%s keeping the API key already configured for this organization\n",
			r.Styles.Success.Render("✓"))
		return nil
	}

	if mode == keySetupPrompt {
		mode, err = promptKeySetup(len(existing) > 0)
		if err != nil {
			return err
		}
		if mode == keySetupSkip {
			fmt.Fprintln(r.Out, r.Styles.Muted.Render("No API key set up. Run `tabstack keys create` when you need one."))
			return nil
		}
	}

	if mode == keySetupExisting {
		if len(existing) == 0 {
			return errors.New("this organization has no API keys to adopt; use --api-key-setup=create")
		}
		chosen, err := pickExistingKey(existing)
		if err != nil {
			return err
		}
		secret, err := api.RevealAPIKey(ctx, chosen.ID)
		if err != nil {
			return fmt.Errorf("reveal API key: %w", err)
		}
		fc.APIKey = secret
		fc.APIKeyID = chosen.ID
		if err := config.SaveFile(*fc); err != nil {
			return err
		}
		fmt.Fprintf(r.Out, "%s using existing API key %q\n", r.Styles.Success.Render("✓"), chosen.Name)
		return nil
	}

	created, err := api.CreateAPIKey(ctx, fc.DefaultOrg, defaultKeyName())
	if err != nil {
		return fmt.Errorf("create API key: %w", err)
	}
	fc.APIKey = created.Secret
	fc.APIKeyID = created.ID
	if err := config.SaveFile(*fc); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "%s created API key %q\n", r.Styles.Success.Render("✓"), created.Name)
	return nil
}

// promptKeySetup asks what to do about a product key. "Use existing" is only
// offered when the organization actually has one.
func promptKeySetup(hasExisting bool) (keySetupMode, error) {
	r := rootApp.renderer
	fmt.Fprintln(r.Out)
	fmt.Fprintln(r.Out, "Set up an API key for this organization?")
	fmt.Fprintln(r.Out, "  1. Create a new key (default)")
	if hasExisting {
		fmt.Fprintln(r.Out, "  2. Use an existing key")
		fmt.Fprintln(r.Out, "  3. Do nothing")
	} else {
		fmt.Fprintln(r.Out, "  2. Do nothing")
	}
	fmt.Fprint(r.Out, "> ")

	answer, err := readLine()
	if err != nil {
		return keySetupSkip, err
	}
	switch strings.TrimSpace(answer) {
	case "", "1":
		return keySetupCreate, nil
	case "2":
		if hasExisting {
			return keySetupExisting, nil
		}
		return keySetupSkip, nil
	case "3":
		if hasExisting {
			return keySetupSkip, nil
		}
	}
	return keySetupSkip, nil
}

// keyIDSelection is set by `auth login --api-key-id`, so adopting an existing key
// works without a prompt in scripts and CI.
var keyIDSelection string

// pickExistingKey resolves which key to adopt: an explicit --api-key-id, the only
// key when there is just one, or an interactive choice. Without a terminal and
// without a selector it fails with the ids rather than reading from a closed stdin.
func pickExistingKey(keys []console.APIKey) (console.APIKey, error) {
	if keyIDSelection != "" {
		for _, key := range keys {
			if key.ID == keyIDSelection {
				return key, nil
			}
		}
		return console.APIKey{}, fmt.Errorf("no API key %s in this organization", keyIDSelection)
	}
	if len(keys) == 1 {
		return keys[0], nil
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		var ids []string
		for _, key := range keys {
			ids = append(ids, fmt.Sprintf("%s (%s)", key.ID, key.Name))
		}
		return console.APIKey{}, fmt.Errorf(
			"several API keys exist; pass --api-key-id with one of: %s", strings.Join(ids, ", "))
	}
	return chooseKey(keys)
}

func chooseKey(keys []console.APIKey) (console.APIKey, error) {
	r := rootApp.renderer
	fmt.Fprintln(r.Out)
	fmt.Fprintln(r.Out, "Which API key?")
	for i, key := range keys {
		last := "never used"
		if key.LastUsedAt != nil {
			last = "last used " + key.LastUsedAt.Local().Format("2006-01-02")
		}
		fmt.Fprintf(r.Out, "  %d. %s  %s  %s\n", i+1, key.Name, key.Preview, r.Styles.Muted.Render(last))
	}
	fmt.Fprint(r.Out, "> ")

	answer, err := readLine()
	if err != nil {
		return console.APIKey{}, err
	}
	index, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil || index < 1 || index > len(keys) {
		return console.APIKey{}, fmt.Errorf("pick a number between 1 and %d", len(keys))
	}
	return keys[index-1], nil
}

func newAuthLogoutCmd() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:         "logout",
		Short:       "Revoke this device's session (API keys are left in place)",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			fc, err := config.LoadFile()
			if err != nil {
				return withCode(1, err)
			}
			if fc.SessionToken == "" {
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("Not signed in."))
				return nil
			}

			api := console.NewClient(rootApp.cfg.AuthURL, fc.SessionToken)
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			if all {
				err = api.RevokeAllSessions(ctx)
			} else {
				err = api.Logout(ctx)
			}
			// An already-dead session still clears locally.
			if err != nil && !errors.Is(err, console.ErrInvalidSession) && !errors.Is(err, console.ErrSessionExpired) {
				return withCode(1, err)
			}

			fc.SessionToken = ""
			fc.RefreshToken = ""
			fc.SessionExpiry = time.Time{}
			if err := config.SaveFile(fc); err != nil {
				return withCode(1, err)
			}

			if all {
				fmt.Fprintf(r.Out, "%s all sessions revoked\n", r.Styles.Success.Render("✓"))
			} else {
				fmt.Fprintf(r.Out, "%s signed out\n", r.Styles.Success.Render("✓"))
			}
			fmt.Fprintln(r.Out, r.Styles.Muted.Render("Your API key is still configured. Remove it with `tabstack keys revoke`."))
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "revoke every session on your account, not just this device")
	return cmd
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "status",
		Short:       "Show your session, organization, and API key state",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			cfg := rootApp.cfg
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			if cfg.APIKey == "" && cfg.SessionToken == "" {
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("Not signed in and no API key configured."))
				fmt.Fprintln(r.Out, "Run `tabstack auth login`.")
				return nil
			}

			if cfg.APIKey != "" {
				fmt.Fprintf(r.Out, "%s API key configured\n", r.Styles.Success.Render("✓"))
				fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("source:"), cfg.KeySource)
			}
			fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("base url:"), cfg.BaseURL)
			fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("auth url:"), cfg.AuthURL)

			if cfg.SessionToken == "" {
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("No session. Run `tabstack auth login` to manage keys from the CLI."))
			} else if err := reportSession(ctx, r, cfg); err != nil {
				return withCode(1, err)
			}

			if path, err := config.ConfigPath(); err == nil {
				fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("config:"), path)
			}
			return nil
		},
	}
}

// reportSession prints who the session belongs to, when it lapses, and whether
// the stored API key still exists on the server.
func reportSession(ctx context.Context, r ui.Renderer, cfg config.Config) error {
	api, _, err := authedClient(ctx, cfg)
	if err != nil {
		if errors.Is(err, console.ErrInvalidSession) || errors.Is(err, console.ErrSessionExpired) {
			fmt.Fprintln(r.Out, r.Styles.Muted.Render("Session is no longer valid. Run `tabstack auth login`."))
			return nil
		}
		return err
	}

	me, err := api.Me(ctx)
	if err != nil {
		if errors.Is(err, console.ErrInvalidSession) || errors.Is(err, console.ErrSessionExpired) {
			fmt.Fprintln(r.Out, r.Styles.Muted.Render("Session is no longer valid. Run `tabstack auth login`."))
			return nil
		}
		return err
	}

	fmt.Fprintf(r.Out, "%s signed in as %s\n", r.Styles.Success.Render("✓"), me.User.Email)
	if org := findOrg(me.Organizations, me.DefaultOrg); org != nil {
		fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("organization:"), org.Name)
	}
	if !me.Session.ExpiresAt.IsZero() {
		days := int(time.Until(me.Session.ExpiresAt).Hours() / 24)
		fmt.Fprintf(r.Out, "%s expires in %d days\n", r.Styles.Key.Render("session:"), days)
	}

	// Catch the case where the key was deleted in the console: the session still
	// works, but every product call would 401 with no obvious cause.
	if cfg.APIKeyID != "" && me.DefaultOrg != "" {
		keys, err := api.ListAPIKeys(ctx, me.DefaultOrg)
		if err == nil && !containsKey(keys, cfg.APIKeyID) {
			fmt.Fprintln(r.Out, r.Styles.ErrorTag.Render("!")+
				" the configured API key no longer exists in the console. Run `tabstack keys create`.")
		}
	}
	return nil
}

func newAuthSessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "sessions",
		Short:       "List the devices signed in to your account",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			api, _, err := authedClient(ctx, rootApp.cfg)
			if err != nil {
				return withCode(2, err)
			}
			sessions, err := api.Sessions(ctx)
			if err != nil {
				return withCode(1, err)
			}
			if len(sessions) == 0 {
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("No active sessions."))
				return nil
			}
			for _, s := range sessions {
				marker := " "
				if s.Current {
					marker = "*"
				}
				label := s.Label
				if label == "" {
					label = "Tabstack CLI"
				}
				last := "never used"
				if s.LastUsedAt != nil {
					last = "last used " + s.LastUsedAt.Local().Format("2006-01-02 15:04")
				}
				fmt.Fprintf(r.Out, "%s %s  %s  %s\n", marker, s.ID, label, r.Styles.Muted.Render(last))
			}
			return nil
		},
	}
}

func newAuthSwitchCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "switch [organization-id]",
		Short:       "Change the active organization without signing in again",
		Annotations: map[string]string{"skipClient": "true"},
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rootApp.renderer
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			api, fc, err := authedClient(ctx, rootApp.cfg)
			if err != nil {
				return withCode(2, err)
			}
			orgs, err := api.Organizations(ctx)
			if err != nil {
				return withCode(1, err)
			}
			if len(orgs) == 0 {
				return withCode(2, errors.New("your account has no organizations"))
			}

			target := ""
			if len(args) == 1 {
				target = args[0]
			} else {
				fmt.Fprintln(r.Out, "Which organization?")
				for i, org := range orgs {
					current := ""
					if org.ID == fc.DefaultOrg {
						current = r.Styles.Muted.Render(" (current)")
					}
					fmt.Fprintf(r.Out, "  %d. %s (%s)%s\n", i+1, org.Name, org.Role, current)
				}
				fmt.Fprint(r.Out, "> ")
				answer, err := readLine()
				if err != nil {
					return withCode(1, err)
				}
				index, err := strconv.Atoi(strings.TrimSpace(answer))
				if err != nil || index < 1 || index > len(orgs) {
					return withCode(2, fmt.Errorf("pick a number between 1 and %d", len(orgs)))
				}
				target = orgs[index-1].ID
			}

			chosen := findOrg(orgs, target)
			if chosen == nil {
				return withCode(2, fmt.Errorf("%q is not one of your organizations", target))
			}

			// The stored API key belongs to the old org, so it is no longer the right
			// credential. Clear it and point at `keys create`.
			fc.DefaultOrg = chosen.ID
			fc.APIKey = ""
			fc.APIKeyID = ""
			if err := config.SaveFile(fc); err != nil {
				return withCode(1, err)
			}

			fmt.Fprintf(r.Out, "%s switched to %s\n", r.Styles.Success.Render("✓"), chosen.Name)
			fmt.Fprintln(r.Out, r.Styles.Muted.Render("Run `tabstack keys create` to get an API key for it."))
			return nil
		},
	}
}

// authedClient builds a management client, refreshing the session first if the
// access token has aged out and persisting any rotation.
func authedClient(ctx context.Context, cfg config.Config) (*console.Client, config.File, error) {
	fc, err := config.LoadFile()
	if err != nil {
		return nil, fc, err
	}
	if fc.SessionToken == "" {
		return nil, fc, errors.New("not signed in. Run `tabstack auth login`")
	}

	stored := &oauth2.Token{
		AccessToken:  fc.SessionToken,
		RefreshToken: fc.RefreshToken,
		TokenType:    "bearer",
		Expiry:       fc.SessionExpiry,
	}

	fresh, err := console.TokenSource(ctx, cfg.AuthURL, stored).Token()
	if err != nil {
		return nil, fc, fmt.Errorf("could not refresh your session, run `tabstack auth login`: %w", err)
	}
	// Refresh tokens rotate on use, so persist immediately or the new one is lost.
	if fresh.AccessToken != fc.SessionToken || fresh.RefreshToken != fc.RefreshToken {
		applyToken(&fc, fresh)
		if err := config.SaveFile(fc); err != nil {
			return nil, fc, err
		}
	}
	return console.NewClient(cfg.AuthURL, fresh.AccessToken), fc, nil
}

func applyToken(fc *config.File, token *oauth2.Token) {
	fc.SessionToken = token.AccessToken
	fc.SessionExpiry = token.Expiry
	if token.RefreshToken != "" {
		fc.RefreshToken = token.RefreshToken
	}
}

func saveKeyDirectly(key string) error {
	if err := validateKeyFormat(key); err != nil {
		return withCode(2, err)
	}
	if err := config.Save(key, flagBaseURL); err != nil {
		return withCode(1, fmt.Errorf("save config: %w", err))
	}
	path, _ := config.ConfigPath()
	fmt.Fprintf(rootApp.renderer.Out, "%s key saved to %s\n",
		rootApp.renderer.Styles.Success.Render("✓"), path)
	return nil
}

// validateKeyFormat performs a basic sanity check on a supplied API key before
// saving it. It does not make an API call.
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

// readLine reads one line of user input from stdin, for the small numbered menus
// the auth commands present.
func readLine() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read input: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// promptForKey reads an API key without echoing it. Retained for the interactive
// `--key`-less legacy path used by `keys set`.
func promptForKey() (string, error) {
	fmt.Fprint(os.Stderr, "Tabstack API key: ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read key: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// warnMismatchedHosts catches the case where someone signs in to a non-production
// console while product calls still point at production. The key just issued is
// only valid against the API paired with that console, so the first request would
// 401 with nothing to explain why.
func warnMismatchedHosts(r ui.Renderer, authURL, baseURL string) {
	if authURL == config.DefaultAuthURL || baseURL != config.DefaultBaseURL {
		return
	}
	fmt.Fprintf(r.Out, "%s signed in to %s, but requests go to %s.\n",
		r.Styles.ErrorTag.Render("!"), authURL, baseURL)
	fmt.Fprintf(r.Out, "  The key issued here is only valid against that console's API. Set --base-url or %s.\n",
		"TABSTACK_BASE_URL")
}

func findOrg(orgs []console.Organization, id string) *console.Organization {
	for i := range orgs {
		if orgs[i].ID == id {
			return &orgs[i]
		}
	}
	return nil
}

func containsKey(keys []console.APIKey, id string) bool {
	for _, key := range keys {
		if key.ID == id {
			return true
		}
	}
	return false
}

// defaultKeyName names a CLI-created key after the machine, so the console list
// stays legible when someone signs in from several devices.
func defaultKeyName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "cli"
	}
	return "cli-" + host
}
