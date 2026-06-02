package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

// newAuthCmd groups credential management. These commands carry the skipClient
// annotation so the root pre-run does not require a key before one exists,
// which is what lets `auth login` work on a fresh install.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage your Tabstack API credentials",
	}
	cmd.AddCommand(newAuthLoginCmd(), newAuthStatusCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var keyFlag string

	cmd := &cobra.Command{
		Use:         "login",
		Short:       "Store your API key in the config file",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			key := keyFlag

			// If no key was passed, prompt without echoing it to the terminal.
			if key == "" {
				fmt.Fprint(os.Stderr, "Tabstack API key: ")
				raw, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
				if err != nil {
					return withCode(1, fmt.Errorf("read key: %w", err))
				}
				key = strings.TrimSpace(string(raw))
			}

			if key == "" {
				return withCode(2, fmt.Errorf("no key provided"))
			}

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
		},
	}

	cmd.Flags().StringVar(&keyFlag, "key", "", "API key (if omitted, you will be prompted)")
	return cmd
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

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "status",
		Short:       "Show how your API key is being resolved",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			cfg := rootApp.cfg

			if cfg.APIKey == "" {
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("No API key configured."))
				fmt.Fprintln(r.Out, "Set one with `tabstack auth login`.")
				return nil
			}

			// Never print the key itself, only that one exists and where from.
			fmt.Fprintf(r.Out, "%s API key configured\n", r.Styles.Success.Render("✓"))
			fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("source:"), cfg.KeySource)
			fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("base url:"), cfg.BaseURL)
			if path, err := config.ConfigPath(); err == nil {
				fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("config:"), path)
			}
			return nil
		},
	}
}
