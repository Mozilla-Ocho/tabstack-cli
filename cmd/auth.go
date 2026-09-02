package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
)

// expiryWarnWindow is how close a session has to be to expiring before
// `auth status` calls it out.
const expiryWarnWindow = 7 * 24 * time.Hour

// newAuthCmd groups session and organisation management. These commands carry
// the skipClient annotation so the root pre-run does not require a product
// credential before one exists, which is what lets `auth login` work on a fresh
// install.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Sign in, inspect your session, and switch organisation",
		Long: "Manage the user-scoped session used for sign-in and organisation\n" +
			"management. API keys themselves are organisation scoped and managed\n" +
			"with `tabstack keys`.",
		Example: "  tabstack auth login\n  tabstack auth status\n  tabstack auth switch acme",
	}
	cmd.AddCommand(
		newAuthLoginCmd(),
		newAuthStatusCmd(),
		newAuthLogoutCmd(),
		newAuthSwitchCmd(),
		newAuthSessionsCmd(),
	)
	return cmd
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "status",
		Short:       "Show who you are signed in as and which credential is in play",
		Example:     "  # What have I changed, and what has moved upstream?\n  tabstack schema status\n\n  # Skip the network; only report local edits\n  tabstack schema status --local",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			cfg := rootApp.cfg
			k := r.Styles.Key

			if jsonMode(r) {
				return emitJSON(r, authStatusJSON(cfg, rootApp.store.Path()))
			}

			// 1. Identity.
			if cfg.Session == nil || cfg.Session.AccessToken == "" {
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("not signed in"))
				fmt.Fprintln(r.Out, "Sign in with `tabstack auth login`.")
			} else {
				email := cfg.Session.UserEmail
				if email == "" {
					email = "(unknown email)"
				}
				fmt.Fprintf(r.Out, "%s signed in as %s\n", r.Styles.Success.Render("✓"), email)
			}

			// 2. Active organisation.
			if cfg.ActiveOrg == "" {
				fmt.Fprintf(r.Out, "%s %s\n", k.Render("active org:"), r.Styles.Muted.Render("none"))
			} else {
				fmt.Fprintf(r.Out, "%s %s (%s)\n", k.Render("active org:"), cfg.OrgName(cfg.ActiveOrg), cfg.ActiveOrg)
			}

			// 3. Session expiry.
			if cfg.Session != nil && !cfg.Session.ExpiresAt.IsZero() {
				printExpiry(r, cfg.Session.ExpiresAt)
			}

			// 4. Key state for the active org.
			printKeyState(r, cfg)

			// 5. Environment override. TABSTACK_API_KEY wins for product calls, so
			// say that plainly rather than showing a stored org key that is not
			// actually the one being used.
			if os.Getenv(config.EnvAPIKey) != "" {
				fmt.Fprintf(r.Out, "%s %s is set and overrides stored keys for product calls\n",
					r.Styles.ErrorTag.Render("!"), config.EnvAPIKey)
			}

			// 6. Config location and permissions.
			path := rootApp.store.Path()
			fmt.Fprintf(r.Out, "%s %s\n", k.Render("config:"), path)
			if mode, ok := config.PermissionsOK(path); !ok {
				fmt.Fprintf(r.Out, "%s permissions are %#o, should be 0600: chmod 600 %s\n",
					r.Styles.ErrorTag.Render("!"), mode, path)
			}

			// Finally, confirm the stored key still exists server-side. Skipped
			// silently without a session: there is nothing to ask with.
			return checkKeyRevoked(cmd.Context(), r, cfg)
		},
	}
}

// printExpiry renders the session expiry in days, warning when it is close.
func printExpiry(r uiRenderer, at time.Time) {
	d := time.Until(at)
	days := int(d.Hours() / 24)
	switch {
	case d <= 0:
		fmt.Fprintf(r.Out, "%s session expired. Run: tabstack auth login\n", r.Styles.ErrorTag.Render("!"))
	case d < expiryWarnWindow:
		fmt.Fprintf(r.Out, "%s session expires in %s (%s)\n",
			r.Styles.ErrorTag.Render("!"), humanDuration(d), at.Local().Format(time.RFC1123))
	default:
		fmt.Fprintf(r.Out, "%s expires in %d days\n", r.Styles.Key.Render("session:"), days)
	}
}

