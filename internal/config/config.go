// Package config owns credential and endpoint resolution for the CLI.
//
// There are two credentials in play and they are deliberately never conflated:
//
//   - the session (an OAuth access token) is user scoped and only ever sent to
//     the auth host (console.tabstack.ai);
//   - an API key is organisation scoped and only ever sent to the product host
//     (api.tabstack.ai).
//
// Because API keys are org scoped, the on-disk shape keys them by organisation
// id: one user can belong to several orgs and hold a different key in each.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	envAPIKey  = "TABSTACK_API_KEY"
	envBaseURL = "TABSTACK_BASE_URL"
	envAuthURL = "TABSTACK_AUTH_URL"

	// EnvAPIKey is exported so `auth status` can name the variable it is
	// reporting on without re-declaring the string.
	EnvAPIKey = envAPIKey

	// DefaultBaseURL is the production product API root. Every extract,
	// generate, automate, and research endpoint hangs off this.
	DefaultBaseURL = "https://api.tabstack.ai/v1"

	// DefaultAuthURL is the auth and management host. OAuth and every /cli/*
	// management endpoint hangs off this. The session token goes here and
	// nowhere else.
	DefaultAuthURL = "https://console.tabstack.ai"

	// CurrentVersion is the schema version written into the config file. A file
	// with no version predates org-scoped keys and gets migrated on load.
	CurrentVersion = 1
)

// Config is the on-disk configuration, decoded as TOML.
//
// Field order matters for encoding: TOML tables must follow the scalars of the
// table they sit in, so Session and Orgs are declared last. Reordering them
// above a scalar would emit a file that no longer parses.
type Config struct {
	AuthURL      string `toml:"auth_url,omitempty"`
	BaseURL      string `toml:"base_url,omitempty"`
	ActiveOrg    string `toml:"active_org,omitempty"`
	LegacyAPIKey string `toml:"legacy_api_key,omitempty"`
	Version      int    `toml:"version"`

	Session *Session             `toml:"session,omitempty"`
	Orgs    map[string]*OrgCreds `toml:"orgs,omitempty"`
}

// Session is the OAuth session: user scoped, auth host only.
type Session struct {
	AccessToken  string    `toml:"access_token"`
	RefreshToken string    `toml:"refresh_token"`
	ExpiresAt    time.Time `toml:"expires_at"`
	Scope        string    `toml:"scope,omitempty"`
	UserEmail    string    `toml:"user_email,omitempty"`
}

// OrgCreds is one organisation's product credential. Name is display only and
// can change server-side, which is why Orgs is keyed by organisation id.
type OrgCreds struct {
	Name       string `toml:"name"`
	APIKey     string `toml:"api_key,omitempty"`
	APIKeyID   string `toml:"api_key_id,omitempty"`
	APIKeyName string `toml:"api_key_name,omitempty"`
}

// Expired reports whether the access token is past its expiry, allowing for a
// skew window so we refresh slightly early rather than racing the server.
func (s *Session) Expired(now time.Time, skew time.Duration) bool {
	if s == nil || s.AccessToken == "" {
		return true
	}
	if s.ExpiresAt.IsZero() {
		return false
	}
	return !now.Add(skew).Before(s.ExpiresAt)
}

// CredentialStore is the whole surface commands use to read and write
// credentials. Everything goes through it so an OS keychain implementation can
// be added later without touching command code.
type CredentialStore interface {
	Load() (*Config, error)
	Save(*Config) error
	Path() string
}

// FileStore is the shipped CredentialStore: a single TOML file, 0600, inside a
// 0700 directory.
type FileStore struct {
	path string

	// Warn is where permission warnings go. Defaults to os.Stderr; tests
	// substitute a buffer.
	Warn io.Writer
}

// NewFileStore builds a store over the default config path.
func NewFileStore() (*FileStore, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return NewFileStoreAt(path), nil
}

// NewFileStoreAt builds a store over an explicit path. Used by tests and by
// anything that needs to point at a throwaway config.
func NewFileStoreAt(path string) *FileStore {
	return &FileStore{path: path, Warn: os.Stderr}
}

// Path returns the file the store reads and writes.
func (f *FileStore) Path() string { return f.path }

// legacyFields carries the pre-org shape alongside the current one so a single
// decode can spot an old file. The legacy config held exactly one API key at
// the top level, under `api_key`.
type legacyFields struct {
	APIKey string `toml:"api_key"`
}

