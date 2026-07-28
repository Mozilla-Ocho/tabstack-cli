package cmd

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

// newConfigCmd groups commands that inspect and tidy the local config file.
// None of them talk to the product API, so they all carry skipClient.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and tidy your local configuration",
		Long: "Show where credentials are stored and what is in the file, without ever\n" +
			"printing a secret in full. Migration to the current config shape happens\n" +
			"automatically on the next save; these commands are for looking at the\n" +
			"result and clearing out what is no longer used.",
	}
	cmd.AddCommand(newConfigPathCmd(), newConfigShowCmd(), newConfigDropLegacyKeyCmd())
	return cmd
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "path",
		Short:       "Print the path of the config file",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Bare and unstyled: this exists to be substituted into other commands.
			fmt.Fprintln(rootApp.renderer.Out, rootApp.store.Path())
			return nil
		},
	}
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the stored configuration, with secrets redacted",
		Long: "Print every organisation the CLI knows about and the state of its API key,\n" +
			"not just the active one. Keys and tokens are redacted to their first and\n" +
			"last four characters, so the output is safe to paste into a bug report.",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			showConfig(rootApp.renderer, rootApp.cfg, rootApp.store.Path())
			return nil
		},
	}
}

// showConfig renders the whole config. Secrets are redacted here and nowhere
// else decides otherwise: there is no flag to print them in full.
func showConfig(r uiRenderer, cfg *config.Config, path string) {
	k := r.Styles.Key
	mode, permsOK := config.PermissionsOK(path)

	fmt.Fprintf(r.Out, "%s %s\n", k.Render("config:"), path)
	if permsOK {
		fmt.Fprintf(r.Out, "%s %#o\n", k.Render("permissions:"), mode)
	} else {
		fmt.Fprintf(r.Out, "%s %#o %s\n", k.Render("permissions:"), mode,
			r.Styles.ErrorTag.Render(fmt.Sprintf("should be 0600: chmod 600 %s", path)))
	}
	fmt.Fprintf(r.Out, "%s %d\n", k.Render("version:"), cfg.Version)

	// Session.
	if cfg.Session == nil || cfg.Session.AccessToken == "" {
		fmt.Fprintf(r.Out, "%s %s\n", k.Render("session:"), r.Styles.Muted.Render("none, run `tabstack auth login`"))
	} else {
		s := cfg.Session
		email := s.UserEmail
		if email == "" {
			email = "(unknown email)"
		}
		fmt.Fprintf(r.Out, "%s %s %s\n", k.Render("session:"), email,
			r.Styles.Muted.Render(config.Redact(s.AccessToken)))
		if !s.ExpiresAt.IsZero() {
			state := "expires " + s.ExpiresAt.Local().Format(time.RFC1123)
			if time.Until(s.ExpiresAt) <= 0 {
				state = "expired " + s.ExpiresAt.Local().Format(time.RFC1123)
			}
			fmt.Fprintf(r.Out, "%s %s\n", k.Render("           "), r.Styles.Muted.Render(state))
		}
		if s.Scope != "" {
			fmt.Fprintf(r.Out, "%s %s\n", k.Render("scope:"), s.Scope)
		}
	}

	// Endpoints, as they will actually resolve for this invocation.
	fmt.Fprintf(r.Out, "%s %s\n", k.Render("base url:"), cfg.ResolveBaseURL(flagBaseURL))
	fmt.Fprintf(r.Out, "%s %s\n", k.Render("auth url:"), cfg.ResolveAuthURL(flagAuthURL))

	// Every org, not just the active one: this is the view that answers "which
	// credential would go out if I switched".
	orgs := orgRefsFromConfig(cfg)
	if len(orgs) == 0 {
		fmt.Fprintf(r.Out, "%s %s\n", k.Render("orgs:"), r.Styles.Muted.Render("none stored"))
	} else {
		fmt.Fprintf(r.Out, "%s\n", k.Render("orgs:"))
		for _, o := range orgs {
			marker := " "
			if o.ID == cfg.ActiveOrg {
				marker = r.Styles.Success.Render("*")
			}
			name := o.Name
			if name == "" {
				name = "(unnamed)"
			}
			fmt.Fprintf(r.Out, "%s %s %s\n", marker, name, r.Styles.Muted.Render(o.ID))
			fmt.Fprintf(r.Out, "    %s\n", r.Styles.Muted.Render(orgKeyLine(cfg.Org(o.ID))))
		}
		fmt.Fprintln(r.Out, r.Styles.Muted.Render("* active org: its key is the one product commands send."))
	}

	if cfg.ActiveOrg == "" {
		fmt.Fprintf(r.Out, "%s %s\n", k.Render("active org:"), r.Styles.Muted.Render("none"))
	}

	// Legacy key, and whether it is doing anything.
	if cfg.LegacyAPIKey != "" {
		state := "ignored, the active org's key wins"
		if cfg.ActiveOrg == "" {
			state = "in use, because no organisation is active"
		}
		fmt.Fprintf(r.Out, "%s %s (%s)\n", k.Render("legacy key:"),
			r.Styles.Muted.Render(config.Redact(cfg.LegacyAPIKey)), state)
		if cfg.ActiveOrg != "" {
			fmt.Fprintln(r.Out, r.Styles.Muted.Render("  remove it with `tabstack config drop-legacy-key`"))
		}
	}

	if os.Getenv(config.EnvAPIKey) != "" {
		fmt.Fprintf(r.Out, "%s %s is set and overrides every stored key for product calls\n",
			r.Styles.ErrorTag.Render("!"), config.EnvAPIKey)
	}
}

