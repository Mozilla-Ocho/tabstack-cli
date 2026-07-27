package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

// newKeysCmd groups API key management. These commands drive the console
// management API with the CLI session, so they carry skipClient like the auth
// commands: they do not need a product API key to run.
func newKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "keys",
		Short:       "Manage the API keys your organization uses for the product API",
		Annotations: map[string]string{"skipClient": "true"},
	}
	cmd.AddCommand(newKeysCreateCmd(), newKeysListCmd(), newKeysRevokeCmd(), newKeysSetCmd())
	return cmd
}

func newKeysCreateCmd() *cobra.Command {
	var (
		orgFlag  string
		nameFlag string
		useFlag  bool
	)

	cmd := &cobra.Command{
		Use:         "create",
		Short:       "Create an API key and store it for use",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			ctx := commandContext(cmd)

			api, fc, err := authedClient(ctx, rootApp.cfg)
			if err != nil {
				return withCode(2, err)
			}

			orgID := orgFlag
			if orgID == "" {
				orgID = fc.DefaultOrg
			}
			if orgID == "" {
				return withCode(2, errors.New("no organization selected. Pass --org or run `tabstack auth switch`"))
			}

			name := nameFlag
			if name == "" {
				name = defaultKeyName()
			}

			created, err := api.CreateAPIKey(ctx, orgID, name)
			if err != nil {
				return withCode(1, err)
			}

			fmt.Fprintf(r.Out, "%s created API key %q\n", r.Styles.Success.Render("✓"), created.Name)

			if useFlag {
				fc.APIKey = created.Secret
				fc.APIKeyID = created.ID
				if err := config.SaveFile(fc); err != nil {
					return withCode(1, err)
				}
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("Stored as the key this CLI uses."))
			} else {
				// Plaintext is returned once, so show it when we are not storing it.
				fmt.Fprintf(r.Out, "%s %s\n", r.Styles.Key.Render("key:"), created.Secret)
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("This is the only time the key is shown."))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&orgFlag, "org", "", "organization to create the key in (defaults to the active one)")
	cmd.Flags().StringVar(&nameFlag, "name", "", "key name (defaults to cli-<hostname>)")
	cmd.Flags().BoolVar(&useFlag, "use", true, "store the new key as the one this CLI uses")
	return cmd
}

func newKeysListCmd() *cobra.Command {
	var orgFlag string

	cmd := &cobra.Command{
		Use:         "list",
		Short:       "List the API keys in an organization",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			ctx := commandContext(cmd)

			api, fc, err := authedClient(ctx, rootApp.cfg)
			if err != nil {
				return withCode(2, err)
			}

			orgID := orgFlag
			if orgID == "" {
				orgID = fc.DefaultOrg
			}
			if orgID == "" {
				return withCode(2, errors.New("no organization selected. Pass --org or run `tabstack auth switch`"))
			}

			keys, err := api.ListAPIKeys(ctx, orgID)
			if err != nil {
				return withCode(1, err)
			}
			if len(keys) == 0 {
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("No API keys in this organization."))
				return nil
			}

			for _, key := range keys {
				marker := " "
				if key.ID == fc.APIKeyID {
					marker = "*"
				}
				last := "never used"
				if key.LastUsedAt != nil {
					last = "last used " + key.LastUsedAt.Local().Format("2006-01-02")
				}
				fmt.Fprintf(r.Out, "%s %s  %-24s %s  %s\n",
					marker, key.ID, key.Name, key.Preview, r.Styles.Muted.Render(last))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&orgFlag, "org", "", "organization to list (defaults to the active one)")
	return cmd
}

func newKeysRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "revoke <key-id>",
		Short:       "Delete an API key",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rootApp.renderer
			ctx := commandContext(cmd)

			api, fc, err := authedClient(ctx, rootApp.cfg)
			if err != nil {
				return withCode(2, err)
			}
			if err := api.DeleteAPIKey(ctx, args[0]); err != nil {
				return withCode(1, err)
			}

			// If we just deleted the key this CLI was using, stop pointing at it.
			if fc.APIKeyID == args[0] {
				fc.APIKey = ""
				fc.APIKeyID = ""
				if err := config.SaveFile(fc); err != nil {
					return withCode(1, err)
				}
				fmt.Fprintln(r.Out, r.Styles.Muted.Render("That was the key this CLI used; run `tabstack keys create` for a new one."))
			}

			fmt.Fprintf(r.Out, "%s revoked %s\n", r.Styles.Success.Render("✓"), args[0])
			return nil
		},
	}
}

// newKeysSetCmd stores a key you already hold, without signing in. This is the
// path CI and anyone pasting a key from the console uses.
func newKeysSetCmd() *cobra.Command {
	var keyFlag string

	cmd := &cobra.Command{
		Use:         "set",
		Short:       "Store an API key you already have",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			key := keyFlag
			if key == "" {
				var err error
				key, err = promptForKey()
				if err != nil {
					return withCode(1, err)
				}
			}
			if key == "" {
				return withCode(2, errors.New("no key provided"))
			}
			return saveKeyDirectly(key)
		},
	}
	cmd.Flags().StringVar(&keyFlag, "key", "", "API key (if omitted, you will be prompted)")
	return cmd
}

// commandContext returns the command's context, falling back to Background so
// each RunE can pass a non-nil context to the console client.
func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