// humanDuration renders a short duration the way a warning should read: days
// when there are any, hours otherwise.
func humanDuration(d time.Duration) string {
	if days := int(d.Hours() / 24); days >= 1 {
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	hours := int(d.Hours())
	if hours <= 1 {
		return "under an hour"
	}
	return fmt.Sprintf("%d hours", hours)
}

// printKeyState reports whether the active org has a stored API key, with a
// redacted preview so the user can match it against the console without the
// plaintext ever being printed.
func printKeyState(r uiRenderer, cfg *config.Config) {
	k := r.Styles.Key
	switch {
	case cfg.ActiveOrg == "" && cfg.LegacyAPIKey != "":
		fmt.Fprintf(r.Out, "%s stored (from a pre-organisation config) %s\n",
			k.Render("api key:"), r.Styles.Muted.Render(config.Redact(cfg.LegacyAPIKey)))
	case cfg.ActiveOrg == "":
		fmt.Fprintf(r.Out, "%s %s\n", k.Render("api key:"), r.Styles.Muted.Render("not stored"))
	case cfg.HasKey(cfg.ActiveOrg):
		o := cfg.Org(cfg.ActiveOrg)
		line := fmt.Sprintf("stored %s", config.Redact(o.APIKey))
		if o.APIKeyName != "" {
			line += fmt.Sprintf(" (%s)", o.APIKeyName)
		}
		fmt.Fprintf(r.Out, "%s %s\n", k.Render("api key:"), line)
	default:
		fmt.Fprintf(r.Out, "%s not stored. Run: tabstack keys create --org %s\n",
			k.Render("api key:"), cfg.ActiveOrg)
	}
}

// actionJSON is the machine-readable acknowledgement for commands whose pretty
// output is a single confirmation line. Keeping one shape across them means a
// script can branch on .ok without learning a type per command.
type actionJSON struct {
	Action string `json:"action"`
	OK     bool   `json:"ok"`
	ID     string `json:"id,omitempty"`
	Org    string `json:"org,omitempty"`
	Note   string `json:"note,omitempty"`
}

// statusJSON is the machine-readable form of `auth status`. Declared rather
// than assembled from a map so the shape is reviewable in one place and stable
// for scripts. Secrets stay redacted exactly as in pretty mode: there is no
// output mode that prints a credential in full.
type statusJSON struct {
	SignedIn       bool    `json:"signed_in"`
	Email          string  `json:"email,omitempty"`
	SessionExpiry  *string `json:"session_expires_at,omitempty"`
	SessionExpired bool    `json:"session_expired"`
	ActiveOrg      string  `json:"active_org,omitempty"`
	ActiveOrgName  string  `json:"active_org_name,omitempty"`
	APIKeyStored   bool    `json:"api_key_stored"`
	APIKeyPreview  string  `json:"api_key_preview,omitempty"`
	APIKeyName     string  `json:"api_key_name,omitempty"`
	APIKeyLegacy   bool    `json:"api_key_from_legacy_config"`
	EnvOverride    bool    `json:"env_override"`
	ConfigPath     string  `json:"config_path"`
	ConfigPerms    string  `json:"config_permissions,omitempty"`
	PermsOK        bool    `json:"config_permissions_ok"`
}

// authStatusJSON builds statusJSON from config. It mirrors the pretty-mode
// branches in printKeyState and printExpiry; keep the two in step.
func authStatusJSON(cfg *config.Config, path string) statusJSON {
	out := statusJSON{
		ActiveOrg:   cfg.ActiveOrg,
		EnvOverride: os.Getenv(config.EnvAPIKey) != "",
		ConfigPath:  path,
	}
	if cfg.ActiveOrg != "" {
		out.ActiveOrgName = cfg.OrgName(cfg.ActiveOrg)
	}
	if cfg.Session != nil && cfg.Session.AccessToken != "" {
		out.SignedIn = true
		out.Email = cfg.Session.UserEmail
		if !cfg.Session.ExpiresAt.IsZero() {
			at := cfg.Session.ExpiresAt.UTC().Format(time.RFC3339)
			out.SessionExpiry = &at
			out.SessionExpired = time.Until(cfg.Session.ExpiresAt) <= 0
		}
	}
	switch {
	case cfg.ActiveOrg == "" && cfg.LegacyAPIKey != "":
		out.APIKeyStored = true
		out.APIKeyLegacy = true
		out.APIKeyPreview = config.Redact(cfg.LegacyAPIKey)
	case cfg.ActiveOrg != "" && cfg.HasKey(cfg.ActiveOrg):
		o := cfg.Org(cfg.ActiveOrg)
		out.APIKeyStored = true
		out.APIKeyPreview = config.Redact(o.APIKey)
		out.APIKeyName = o.APIKeyName
	}
	mode, exists, ok := config.PermissionsState(path)
	out.PermsOK = ok
	if exists {
		out.ConfigPerms = fmt.Sprintf("%#o", mode)
	}
	return out
}

// checkKeyRevoked verifies the stored key id still exists for the active org. A
// key revoked in the console keeps working in config until something asks, and
// "401 from the API" is a poor way to find out.
func checkKeyRevoked(ctx context.Context, r uiRenderer, cfg *config.Config) error {
	if cfg.Session == nil || cfg.Session.AccessToken == "" {
		return nil
	}
	o := cfg.Org(cfg.ActiveOrg)
	if o == nil || o.APIKeyID == "" {
		return nil
	}

	c, _ := consoleClient()
	keys, err := c.ListAPIKeys(ctx, cfg.ActiveOrg)
	if err != nil {
		// Not being able to check is not a failure of `auth status` itself.
		fmt.Fprintf(r.Err, "could not verify the stored key against the console: %v\n", err)
		return nil
	}
	for _, key := range keys {
		if key.ID == o.APIKeyID {
			return nil
		}
	}
	fmt.Fprintf(r.Out, "%s the stored key for %s was revoked in the console. Run: tabstack keys create --org %s\n",
		r.Styles.ErrorTag.Render("!"), cfg.OrgName(cfg.ActiveOrg), cfg.ActiveOrg)
	return nil
}

func newAuthLogoutCmd() *cobra.Command {
	var (
		all bool
		yes bool
	)

	cmd := &cobra.Command{
		Use:         "logout",
		Short:       "Revoke this session (or all of them) and clear it locally",
		Example:     "  # Revoke this session only\n  tabstack auth logout\n\n  # Revoke every session, on every machine (asks first)\n  tabstack auth logout --all --yes",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			if rootApp.cfg.Session == nil || rootApp.cfg.Session.AccessToken == "" {
				if jsonMode(r) {
					return emitJSON(r, actionJSON{Action: "logout", OK: true, Note: "not signed in"})
				}
				fmt.Fprintln(r.Out, "not signed in")
				return nil
			}

			c, sm := consoleClient()
			ctx := cmd.Context()

			// Signing this session out is routine and reversible by signing in
			// again. --all reaches every other machine the user is signed in on,
			// so that one is confirmed.
			if all {
				ok, err := confirmDestructive(r, "revoke every session for your user, on all machines", yes)
				if err != nil || !ok {
					return err
				}
			}

			var err error
			if all {
				err = c.RevokeAllSessions(ctx)
			} else {
				err = c.Logout(ctx)
			}
			// An already-dead session still has to be cleared locally, otherwise
			// the user is stuck with a stale token and no way to drop it.
			if err != nil && !errors.Is(err, console.ErrSessionExpired) {
				return classifyConsoleError(err)
			}

			if clearErr := sm.Clear(); clearErr != nil {
				return withCode(1, fmt.Errorf("clear session: %w", clearErr))
			}

			what := "session revoked"
			action := "logout"
			if all {
				what = "all sessions revoked"
				action = "logout_all"
			}
			if jsonMode(r) {
				return emitJSON(r, actionJSON{Action: action, OK: true})
			}
			fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Success.Render("✓"), what)
			fmt.Fprintln(r.Out, r.Styles.Muted.Render("stored API keys were left in place; remove them with `tabstack keys revoke <id>`"))
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "revoke every session for your user, not just this one")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt for --all")
	return cmd
}