// orgKeyLine describes one org's stored key without revealing it.
func orgKeyLine(org *config.OrgCreds) string {
	if org == nil || org.APIKey == "" {
		return "no key stored"
	}
	line := "key " + config.Redact(org.APIKey)
	if org.APIKeyName != "" {
		line += " (" + org.APIKeyName + ")"
	}
	if org.APIKeyID != "" {
		line += " id " + org.APIKeyID
	}
	return line
}

func newConfigDropLegacyKeyCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "drop-legacy-key",
		Short: "Remove the pre-organisation API key from the config file",
		Long: "Delete the single global API key carried over from a pre-organisation\n" +
			"config. It is only used while no organisation is active, so this refuses\n" +
			"to run until the active organisation has a key of its own, which stops it\n" +
			"leaving you with no working credential.",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			cfg := rootApp.cfg

			if cfg.LegacyAPIKey == "" {
				fmt.Fprintln(r.Out, "no legacy API key stored, nothing to do")
				return nil
			}

			// The guard is the whole point of the command: dropping the only
			// credential a user has is worse than leaving a stale line in a file.
			if !force && !cfg.HasKey(cfg.ActiveOrg) {
				return withCode(2, errors.New(dropBlockedReason(cfg)))
			}

			cfg.LegacyAPIKey = ""
			if err := rootApp.store.Save(cfg); err != nil {
				return withCode(1, fmt.Errorf("save config: %w", err))
			}

			fmt.Fprintf(r.Out, "%s legacy API key removed from %s\n",
				r.Styles.Success.Render("✓"), rootApp.store.Path())
			if cfg.ActiveOrg != "" {
				fmt.Fprintf(r.Out, "product commands now use %s's key\n", cfg.OrgName(cfg.ActiveOrg))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false,
		"drop it even if no organisation key is in place (you may be left with no credential)")
	return cmd
}

// dropBlockedReason explains which half of the guard failed and what to run.
func dropBlockedReason(cfg *config.Config) string {
	if cfg.ActiveOrg == "" {
		return "refusing to drop the legacy key: no organisation is active, so it is the credential in use. " +
			"Run `tabstack auth login`, or pass --force to remove it anyway"
	}
	return fmt.Sprintf("refusing to drop the legacy key: %s has no key of its own. "+
		"Run `tabstack keys create --org %s`, or pass --force to remove it anyway",
		cfg.OrgName(cfg.ActiveOrg), cfg.ActiveOrg)
}
