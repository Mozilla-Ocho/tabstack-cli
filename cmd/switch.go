package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
)

func newAuthSwitchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "switch [organisation]",
		Short: "Change which organisation your commands act as",
		Long: "Switch the active organisation. The session is user scoped, so switching\n" +
			"never signs you in again; it selects which stored API key product calls\n" +
			"use. Pass an organisation id, name, or unique name prefix, or run with no\n" +
			"argument to pick from a list.",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			return runSwitch(cmd.Context(), arg)
		},
	}
}

// runSwitch resolves the target organisation and makes it active. Switching is a
// credential change, not a display setting: it decides which API key every
// later command sends.
func runSwitch(ctx context.Context, arg string) error {
	r := rootApp.renderer
	cfg := rootApp.cfg

	c, _, err := requireSession()
	if err != nil {
		return err
	}

	orgs, listErr := c.Organizations(ctx)
	if listErr != nil {
		// Offline tolerance: an org we already know about can still be selected
		// when the list cannot be refreshed. Anything the server actively
		// rejected is a real error.
		if arg == "" || !isOfflineError(listErr) {
			return classifyConsoleError(listErr)
		}
		id, resolveErr := resolveOrgLocal(cfg, arg)
		if resolveErr != nil {
			return withCode(2, resolveErr)
		}
		fmt.Fprintf(r.Err, "could not refresh the organisation list (%v); using the copy in your config\n", listErr)
		return applySwitch(ctx, c, orgRef{ID: id, Name: cfg.OrgName(id)})
	}

	if arg == "" {
		target, err := pickOrg(r, orgs)
		if err != nil {
			return err
		}
		if target.ID == "" {
			// Single-org user: nothing to pick between.
			return nil
		}
		return applySwitch(ctx, c, target)
	}

	target, err := resolveOrgRef(orgRefsFromConsole(orgs), arg)
	if err != nil {
		return withCode(2, err)
	}
	return applySwitch(ctx, c, target)
}

// pickOrg renders the interactive organisation picker. A user with exactly one
// organisation is told so instead of being shown a list of one. A zero-value
// orgRef with no error means "nothing to do".
func pickOrg(r uiRenderer, orgs []console.Org) (orgRef, error) {
	cfg := rootApp.cfg

	switch len(orgs) {
	case 0:
		return orgRef{}, withCode(2, errors.New("your user does not belong to any organisations"))
	case 1:
		fmt.Fprintf(r.Out, "You belong to one organization: %s\n", orgs[0].Name)
		return orgRef{}, nil
	}

	refs := orgRefsFromConsole(orgs)
	for i, o := range refs {
		marker := " "
		if o.ID == cfg.ActiveOrg {
			marker = r.Styles.Success.Render("*")
		}
		keyState := "no key"
		if cfg.HasKey(o.ID) {
			keyState = "key stored"
		}
		fmt.Fprintf(r.Err, "%s %d) %s  %s\n", marker, i+1, o.Name,
			r.Styles.Muted.Render(fmt.Sprintf("%s, %s", o.Role, keyState)))
	}

	line, err := promptLine(fmt.Sprintf("Switch to which organisation? [1-%d]: ", len(refs)))
	if err != nil {
		return orgRef{}, withCode(2, errors.New("auth switch requires a terminal, pass an organization name or id"))
	}
	line = strings.TrimSpace(line)

	if n, convErr := strconv.Atoi(line); convErr == nil {
		if n < 1 || n > len(refs) {
			return orgRef{}, withCode(2, fmt.Errorf("not a valid choice: %q", line))
		}
		return refs[n-1], nil
	}

	// Anything that is not a number goes through the same resolution rules as a
	// command-line argument, so typing a name into the picker behaves the same.
	target, err := resolveOrgRef(refs, line)
	if err != nil {
		return orgRef{}, withCode(2, err)
	}
	return target, nil
}

// applySwitch records the new active organisation and makes sure it has a key.
func applySwitch(ctx context.Context, c *console.Client, target orgRef) error {
	r := rootApp.renderer
	cfg := rootApp.cfg

	cfg.ActiveOrg = target.ID
	cfg.UpsertOrg(target.ID, target.Name)
	if err := rootApp.store.Save(cfg); err != nil {
		return withCode(1, fmt.Errorf("save config: %w", err))
	}

	if !cfg.HasKey(target.ID) {
		// Same prompt login uses, so a switch into a fresh org is not a dead end.
		if err := runKeySetup(ctx, c, target.ID, keySetupPrompt); err != nil {
			return err
		}
	}

	name := cfg.OrgName(target.ID)
	if cfg.HasKey(target.ID) {
		fmt.Fprintf(r.Out, "%s now acting as %s (%s), API key in place\n",
			r.Styles.Success.Render("✓"), name, target.ID)
	} else {
		fmt.Fprintf(r.Out, "%s now acting as %s (%s), no API key stored\n",
			r.Styles.Success.Render("✓"), name, target.ID)
	}
	return nil
}

// isOfflineError reports whether err looks like "could not reach the console"
// rather than "the console said no". Only the former is safe to work around
// with a cached org list.
func isOfflineError(err error) bool {
	if errors.Is(err, console.ErrSessionExpired) ||
		errors.Is(err, console.ErrInvalidSession) ||
		errors.Is(err, console.ErrNoSession) {
		return false
	}
	var apiErr *console.APIError
	return !errors.As(err, &apiErr)
}
