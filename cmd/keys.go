package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// newKeysCmd groups organisation API key management. Keys are org scoped, so
// every subcommand works against one organisation: --org, defaulting to the
// active one.
func newKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Create, list, and revoke organisation API keys",
		Long: "API keys are scoped to an organisation and are the credential product\n" +
			"commands send. They are managed through your signed-in session, so these\n" +
			"commands need `tabstack auth login` first.",
	}
	cmd.AddCommand(newKeysCreateCmd(), newKeysListCmd(), newKeysUseCmd(), newKeysRevokeCmd())
	return cmd
}

func newKeysUseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use [key-id]",
		Short: "Adopt one of an organisation's existing API keys into this CLI",
		Long: "Reveal an existing API key and store it as the one this CLI sends for the\n" +
			"organisation, replacing any key currently stored. Pass a key id (see\n" +
			"`tabstack keys list`) to pick directly; with no id, the single key is\n" +
			"adopted automatically or you are prompted to choose.",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := requireSession()
			if err != nil {
				return err
			}
			orgID, err := targetOrg()
			if err != nil {
				return err
			}
			keyID := ""
			if len(args) == 1 {
				keyID = args[0]
			}
			return adoptKey(cmd.Context(), c, orgID, keyID)
		},
	}
	return cmd
}

func newKeysCreateCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:         "create",
		Short:       "Create an API key for an organisation and store it",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := requireSession()
			if err != nil {
				return err
			}
			orgID, err := targetOrg()
			if err != nil {
				return err
			}
			if name == "" {
				name = defaultKeyName()
			}
			return createAndStoreKey(cmd.Context(), c, orgID, name)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "key name shown in the console (default: cli-<hostname>)")
	return cmd
}

func newKeysListCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "list",
		Short:       "List an organisation's API keys (previews only)",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := rootApp.renderer
			c, _, err := requireSession()
			if err != nil {
				return err
			}
			orgID, err := targetOrg()
			if err != nil {
				return err
			}

			keys, err := c.ListAPIKeys(cmd.Context(), orgID)
			if err != nil {
				return classifyConsoleError(err)
			}
			if len(keys) == 0 {
				fmt.Fprintf(r.Out, "%s\n", r.Styles.Muted.Render(
					fmt.Sprintf("no API keys for %s. Create one with: tabstack keys create --org %s",
						rootApp.cfg.OrgName(orgID), orgID)))
				return nil
			}

			stored := rootApp.cfg.Org(orgID)
			for _, k := range keys {
				marker := " "
				if stored != nil && stored.APIKeyID == k.ID {
					marker = r.Styles.Success.Render("*")
				}
				// Previews only. The plaintext is never printed by list, whatever
				// the server happens to include in the payload.
				fmt.Fprintf(r.Out, "%s %s  %s  %s\n", marker, k.Name,
					r.Styles.Muted.Render(k.Preview), r.Styles.Muted.Render(k.ID))
				if k.LastUsedAt != nil {
					fmt.Fprintf(r.Out, "    %s\n",
						r.Styles.Muted.Render("last used "+k.LastUsedAt.Local().Format(time.RFC1123)))
				}
			}
			fmt.Fprintln(r.Out, r.Styles.Muted.Render("\n* the key stored in this CLI."))
			return nil
		},
	}
}

func newKeysRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "revoke <key-id>",
		Short:       "Revoke an API key",
		Args:        exactArgsNamed("<key-id>"),
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rootApp.renderer
			cfg := rootApp.cfg
			keyID := args[0]

			c, _, err := requireSession()
			if err != nil {
				return err
			}
			if err := c.RevokeAPIKey(cmd.Context(), keyID); err != nil {
				return classifyConsoleError(err)
			}
			fmt.Fprintf(r.Out, "%s revoked key %s\n", r.Styles.Success.Render("✓"), keyID)

			// A revoked key left in config would keep being sent until the API
			// starts rejecting it, so drop it and say which org is now keyless.
			for orgID, org := range cfg.Orgs {
				if org == nil || org.APIKeyID != keyID {
					continue
				}
				org.APIKey = ""
				org.APIKeyID = ""
				org.APIKeyName = ""
				if err := rootApp.store.Save(cfg); err != nil {
					return withCode(1, fmt.Errorf("save config: %w", err))
				}
				fmt.Fprintf(r.Out, "%s %s now has no API key stored. Create one with: tabstack keys create --org %s\n",
					r.Styles.ErrorTag.Render("!"), cfg.OrgName(orgID), orgID)
				break
			}
			return nil
		},
	}
}

// targetOrg resolves which organisation a keys command acts on: --org when
// given, otherwise the active org. It resolves --org against the server list
// when possible so a brand new org (not yet in config) can be named, falling
// back to the local list when the console is unreachable.
func targetOrg() (string, error) {
	cfg := rootApp.cfg
	if flagOrg == "" {
		if cfg.ActiveOrg == "" {
			return "", withCode(2, errors.New("no active organisation. Run `tabstack auth switch`, or pass --org"))
		}
		return cfg.ActiveOrg, nil
	}

	c, _ := consoleClient()
	orgs, err := c.Organizations(context.Background())
	if err != nil {
		id, localErr := resolveOrgLocal(cfg, flagOrg)
		if localErr != nil {
			return "", withCode(2, localErr)
		}
		return id, nil
	}

	target, err := resolveOrgRef(orgRefsFromConsole(orgs), flagOrg)
	if err != nil {
		return "", withCode(2, err)
	}
	// Remember the name so later output and error messages can use it.
	cfg.UpsertOrg(target.ID, target.Name)
	return target.ID, nil
}
