package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	// DefaultBaseURL is the production API root. Every endpoint hangs off this.
	DefaultBaseURL = "https://api.tabstack.ai/v1"
)

// Config is the resolved runtime configuration for a single CLI invocation.
type Config struct {
	APIKey    string
	KeySource Source
	BaseURL   string
}

// fileConfig is the on-disk shape. We keep it tiny and TOML-ish, but to avoid
// pulling in a TOML dependency for two fields we hand-parse a trivial
// `key = "value"` format. If this grows, swap in a real parser.
type fileConfig struct {
	APIKey  string
	BaseURL string
}

// Resolve builds the effective config from the three sources in priority
// order: explicit flag, then environment, then the config file. baseURLFlag
// and apiKeyFlag are the raw values from cobra (empty string means "not set").
func Resolve(apiKeyFlag, baseURLFlag string) (Config, error) {
	cfg := Config{
		BaseURL:   DefaultBaseURL,
		KeySource: SourceNone,
	}

	// Load the file first so flag/env can override it.
	fc, err := loadFile()
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

	// Environment overrides file.
	if v := os.Getenv(envAPIKey); v != "" {
		cfg.APIKey = v
		cfg.KeySource = SourceEnv
	}
	if v := os.Getenv(envBaseURL); v != "" {
		cfg.BaseURL = v
	}

	// Flags override everything.
	if apiKeyFlag != "" {
		cfg.APIKey = apiKeyFlag
		cfg.KeySource = SourceFlag
	}
	if baseURLFlag != "" {
		cfg.BaseURL = baseURLFlag
	}

	return cfg, nil
}

// ConfigPath returns the path we read from and write to. We follow the XDG
// base directory spec, falling back to ~/.config when XDG_CONFIG_HOME is unset.
func ConfigPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "tabstack", "config.toml"), nil
}

// loadFile reads and parses the config file. A missing file is not an error,
// it just yields an empty fileConfig.
func loadFile() (fileConfig, error) {
	var fc fileConfig

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
		}
	}
	return fc, nil
}

// Save writes the API key (and base URL if non-default) to the config file
// with 0600 permissions. It creates the parent directory as needed.
func Save(apiKey, baseURL string) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	apiKeyJSON, err := json.Marshal(apiKey)
	if err != nil {
		return fmt.Errorf("encode api key: %w", err)
	}

	var b strings.Builder
	b.WriteString("# tabstack CLI configuration\n")
	b.WriteString("api_key = " + string(apiKeyJSON) + "\n")
	if baseURL != "" && baseURL != DefaultBaseURL {
		baseURLJSON, err := json.Marshal(baseURL)
		if err != nil {
			return fmt.Errorf("encode base url: %w", err)
		}
		b.WriteString("base_url = " + string(baseURLJSON) + "\n")
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	// WriteFile only sets 0600 on newly created files; existing files retain
	// their original permissions. Explicitly chmod to enforce the restriction.
	return os.Chmod(path, 0o600)
}
