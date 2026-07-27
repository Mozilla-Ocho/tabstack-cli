package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
)

// keySetupMode is what to do about an organisation's API key after signing in or
// switching. The empty value means "ask, if we can".
type keySetupMode string

const (
	keySetupPrompt   keySetupMode = ""
	keySetupCreate   keySetupMode = "create"
	keySetupExisting keySetupMode = "existing"
	keySetupSkip     keySetupMode = "skip"
)

// keySetupModeFrom validates the --api-key-setup / --no-key flag pair.
func keySetupModeFrom(flag string, noKey bool) (keySetupMode, error) {
	if noKey && flag != "" && flag != string(keySetupSkip) {
		return keySetupPrompt, fmt.Errorf("--no-key conflicts with --api-key-setup=%s", flag)
	}
	if noKey {
		return keySetupSkip, nil
	}

	switch keySetupMode(flag) {
	case keySetupPrompt, keySetupCreate, keySetupSkip:
		return keySetupMode(flag), nil
	case keySetupExisting:
		if !console.RevealEnabled {
			return keySetupPrompt, errors.New("--api-key-setup=existing is not available in this build; use create or skip")
		}
		return keySetupExisting, nil
	default:
		return keySetupPrompt, fmt.Errorf("invalid --api-key-setup %q: must be one of: create, existing, skip", flag)
	}
}

// runKeySetup makes sure the target organisation has a product credential. It is
// shared by `auth login` and by `auth switch` when the target org has no key, so
// both paths behave identically.
//
// Login never mints a key on its own: it either asks, or it was told to by a
// flag.
func runKeySetup(ctx context.Context, c *console.Client, orgID string, mode keySetupMode) error {
	r := rootApp.renderer
	cfg := rootApp.cfg
	name := cfg.OrgName(orgID)

	// An existing working key is left alone. Re-minting on every login would
	// litter the console with keys nobody can attribute.
	if cfg.HasKey(orgID) {
		fmt.Fprintf(r.Err, "using the API key already stored for %s\n", name)
		return nil
	}

	if mode == keySetupPrompt {
		// Check the org for adoptable keys up front, so "use existing" is offered
		// only when there is actually one to adopt. A list failure here is not
		// fatal: fall back to offering just create/skip.
		hasExisting := false
		if console.RevealEnabled {
			if keys, err := c.ListAPIKeys(ctx, orgID); err == nil {
				hasExisting = len(keys) > 0
			} else {
				fmt.Fprintf(r.Err, "could not check %s for existing keys: %v\n", name, err)
			}
		}
		chosen, err := promptKeySetup(r, name, hasExisting)
		if err != nil {
			// No terminal to ask on: skip rather than block, and say what to run.
			mode = keySetupSkip
		} else {
			mode = chosen
		}
	}

	switch mode {
	case keySetupCreate:
		return createAndStoreKey(ctx, c, orgID, defaultKeyName())
	case keySetupExisting:
		return adoptExistingKey(ctx, c, orgID)
	default:
		fmt.Fprintf(r.Err, "no API key stored for %s, run: tabstack keys create --org %s\n", name, orgID)
		return nil
	}
}

// promptKeySetup asks what to do about the missing key. hasExisting reports
// whether the org has an adoptable key, so "use existing" is offered only when
// it can succeed. Returns an error when there is no interactive stdin, which
// callers treat as "skip".
func promptKeySetup(r uiRenderer, orgName string, hasExisting bool) (keySetupMode, error) {
	fmt.Fprintf(r.Err, "\n%s has no API key stored locally.\n", orgName)
	keys := "cn"
	prompt := "Create a new API key now? [C]reate / do [n]othing: "
	if console.RevealEnabled && hasExisting {
		keys = "cne"
		prompt = "Create a new API key now? [C]reate / do [n]othing / use [e]xisting: "
	}

	choice, err := promptChoice(prompt, keys, 'c')
	if err != nil {
		return keySetupPrompt, err
	}
	switch choice {
	case 'c':
		return keySetupCreate, nil
	case 'e':
		return keySetupExisting, nil
	default:
		return keySetupSkip, nil
	}
}