func newAuthSessionsCmd() *cobra.Command {
	var (
		revoke string
		yes    bool
	)

	cmd := &cobra.Command{
		Use:         "sessions",
		Short:       "List your CLI sessions",
		Example:     "  # List your CLI sessions; the current one is marked\n  tabstack auth sessions\n\n  # Revoke one by id\n  tabstack auth sessions --revoke sess_abc123",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			c, _, err := requireSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			if revoke != "" {
				ok, err := confirmDestructive(r, fmt.Sprintf("revoke session %s", revoke), yes)
				if err != nil || !ok {
					return err
				}
				if err := c.RevokeSession(ctx, revoke); err != nil {
					return classifyConsoleError(err)
				}
				if jsonMode(r) {
					return emitJSON(r, actionJSON{Action: "session_revoked", ID: revoke, OK: true})
				}
				fmt.Fprintf(r.Out, "%s session %s revoked\n", r.Styles.Success.Render("✓"), revoke)
				return nil
			}

			sessions, err := c.Sessions(ctx)
			if err != nil {
				return classifyConsoleError(err)
			}
			if jsonMode(r) {
				// Emit the server's own shape: these are already flat records
				// with json tags, so there is nothing to restate.
				return emitJSON(r, sessions)
			}
			if len(sessions) == 0 {
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("no active sessions"))
				return nil
			}

			for _, s := range sessions {
				marker := " "
				if s.Current {
					marker = r.Styles.Success.Render("*")
				}
				label := s.Label
				if label == "" {
					label = "(unlabelled)"
				}
				fmt.Fprintf(r.Out, "%s %s  %s\n", marker, label, r.Styles.Muted.Render(s.ID))
				fmt.Fprintf(r.Out, "    %s\n", r.Styles.Muted.Render(sessionTimes(s)))
			}
			if len(sessions) > 0 {
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("\n* current session. Revoke one with --revoke <id>."))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&revoke, "revoke", "", "revoke a session by id")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt for --revoke")
	return cmd
}

