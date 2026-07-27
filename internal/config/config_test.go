package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// newTestStore builds a FileStore over a throwaway path with warnings captured.
func newTestStore(t *testing.T) (*FileStore, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	s := NewFileStoreAt(filepath.Join(t.TempDir(), "config.toml"))
	s.Warn = buf
	return s, buf
}

// writeFile drops raw content at the store's path with the given mode.
func writeFile(t *testing.T, s *FileStore, mode os.FileMode, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path(), []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Path(), mode); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	s, _ := newTestStore(t)
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("version = %d, want %d", cfg.Version, CurrentVersion)
	}
	if cfg.Session != nil || cfg.ActiveOrg != "" || len(cfg.Orgs) != 0 {
		t.Errorf("fresh config not empty: %+v", cfg)
	}
}

func TestMigrateLegacyConfig(t *testing.T) {
	// The pre-organisation shape: one global key, a base URL, and no version.
	const legacy = "# tabstack CLI configuration\napi_key = \"legacy-key-1234\"\nbase_url = \"https://base.legacy\"\n"

	s, _ := newTestStore(t)
	writeFile(t, s, 0o600, legacy)

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LegacyAPIKey != "legacy-key-1234" {
		t.Errorf("legacy key = %q, want it moved to LegacyAPIKey", cfg.LegacyAPIKey)
	}
	if cfg.BaseURL != "https://base.legacy" {
		t.Errorf("base url = %q, want it preserved", cfg.BaseURL)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("version = %d, want %d", cfg.Version, CurrentVersion)
	}

	// Loading must not rewrite anything: migration lands on the next save.
	onDisk, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), `api_key = "legacy-key-1234"`) {
		t.Errorf("load rewrote the file: %s", onDisk)
	}

	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	again, err := s.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again.LegacyAPIKey != "legacy-key-1234" {
		t.Errorf("legacy key lost across save/load: %+v", again)
	}
	if again.Version != CurrentVersion {
		t.Errorf("version = %d after reload", again.Version)
	}

	// Idempotent: loading the migrated file again changes nothing.
	third, err := s.Load()
	if err != nil {
		t.Fatalf("third load: %v", err)
	}
	if !reflect.DeepEqual(third, again) {
		t.Errorf("second load differed:\n got %+v\nwant %+v", third, again)
	}
}

func TestMigrationNeverOverwritesExistingLegacyKey(t *testing.T) {
	s, _ := newTestStore(t)
	// A file carrying both shapes: the already-migrated value wins and nothing
	// the user has is discarded.
	writeFile(t, s, 0o600, "api_key = \"old\"\nlegacy_api_key = \"already-migrated\"\n")

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LegacyAPIKey != "already-migrated" {
		t.Errorf("legacy key = %q", cfg.LegacyAPIKey)
	}
}

func TestSaveRoundTripsSessionAndOrgs(t *testing.T) {
	s, _ := newTestStore(t)
	expires := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)

	cfg := &Config{
		AuthURL:   "https://console.example",
		ActiveOrg: "org_a",
		Session: &Session{
			AccessToken:  "tcli_access",
			RefreshToken: "tcli_refresh",
			ExpiresAt:    expires,
			Scope:        "cli offline_access",
			UserEmail:    "user@example.test",
		},
		Orgs: map[string]*OrgCreds{
			"org_a": {Name: "Alpha", APIKey: "key-alpha-1234", APIKeyID: "k1", APIKeyName: "cli-laptop"},
			"org_b": {Name: "Beta"},
		},
	}
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Session == nil || got.Session.AccessToken != "tcli_access" || got.Session.RefreshToken != "tcli_refresh" {
		t.Fatalf("session round trip failed: %+v", got.Session)
	}
	if !got.Session.ExpiresAt.Equal(expires) {
		t.Errorf("expires_at = %v, want %v", got.Session.ExpiresAt, expires)
	}
	if got.Session.UserEmail != "user@example.test" || got.Session.Scope != "cli offline_access" {
		t.Errorf("session fields = %+v", got.Session)
	}
	if got.ActiveOrg != "org_a" || len(got.Orgs) != 2 {
		t.Errorf("orgs round trip failed: %+v", got)
	}
	if got.Orgs["org_a"].APIKey != "key-alpha-1234" || got.Orgs["org_a"].APIKeyID != "k1" {
		t.Errorf("org_a = %+v", got.Orgs["org_a"])
	}
	if got.Orgs["org_b"].APIKey != "" {
		t.Errorf("org_b should have no key: %+v", got.Orgs["org_b"])
	}
}

func TestSavePermissionsAndAtomicity(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Save(&Config{ActiveOrg: "org_a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %#o, want 0600", got)
	}

	dir := filepath.Dir(s.Path())
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("dir mode = %#o, want 0700", got)
	}

	// A successful save leaves no temp file behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".config-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestSaveTightensLoosePermissions(t *testing.T) {
	s, _ := newTestStore(t)
	writeFile(t, s, 0o644, "version = 1\n")

	cfg, _ := s.Load()
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %#o after save, want 0600", got)
	}
}