// Load reads and decodes the config. A missing file is not an error: it yields
// an empty config at the current version, which is what a fresh install looks
// like.
//
// Migration is applied in memory only. Nothing is written until the next
// successful Save, so simply running a read-only command never rewrites a
// user's file.
func (f *FileStore) Load() (*Config, error) {
	cfg := &Config{Version: CurrentVersion}

	data, err := os.ReadFile(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, err
	}

	f.checkPerms()

	var onDisk Config
	if err := toml.Unmarshal(data, &onDisk); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.path, err)
	}
	var legacy legacyFields
	if err := toml.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.path, err)
	}

	cfg = &onDisk
	migrate(cfg, legacy)
	return cfg, nil
}

// migrate brings an older file up to CurrentVersion in memory. The only
// migration so far is version 0 (one global API key) to version 1 (keys held
// per organisation): the old key moves to LegacyAPIKey, which keeps working
// until the user logs in and an org key takes over. Nothing is discarded.
func migrate(cfg *Config, legacy legacyFields) {
	if cfg.Version == 0 {
		if legacy.APIKey != "" && cfg.LegacyAPIKey == "" {
			cfg.LegacyAPIKey = legacy.APIKey
		}
		cfg.Version = CurrentVersion
	}
	if cfg.Version < CurrentVersion {
		cfg.Version = CurrentVersion
	}
}

// PermissionsOK reports a config file's permission bits and whether they are
// owner-only. A missing file counts as fine: there is nothing exposed yet.
func PermissionsOK(path string) (os.FileMode, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, true
	}
	mode := info.Mode().Perm()
	return mode, mode&0o077 == 0
}

// checkPerms warns when the config is readable by anyone other than its owner.
// It is a warning and not a refusal: locking a user out of their own CLI over
// file modes is worse than telling them how to fix it.
func (f *FileStore) checkPerms() {
	mode, ok := PermissionsOK(f.path)
	if ok {
		return
	}
	w := f.Warn
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "warning: %s is group/world readable (%#o); it holds credentials.\n", f.path, mode)
	fmt.Fprintf(w, "         fix with: chmod 600 %s\n", f.path)
}

// Save writes the config atomically: a temp file in the same directory, chmod
// 0600 before any content is visible under the real name, then a rename. A
// half-written credential file is never observable, and the rename cannot land
// on a different filesystem.
func (f *FileStore) Save(cfg *Config) error {
	if cfg == nil {
		return errors.New("save: nil config")
	}
	cfg.Version = CurrentVersion
	pruneOrgs(cfg)

	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	header := []byte("# tabstack CLI configuration\n")

	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// MkdirAll only applies the mode to directories it creates. Tighten an
	// existing one too: a 0755 directory holding a 0600 credential file still
	// leaks the file's existence and lets a co-tenant watch for it.
	if info, err := os.Stat(dir); err == nil && info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}

	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		// No-op once the rename has succeeded; cleans up on every failure path.
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(header, data...)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, f.path)
}

// pruneOrgs drops org entries that carry nothing worth persisting, so a failed
// lookup does not leave empty tables behind.
func pruneOrgs(cfg *Config) {
	for id, o := range cfg.Orgs {
		if o == nil {
			delete(cfg.Orgs, id)
		}
	}
	if len(cfg.Orgs) == 0 {
		cfg.Orgs = nil
	}
}

// UpsertOrg records or updates an organisation's display name without touching
// its stored key.
func (c *Config) UpsertOrg(id, name string) *OrgCreds {
	if c.Orgs == nil {
		c.Orgs = map[string]*OrgCreds{}
	}
	o, ok := c.Orgs[id]
	if !ok {
		o = &OrgCreds{}
		c.Orgs[id] = o
	}
	if name != "" {
		o.Name = name
	}
	return o
}

// Org returns the stored credentials for an organisation id.
func (c *Config) Org(id string) *OrgCreds {
	if id == "" || c.Orgs == nil {
		return nil
	}
	return c.Orgs[id]
}

// OrgName returns an organisation's display name, falling back to the id when
// we have never seen a name for it.
func (c *Config) OrgName(id string) string {
	if o := c.Org(id); o != nil && o.Name != "" {
		return o.Name
	}
	return id
}

// HasKey reports whether an organisation has a stored API key.
func (c *Config) HasKey(id string) bool {
	o := c.Org(id)
	return o != nil && o.APIKey != ""
}

// ResolveBaseURL returns the product API root: config file, then environment,
// then flag, each overriding the last.
func (c *Config) ResolveBaseURL(flag string) string {
	url := DefaultBaseURL
	if c.BaseURL != "" {
		url = c.BaseURL
	}
	if v := os.Getenv(envBaseURL); v != "" {
		url = v
	}
	if flag != "" {
		url = flag
	}
	return url
}

