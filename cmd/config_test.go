package cmd

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

func TestConfigPathPrintsTheStorePath(t *testing.T) {
	isolate(t)
	buf := setTestAppPretty(t)

	cmd := newConfigPathCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("config path: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != rootApp.store.Path() {
		t.Errorf("output = %q, want %q", got, rootApp.store.Path())
	}
}

func TestConfigShowRedactsEverySecret(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "")
	buf := setTestAppPretty(t)

	cfg := rootApp.cfg
	cfg.Session = &config.Session{
		AccessToken:  "tcli_accesstokenvalue",
		RefreshToken: "rt_refreshtokenvalue",
		ExpiresAt:    time.Now().Add(48 * time.Hour),
		Scope:        "cli offline_access",
		UserEmail:    "user@example.test",
	}
	cfg.ActiveOrg = "org_a"
	a := cfg.UpsertOrg("org_a", "Alpha")
	a.APIKey = "key-alpha-plaintext"
	a.APIKeyID = "k1"
	a.APIKeyName = "cli-laptop"
	cfg.UpsertOrg("org_b", "Bravo")
	cfg.LegacyAPIKey = "key-legacy-plaintext"

	cmd := newConfigShowCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("config show: %v", err)
	}
	out := buf.String()

	// Every org is listed, keyed or not, with its key state.
	for _, want := range []string{
		"Alpha", "org_a", "Bravo", "org_b",
		"cli-laptop", "no key stored",
		"user@example.test", "cli offline_access",
		"version:", "permissions:", "base url:", "auth url:",
		"legacy key:", "drop-legacy-key",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// No secret appears in full, in any field.
	for _, secret := range []string{
		"key-alpha-plaintext", "key-legacy-plaintext",
		"tcli_accesstokenvalue", "rt_refreshtokenvalue",
	} {
		if strings.Contains(out, secret) {
			t.Errorf("config show leaked %q:\n%s", secret, out)
		}
	}
}

