package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Source describes where a resolved API key came from. Useful for `auth status`
// so we can tell the user how their key is being picked up without ever
// printing the key itself.
type Source string

const (
	SourceFlag Source = "flag"
	SourceEnv  Source = "environment"
	SourceFile Source = "config file"
	SourceNone Source = "unset"
)

const (
	envAPIKey  = "TABSTACK_API_KEY"
	envBaseURL = "TABSTACK_BASE_URL"
	envAuthURL = "TABSTACK_AUTH_URL"

	// DefaultBaseURL is the production API root. Every endpoint hangs off this.
	DefaultBaseURL = "https://api.tabstack.ai/v1"

	// DefaultAuthURL is the console: the OAuth authorization server plus the
	// /cli/* management API. Distinct from the product API in DefaultBaseURL.
	DefaultAuthURL = "https://console.tabstack.ai"
)

// Config is the resolved runtime configuration for a single CLI invocation.
type Config struct {
	APIKey    string
	KeySource Source
	BaseURL   string
	AuthURL   string

	// Session (OAuth) state. The session authenticates the console management
	// API; it is never sent to the product API.
	SessionToken  string
	RefreshToken  string
	SessionExpiry time.Time

	// APIKeyID identifies the stored product key server-side so `auth status`
	// can tell whether it still exists. DefaultOrg is the org chosen at consent.
	APIKeyID   string
	DefaultOrg string
}

// File is the on-disk shape. We keep it tiny and TOML-ish, but to avoid pulling
// in a TOML dependency we hand-parse a trivial `key = "value"` format. If this
// grows, swap in a real parser.
type File struct {
	APIKey        string
	BaseURL       string
	AuthURL       string
	SessionToken  string
	RefreshToken  string
	SessionExpiry time.Time
	APIKeyID      string
	DefaultOrg    string
}

// Resolve builds the effective config from the three sources in priority
// order: explicit flag, then environment, then the config file. baseURLFlag
// and apiKeyFlag are the raw values from cobra (empty string means "not set").
func Resolve(apiKeyFlag, baseURLFlag string) (Config, error) {
	return ResolveWithAuth(apiKeyFlag, baseURLFlag, "")
}

// ResolveWithAuth is Resolve plus the console auth URL, which follows the same
// flag > env > file > default precedence.
func ResolveWithAuth(apiKeyFlag, baseURLFlag, authURLFlag string) (Config, error) {
	cfg := Config{
		BaseURL:   DefaultBaseURL,
		AuthURL:   DefaultAuthURL,
		KeySource: SourceNone,
	}

	// Load the file first so flag/env can override it.
	fc, err := LoadFile()
	if err != nil {
		return cfg, err
	}
	if fc.APIKey != "" {
		cfg.APIKey = fc.APIKey
		cfg.KeySource = SourceFile
	}
	if fc.BaseURL != "" {
		cfg.BaseURL = fc.BaseURL
	}
	if fc.AuthURL != "" {
		cfg.AuthURL = fc.AuthURL
	}
	cfg.SessionToken = fc.SessionToken
	cfg.RefreshToken = fc.RefreshToken
	cfg.SessionExpiry = fc.SessionExpiry
	cfg.APIKeyID = fc.APIKeyID
	cfg.DefaultOrg = fc.DefaultOrg

	// Environment overrides file.
	if v := os.Getenv(envAPIKey); v != "" {
		cfg.APIKey = v
		cfg.KeySource = SourceEnv
	}
	if v := os.Getenv(envBaseURL); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv(envAuthURL); v != "" {
		cfg.AuthURL = v
	}

	// Flags override everything.
	if apiKeyFlag != "" {
		cfg.APIKey = apiKeyFlag
		cfg.KeySource = SourceFlag
	}
	if baseURLFlag != "" {
		cfg.BaseURL = baseURLFlag
	}
	if authURLFlag != "" {
		cfg.AuthURL = authURLFlag
	}

	return cfg, nil
}

// configHome returns the tabstack config directory. We follow the XDG base
// directory spec, falling back to ~/.config when XDG_CONFIG_HOME is unset.
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

// ConfigPath returns the path we read the config file from and write it to.
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

// LoadFile reads and parses the config file. A missing file is not an error,
// it just yields an empty File.
func LoadFile() (File, error) {
	var fc File

	path, err := ConfigPath()
	if err != nil {
		return fc, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fc, nil
		}
		return fc, err
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// Values are JSON-encoded strings. Try to decode; fall back to bare-quote strip for legacy files.
		var decoded string
		if json.Unmarshal([]byte(v), &decoded) == nil {
			v = decoded
		} else {
			v = strings.Trim(v, `"`)
		}
		switch k {
		case "api_key":
			fc.APIKey = v
		case "base_url":
			fc.BaseURL = v
		case "auth_url":
			fc.AuthURL = v
		case "session_token":
			fc.SessionToken = v
		case "refresh_token":
			fc.RefreshToken = v
		case "session_expiry":
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				fc.SessionExpiry = t
			}
		case "api_key_id":
			fc.APIKeyID = v
		case "default_org":
			fc.DefaultOrg = v
		}
	}
	return fc, nil
}

// Save writes the API key (and base URL if non-default) to the config file with
// 0600 permissions, preserving any session already stored so signing in and then
// setting a key by hand does not silently log you out.
func Save(apiKey, baseURL string) error {
	fc, err := LoadFile()
	if err != nil {
		return err
	}
	fc.APIKey = apiKey
	if baseURL != "" {
		fc.BaseURL = baseURL
	}
	return SaveFile(fc)
}

// SaveFile writes the whole config file with 0600 permissions, creating the
// parent directory as needed. Empty fields are omitted rather than written blank.
func SaveFile(fc File) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# tabstack CLI configuration\n")

	write := func(key, value string) error {
		if value == "" {
			return nil
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode %s: %w", key, err)
		}
		b.WriteString(key + " = " + string(encoded) + "\n")
		return nil
	}

	if err := write("api_key", fc.APIKey); err != nil {
		return err
	}
	if err := write("api_key_id", fc.APIKeyID); err != nil {
		return err
	}
	// Only persist the URLs when they differ from the built-in defaults, so the
	// file stays readable and upgrades pick up new defaults.
	if fc.BaseURL != DefaultBaseURL {
		if err := write("base_url", fc.BaseURL); err != nil {
			return err
		}
	}
	if fc.AuthURL != DefaultAuthURL {
		if err := write("auth_url", fc.AuthURL); err != nil {
			return err
		}
	}
	if err := write("session_token", fc.SessionToken); err != nil {
		return err
	}
	if err := write("refresh_token", fc.RefreshToken); err != nil {
		return err
	}
	if !fc.SessionExpiry.IsZero() {
		if err := write("session_expiry", fc.SessionExpiry.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	if err := write("default_org", fc.DefaultOrg); err != nil {
		return err
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	// WriteFile only sets 0600 on newly created files; existing files retain
	// their original permissions. Explicitly chmod to enforce the restriction.
	return os.Chmod(path, 0o600)
}