func TestLoadWarnsOnLoosePermissions(t *testing.T) {
	s, warn := newTestStore(t)
	writeFile(t, s, 0o644, "version = 1\n")

	if _, err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := warn.String()
	if !strings.Contains(out, s.Path()) {
		t.Errorf("warning does not name the path: %q", out)
	}
	if !strings.Contains(out, "chmod 600") {
		t.Errorf("warning does not name the fix: %q", out)
	}
}

func TestLoadDoesNotWarnOnTightPermissions(t *testing.T) {
	s, warn := newTestStore(t)
	writeFile(t, s, 0o600, "version = 1\n")

	if _, err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if warn.Len() != 0 {
		t.Errorf("unexpected warning: %q", warn.String())
	}
}

func TestPermissionsOK(t *testing.T) {
	dir := t.TempDir()
	tight := filepath.Join(dir, "tight")
	loose := filepath.Join(dir, "loose")
	if err := os.WriteFile(tight, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loose, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ok := PermissionsOK(tight); !ok {
		t.Error("0600 reported as not ok")
	}
	if mode, ok := PermissionsOK(loose); ok {
		t.Errorf("0644 reported as ok (mode %#o)", mode)
	}
	if _, ok := PermissionsOK(filepath.Join(dir, "missing")); !ok {
		t.Error("missing file should not be reported as a problem")
	}
}

func TestResolveAPIKeyPrecedence(t *testing.T) {
	withOrgs := func() *Config {
		return &Config{
			ActiveOrg: "org_a",
			Orgs: map[string]*OrgCreds{
				"org_a": {Name: "Alpha", APIKey: "key-alpha"},
				"org_b": {Name: "Beta", APIKey: "key-beta"},
				"org_c": {Name: "Gamma"},
			},
			LegacyAPIKey: "key-legacy",
		}
	}

	cases := []struct {
		name       string
		cfg        *Config
		env        string
		req        KeyRequest
		wantKey    string
		wantSource KeySource
		wantErr    string
	}{
		{
			name:       "flag beats everything",
			cfg:        withOrgs(),
			env:        "key-env",
			req:        KeyRequest{Flag: "key-flag", OrgOverride: "org_b"},
			wantKey:    "key-flag",
			wantSource: SourceFlag,
		},
		{
			name:       "env beats stored keys",
			cfg:        withOrgs(),
			env:        "key-env",
			req:        KeyRequest{OrgOverride: "org_b"},
			wantKey:    "key-env",
			wantSource: SourceEnv,
		},
		{
			name:       "org override beats active org",
			cfg:        withOrgs(),
			req:        KeyRequest{OrgOverride: "org_b"},
			wantKey:    "key-beta",
			wantSource: SourceOrgOverride,
		},
		{
			name:       "active org beats legacy",
			cfg:        withOrgs(),
			req:        KeyRequest{},
			wantKey:    "key-alpha",
			wantSource: SourceActiveOrg,
		},
		{
			name:       "legacy only when no active org",
			cfg:        &Config{LegacyAPIKey: "key-legacy"},
			req:        KeyRequest{},
			wantKey:    "key-legacy",
			wantSource: SourceLegacy,
		},
		{
			name: "legacy ignored once the active org has a key",
			cfg: &Config{
				ActiveOrg:    "org_a",
				Orgs:         map[string]*OrgCreds{"org_a": {Name: "Alpha", APIKey: "key-alpha"}},
				LegacyAPIKey: "key-legacy",
			},
			req:        KeyRequest{},
			wantKey:    "key-alpha",
			wantSource: SourceActiveOrg,
		},
		{
			name:    "org override without a key errors, never borrows another org's key",
			cfg:     withOrgs(),
			req:     KeyRequest{OrgOverride: "org_c"},
			wantErr: "tabstack keys create --org org_c",
		},
		{
			name:    "active org without a key errors even with a legacy key present",
			cfg:     &Config{ActiveOrg: "org_c", Orgs: map[string]*OrgCreds{"org_c": {Name: "Gamma"}}, LegacyAPIKey: "key-legacy"},
			wantErr: "tabstack keys create --org org_c",
		},
		{
			name:    "nothing at all",
			cfg:     &Config{},
			wantErr: "no API key found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envAPIKey, tc.env)

			got, err := tc.cfg.ResolveAPIKey(tc.req)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got key %q", tc.wantErr, got.APIKey)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				if got.APIKey != "" {
					t.Errorf("a failed resolution returned a key: %q", got.APIKey)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveAPIKey: %v", err)
			}
			if got.APIKey != tc.wantKey {
				t.Errorf("key = %q, want %q", got.APIKey, tc.wantKey)
			}
			if got.Source != tc.wantSource {
				t.Errorf("source = %q, want %q", got.Source, tc.wantSource)
			}
		})
	}
}