func TestConfigShowOnAFreshInstall(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "")
	buf := setTestAppPretty(t)

	cmd := newConfigShowCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("config show: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"session:", "none", "orgs:", "none stored"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestConfigShowFlagsTheEnvOverrideAndLoosePermissions(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "key-from-env")
	buf := setTestAppPretty(t)

	if err := rootApp.store.Save(rootApp.cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(rootApp.store.Path(), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newConfigShowCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("config show: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "should be 0600") {
		t.Errorf("no permission warning:\n%s", out)
	}
	if !strings.Contains(out, config.EnvAPIKey) {
		t.Errorf("no env override notice:\n%s", out)
	}
}

func TestConfigShowSaysWhenTheLegacyKeyIsInUse(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "")
	buf := setTestAppPretty(t)
	rootApp.cfg.LegacyAPIKey = "key-legacy-plaintext"

	cmd := newConfigShowCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("config show: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "in use, because no organisation is active") {
		t.Errorf("legacy key state not explained:\n%s", out)
	}
	// With nothing to fall back on, do not advertise removing it.
	if strings.Contains(out, "drop-legacy-key") {
		t.Errorf("suggested dropping the only working credential:\n%s", out)
	}
}

func TestDropLegacyKey(t *testing.T) {
	cases := []struct {
		name       string
		activeOrg  string
		orgHasKey  bool
		legacy     string
		force      bool
		wantCode   int
		wantDrop   bool
		wantOutput string
	}{
		{
			name:       "nothing to drop",
			legacy:     "",
			wantOutput: "nothing to do",
		},
		{
			name:       "drops once the active org has a key",
			activeOrg:  "org_a",
			orgHasKey:  true,
			legacy:     "key-legacy-plaintext",
			wantDrop:   true,
			wantOutput: "legacy API key removed",
		},
		{
			name:      "refuses while no org is active",
			legacy:    "key-legacy-plaintext",
			wantCode:  2,
			activeOrg: "",
		},
		{
			name:      "refuses while the active org has no key",
			activeOrg: "org_a",
			legacy:    "key-legacy-plaintext",
			wantCode:  2,
		},
		{
			name:      "force overrides the guard",
			activeOrg: "org_a",
			legacy:    "key-legacy-plaintext",
			force:     true,
			wantDrop:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			buf := setTestAppPretty(t)
			cfg := rootApp.cfg
			cfg.LegacyAPIKey = tc.legacy
			cfg.ActiveOrg = tc.activeOrg
			if tc.activeOrg != "" {
				org := cfg.UpsertOrg(tc.activeOrg, "Alpha")
				if tc.orgHasKey {
					org.APIKey = "key-alpha-1234"
				}
			}

			cmd := newConfigDropLegacyKeyCmd()
			cmd.SetContext(context.Background())
			if tc.force {
				if err := cmd.Flags().Set("force", "true"); err != nil {
					t.Fatal(err)
				}
			}

			err := cmd.RunE(cmd, nil)
			if tc.wantCode != 0 {
				if codeOf(err) != tc.wantCode {
					t.Fatalf("err = %v, want exit code %d", err, tc.wantCode)
				}
				if cfg.LegacyAPIKey == "" {
					t.Error("a refused drop still removed the key")
				}
				if !strings.Contains(err.Error(), "--force") {
					t.Errorf("error does not mention the override: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("drop-legacy-key: %v", err)
			}

			if tc.wantDrop {
				if cfg.LegacyAPIKey != "" {
					t.Errorf("legacy key still present: %q", cfg.LegacyAPIKey)
				}
				saved, loadErr := rootApp.store.Load()
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if saved.LegacyAPIKey != "" {
					t.Errorf("legacy key survived on disk: %q", saved.LegacyAPIKey)
				}
			}
			if tc.wantOutput != "" && !strings.Contains(buf.String(), tc.wantOutput) {
				t.Errorf("output = %q, want it to mention %q", buf.String(), tc.wantOutput)
			}
			// Dropping the legacy key must never touch an org's key.
			if tc.orgHasKey && !cfg.HasKey(tc.activeOrg) {
				t.Error("dropping the legacy key removed the org key")
			}
		})
	}
}

func TestConfigCommandsAreRegistered(t *testing.T) {
	sub := map[string]bool{}
	for _, c := range newConfigCmd().Commands() {
		sub[c.Name()] = true
	}
	for _, want := range []string{"path", "show", "drop-legacy-key"} {
		if !sub[want] {
			t.Errorf("config %s not registered", want)
		}
	}
}

// TestConfigShowJSONRedactsEverything is the security-relevant half of the new
// JSON mode: adding a machine-readable output must not become a way to read a
// credential out of the config in full.
func TestConfigShowJSONRedactsEverything(t *testing.T) {
	isolate(t)
	setTestApp(t)
	cfg := rootApp.cfg
	cfg.ActiveOrg = "org_a"
	org := cfg.UpsertOrg("org_a", "Alpha")
	org.APIKey = "ts-secret-plaintext-key-value"
	org.APIKeyID = "k1"
	cfg.LegacyAPIKey = "ts-legacy-plaintext-value"
	cfg.Session = &config.Session{
		AccessToken:  "at-secret-plaintext-token",
		RefreshToken: "rt-secret-plaintext-token",
		UserEmail:    "user@example.test",
	}

	out := configJSON(cfg, rootApp.store.Path())
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, secret := range []string{
		"ts-secret-plaintext-key-value",
		"ts-legacy-plaintext-value",
		"at-secret-plaintext-token",
		"rt-secret-plaintext-token",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("config show --output json leaked a secret: %s", secret)
		}
	}
	// The refresh token has no field at all, redacted or otherwise.
	if strings.Contains(body, "refresh") {
		t.Errorf("refresh token should not appear in the JSON shape: %s", body)
	}
	if len(out.Orgs) != 1 || !out.Orgs[0].Active || !out.Orgs[0].KeyStored {
		t.Errorf("org row wrong: %+v", out.Orgs)
	}
}

// TestAuthStatusJSONRedactsTheKey guards the same property for auth status.
func TestAuthStatusJSONRedactsTheKey(t *testing.T) {
	isolate(t)
	setTestApp(t)
	cfg := rootApp.cfg
	cfg.ActiveOrg = "org_a"
	org := cfg.UpsertOrg("org_a", "Alpha")
	org.APIKey = "ts-secret-plaintext-key-value"

	raw, err := json.Marshal(authStatusJSON(cfg, "/tmp/x/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "ts-secret-plaintext-key-value") {
		t.Errorf("auth status --output json leaked the key: %s", raw)
	}
	if !strings.Contains(string(raw), `"api_key_stored":true`) {
		t.Errorf("api_key_stored not reported: %s", raw)
	}
}