// ResolveAuthURL returns the auth and management host, with the same precedence
// as ResolveBaseURL.
func (c *Config) ResolveAuthURL(flag string) string {
	url := DefaultAuthURL
	if c.AuthURL != "" {
		url = c.AuthURL
	}
	if v := os.Getenv(envAuthURL); v != "" {
		url = v
	}
	if flag != "" {
		url = flag
	}
	return url
}

// KeySource describes where a resolved API key came from, so `auth status` can
// explain the resolution without ever printing the key.
type KeySource string

const (
	SourceFlag        KeySource = "flag"
	SourceEnv         KeySource = "environment"
	SourceOrgOverride KeySource = "--org override"
	SourceActiveOrg   KeySource = "active org"
	SourceLegacy      KeySource = "legacy config key"
	SourceNone        KeySource = "unset"
)

// KeyRequest is the per-invocation input to API key resolution.
type KeyRequest struct {
	// Flag is the value of --api-key/--key ("" when unset).
	Flag string
	// OrgOverride is an already-resolved organisation id from --org ("" when
	// unset). It selects which stored key to use for this invocation only and
	// never mutates config.
	OrgOverride string
}

// KeyResolution is the outcome of resolving a product credential.
type KeyResolution struct {
	APIKey string
	Source KeySource
	// OrgID is the organisation the key belongs to, when it came from stored
	// per-org credentials. Empty for flag, env, and legacy keys.
	OrgID   string
	OrgName string
	// EnvOverriding is true when TABSTACK_API_KEY won, which means the active
	// org is not authoritative for product calls this invocation.
	EnvOverriding bool
}

// ErrNoAPIKey is returned when no credential can be resolved at all.
var ErrNoAPIKey = errors.New("no API key found")

// ResolveAPIKey picks the product credential for this invocation. It is the one
// place precedence is decided, so every command resolves identically:
//
//  1. --key/--api-key flag
//  2. TABSTACK_API_KEY
//  3. the stored key for the --org override, when given
//  4. the stored key for the active org
//  5. LegacyAPIKey, only while no active org is set
//
// A --org override that has no stored key is an error, never a fallback. Using
// org A's credential while the user believes they are acting as org B is the
// worst failure available here, so it is made impossible rather than unlikely.
func (c *Config) ResolveAPIKey(req KeyRequest) (KeyResolution, error) {
	envKey := os.Getenv(envAPIKey)

	if req.Flag != "" {
		return KeyResolution{APIKey: req.Flag, Source: SourceFlag, EnvOverriding: false}, nil
	}
	if envKey != "" {
		return KeyResolution{APIKey: envKey, Source: SourceEnv, EnvOverriding: true}, nil
	}

	if req.OrgOverride != "" {
		id := req.OrgOverride
		name := c.OrgName(id)
		if !c.HasKey(id) {
			return KeyResolution{}, fmt.Errorf("no API key stored for %s. Run: tabstack keys create --org %s", name, id)
		}
		return KeyResolution{
			APIKey:  c.Orgs[id].APIKey,
			Source:  SourceOrgOverride,
			OrgID:   id,
			OrgName: name,
		}, nil
	}

	if c.ActiveOrg != "" {
		id := c.ActiveOrg
		name := c.OrgName(id)
		if c.HasKey(id) {
			return KeyResolution{
				APIKey:  c.Orgs[id].APIKey,
				Source:  SourceActiveOrg,
				OrgID:   id,
				OrgName: name,
			}, nil
		}
		return KeyResolution{}, fmt.Errorf("no API key stored for %s. Run: tabstack keys create --org %s", name, id)
	}

	if c.LegacyAPIKey != "" {
		return KeyResolution{APIKey: c.LegacyAPIKey, Source: SourceLegacy}, nil
	}

	return KeyResolution{}, ErrNoAPIKey
}

// configHome returns the tabstack config directory, following the XDG base
// directory spec and falling back to ~/.config when XDG_CONFIG_HOME is unset.
func configHome() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "tabstack"), nil
}

// ConfigPath returns the path the config file is read from and written to.
func ConfigPath() (string, error) {
	home, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "config.toml"), nil
}

// SchemasDir returns the default directory pre-defined schemas are pulled into
// (`schema pull`). It sits alongside the config file under the tabstack config
// home. The `--storage` flag overrides it per invocation.
func SchemasDir() (string, error) {
	home, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "schemas"), nil
}

// Redact shortens a secret for display: first four and last four characters,
// elided in the middle. Anything too short to redact meaningfully collapses
// entirely, so a short token cannot be reconstructed from its own preview.
func Redact(s string) string {
	const keep = 4
	if len(s) <= keep*2+2 {
		if s == "" {
			return ""
		}
		return strings.Repeat("*", len(s))
	}
	return s[:keep] + "…" + s[len(s)-keep:]
}