// sessionTimes renders whichever of a session's timestamps the server sent.
func sessionTimes(s console.SessionInfo) string {
	parts := make([]string, 0, 3)
	if s.CreatedAt != nil {
		parts = append(parts, "created "+s.CreatedAt.Local().Format(time.RFC1123))
	}
	if s.LastUsedAt != nil {
		parts = append(parts, "last used "+s.LastUsedAt.Local().Format(time.RFC1123))
	}
	if s.ExpiresAt != nil {
		parts = append(parts, "expires "+s.ExpiresAt.Local().Format(time.RFC1123))
	}
	if len(parts) == 0 {
		return "no timestamps reported"
	}
	return joinComma(parts)
}

// joinComma joins with ", " without pulling strings.Join's import into every
// caller's mental model of what this file does.
func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// classifyConsoleError maps auth-host failures onto the CLI's exit codes: an
// expired session is a configuration problem the user must fix (2), a rejected
// request is an API error (3), anything else is runtime (1).
func classifyConsoleError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, console.ErrSessionExpired) ||
		errors.Is(err, console.ErrInvalidSession) ||
		errors.Is(err, console.ErrNoSession) {
		return withCode(2, err)
	}
	var apiErr *console.APIError
	if errors.As(err, &apiErr) {
		return withCode(3, err)
	}
	return classifyError(err)
}