func TestResolveAPIKeyReportsEnvOverride(t *testing.T) {
	t.Setenv(envAPIKey, "key-env")
	cfg := &Config{ActiveOrg: "org_a", Orgs: map[string]*OrgCreds{"org_a": {Name: "Alpha", APIKey: "key-alpha"}}}

	got, err := cfg.ResolveAPIKey(KeyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.EnvOverriding {
		t.Error("EnvOverriding should be true when the env var wins")
	}
}

func TestResolveURLs(t *testing.T) {
	cases := []struct {
		name     string
		cfg      *Config
		envBase  string
		envAuth  string
		flagBase string
		flagAuth string
		wantBase string
		wantAuth string
	}{
		{
			name:     "defaults",
			cfg:      &Config{},
			wantBase: DefaultBaseURL,
			wantAuth: DefaultAuthURL,
		},
		{
			name:     "file overrides defaults",
			cfg:      &Config{BaseURL: "https://base.file", AuthURL: "https://auth.file"},
			wantBase: "https://base.file",
			wantAuth: "https://auth.file",
		},
		{
			name:     "env overrides file",
			cfg:      &Config{BaseURL: "https://base.file", AuthURL: "https://auth.file"},
			envBase:  "https://base.env",
			envAuth:  "https://auth.env",
			wantBase: "https://base.env",
			wantAuth: "https://auth.env",
		},
		{
			name:     "flag overrides env",
			cfg:      &Config{BaseURL: "https://base.file"},
			envBase:  "https://base.env",
			envAuth:  "https://auth.env",
			flagBase: "https://base.flag",
			flagAuth: "https://auth.flag",
			wantBase: "https://base.flag",
			wantAuth: "https://auth.flag",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envBaseURL, tc.envBase)
			t.Setenv(envAuthURL, tc.envAuth)

			if got := tc.cfg.ResolveBaseURL(tc.flagBase); got != tc.wantBase {
				t.Errorf("base = %q, want %q", got, tc.wantBase)
			}
			if got := tc.cfg.ResolveAuthURL(tc.flagAuth); got != tc.wantAuth {
				t.Errorf("auth = %q, want %q", got, tc.wantAuth)
			}
		})
	}
}

func TestSessionExpired(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		sess *Session
		want bool
	}{
		{"nil session", nil, true},
		{"no token", &Session{}, true},
		{"future expiry", &Session{AccessToken: "t", ExpiresAt: now.Add(time.Hour)}, false},
		{"past expiry", &Session{AccessToken: "t", ExpiresAt: now.Add(-time.Minute)}, true},
		{"inside skew window", &Session{AccessToken: "t", ExpiresAt: now.Add(30 * time.Second)}, true},
		{"no expiry recorded", &Session{AccessToken: "t"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sess.Expired(now, time.Minute); got != tc.want {
				t.Errorf("Expired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOrgHelpers(t *testing.T) {
	cfg := &Config{}
	org := cfg.UpsertOrg("org_a", "Alpha")
	org.APIKey = "key-alpha"

	if !cfg.HasKey("org_a") {
		t.Error("HasKey should be true after storing a key")
	}
	if cfg.HasKey("org_zz") {
		t.Error("HasKey should be false for an unknown org")
	}
	if got := cfg.OrgName("org_a"); got != "Alpha" {
		t.Errorf("OrgName = %q", got)
	}
	if got := cfg.OrgName("org_zz"); got != "org_zz" {
		t.Errorf("unknown OrgName = %q, want the id back", got)
	}

	// Upserting again keeps the key and updates the name.
	cfg.UpsertOrg("org_a", "Alpha Renamed")
	if cfg.Orgs["org_a"].APIKey != "key-alpha" {
		t.Error("upsert dropped the stored key")
	}
	if cfg.Orgs["org_a"].Name != "Alpha Renamed" {
		t.Errorf("name = %q", cfg.Orgs["org_a"].Name)
	}
	// An empty name must not blank out a known one.
	cfg.UpsertOrg("org_a", "")
	if cfg.Orgs["org_a"].Name != "Alpha Renamed" {
		t.Errorf("empty upsert overwrote the name: %q", cfg.Orgs["org_a"].Name)
	}
}

func TestRedact(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"short", "*****"},
		{"tcli_abcdefghijklmnop", "tcli…mnop"},
	}
	for _, tc := range cases {
		if got := Redact(tc.in); got != tc.want {
			t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := Redact("supersecretvalue12345"); strings.Contains(got, "secretvalue") {
		t.Errorf("Redact leaked the middle: %q", got)
	}
}

func TestConfigPathUsesXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "tabstack", "config.toml"); path != want {
		t.Errorf("ConfigPath = %q, want %q", path, want)
	}

	schemaDir, err := SchemasDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "tabstack", "schemas"); schemaDir != want {
		t.Errorf("SchemasDir = %q, want %q", schemaDir, want)
	}
}