// createAndStoreKey mints a key, stores it against the organisation, and prints
// it exactly once.
func createAndStoreKey(ctx context.Context, c *console.Client, orgID, name string) error {
	r := rootApp.renderer
	cfg := rootApp.cfg

	key, err := c.CreateAPIKey(ctx, orgID, name)
	if err != nil {
		return classifyConsoleError(err)
	}
	if key.APIKey == "" {
		return withCode(3, errors.New("the console created a key but returned no plaintext"))
	}
	if err := validateKeyFormat(key.APIKey); err != nil {
		return withCode(3, fmt.Errorf("the console returned an unusable key: %w", err))
	}

	org := cfg.UpsertOrg(orgID, "")
	org.APIKey = key.APIKey
	org.APIKeyID = key.ID
	org.APIKeyName = key.Name
	if err := rootApp.store.Save(cfg); err != nil {
		return withCode(1, fmt.Errorf("save config: %w", err))
	}

	fmt.Fprintf(r.Out, "\n%s created API key %s for %s\n",
		r.Styles.Success.Render("✓"), key.Name, cfg.OrgName(orgID))
	fmt.Fprintf(r.Out, "%s\n", r.Styles.Box.Render(key.APIKey))
	fmt.Fprintln(r.Out, r.Styles.Muted.Render("This is the only time the key is shown. It has been stored in your config."))
	return nil
}

// adoptExistingKey reveals an existing key and stores it. Only reachable while
// console.RevealEnabled is true.
func adoptExistingKey(ctx context.Context, c *console.Client, orgID string) error {
	if !console.RevealEnabled {
		return withCode(2, errors.New("adopting an existing key is not available in this build; use `tabstack keys create`"))
	}

	r := rootApp.renderer
	cfg := rootApp.cfg

	keys, err := c.ListAPIKeys(ctx, orgID)
	if err != nil {
		return classifyConsoleError(err)
	}
	if len(keys) == 0 {
		// "use existing" was asked for explicitly (a flag, or a prompt that only
		// offers it when a key exists), so an empty list is an error, not a
		// silent switch to creating one.
		return withCode(2, fmt.Errorf("%s has no existing API key to adopt; run: tabstack keys create --org %s",
			cfg.OrgName(orgID), orgID))
	}

	var chosen console.APIKey
	if len(keys) == 1 {
		// Only one candidate: adopt it without asking, so this works on a
		// non-interactive --api-key-setup=existing too.
		chosen = keys[0]
	} else {
		for i, k := range keys {
			fmt.Fprintf(r.Err, "  %d) %s %s\n", i+1, k.Name, r.Styles.Muted.Render(k.Preview))
		}
		line, err := promptLine(fmt.Sprintf("Which key? [1-%d]: ", len(keys)))
		if err != nil {
			return withCode(2, errors.New("choosing among multiple keys requires a terminal; use --api-key-setup=create or run interactively"))
		}
		idx, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr != nil || idx < 1 || idx > len(keys) {
			return withCode(2, fmt.Errorf("not a valid choice: %q", line))
		}
		chosen = keys[idx-1]
	}
	revealed, err := c.RevealAPIKey(ctx, chosen.ID)
	if err != nil {
		return classifyConsoleError(err)
	}

	org := cfg.UpsertOrg(orgID, "")
	org.APIKey = revealed.APIKey
	org.APIKeyID = chosen.ID
	org.APIKeyName = chosen.Name
	if err := rootApp.store.Save(cfg); err != nil {
		return withCode(1, fmt.Errorf("save config: %w", err))
	}

	fmt.Fprintf(r.Out, "%s stored existing key %s %s\n", r.Styles.Success.Render("✓"),
		chosen.Name, r.Styles.Muted.Render(config.Redact(revealed.APIKey)))
	return nil
}

// defaultKeyName names created keys after the machine that created them, so
// revoking the right one in the console is obvious later.
func defaultKeyName() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "cli-unknown-host"
	}
	// Trim the domain: "laptop.local" is noise next to "laptop".
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	return "cli-" + host
}
