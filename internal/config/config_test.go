package config

import (
	"os"
	"path/filepath"
	"testing"
)

// isolateConfig points the config dir at a temp location and clears the env
// vars Resolve reads, so tests never touch the developer's real config or
// inherit ambient credentials.
func isolateConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(envAPIKey, "")
	t.Setenv(envBaseURL, "")
	return dir
}

func TestResolveDefaults(t *testing.T) {
	isolateConfig(t)

	cfg, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", cfg.APIKey)
	}
	if cfg.KeySource != SourceNone {
		t.Errorf("KeySource = %q, want %q", cfg.KeySource, SourceNone)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, DefaultBaseURL)
	}
}

func TestResolvePriority(t *testing.T) {
	dir := isolateConfig(t)

	// Seed a config file with file-level values.
	path := filepath.Join(dir, "tabstack", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "api_key = \"file-key\"\nbase_url = \"https://file.example\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("file only", func(t *testing.T) {
		cfg, err := Resolve("", "")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.APIKey != "file-key" || cfg.KeySource != SourceFile {
			t.Errorf("got key=%q src=%q, want file-key/file", cfg.APIKey, cfg.KeySource)
		}
		if cfg.BaseURL != "https://file.example" {
			t.Errorf("BaseURL = %q", cfg.BaseURL)
		}
	})

	t.Run("env overrides file", func(t *testing.T) {
		t.Setenv(envAPIKey, "env-key")
		t.Setenv(envBaseURL, "https://env.example")
		cfg, err := Resolve("", "")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.APIKey != "env-key" || cfg.KeySource != SourceEnv {
			t.Errorf("got key=%q src=%q, want env-key/environment", cfg.APIKey, cfg.KeySource)
		}
		if cfg.BaseURL != "https://env.example" {
			t.Errorf("BaseURL = %q", cfg.BaseURL)
		}
	})

	t.Run("flag overrides env and file", func(t *testing.T) {
		t.Setenv(envAPIKey, "env-key")
		cfg, err := Resolve("flag-key", "https://flag.example")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.APIKey != "flag-key" || cfg.KeySource != SourceFlag {
			t.Errorf("got key=%q src=%q, want flag-key/flag", cfg.APIKey, cfg.KeySource)
		}
		if cfg.BaseURL != "https://flag.example" {
			t.Errorf("BaseURL = %q", cfg.BaseURL)
		}
	})
}

func TestResolveMissingFileIsNotError(t *testing.T) {
	isolateConfig(t)
	if _, err := Resolve("", ""); err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
}

func TestConfigPathXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "tabstack", "config.toml")
	if got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
}

func TestLoadFileParsing(t *testing.T) {
	dir := isolateConfig(t)
	path := filepath.Join(dir, "tabstack", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// Mix comments, blank lines, quoted values, stray whitespace, an unknown
	// key, and a malformed line with no '='.
	body := `
# a comment
api_key = "abc123"

  base_url = "https://x.example"
unknown = "ignored"
malformed line
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	fc, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if fc.APIKey != "abc123" {
		t.Errorf("APIKey = %q", fc.APIKey)
	}
	if fc.BaseURL != "https://x.example" {
		t.Errorf("BaseURL = %q", fc.BaseURL)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	isolateConfig(t)

	if err := Save("secret-key", DefaultBaseURL); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path, _ := ConfigPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 600", perm)
	}

	// Default base URL should be omitted from the file.
	data, _ := os.ReadFile(path)
	if string(data) == "" {
		t.Fatal("empty config written")
	}

	cfg, err := Resolve("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "secret-key" {
		t.Errorf("round-trip APIKey = %q", cfg.APIKey)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("round-trip BaseURL = %q, want default", cfg.BaseURL)
	}
}

func TestSaveNonDefaultBaseURL(t *testing.T) {
	isolateConfig(t)
	if err := Save("k", "https://custom.example"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Resolve("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://custom.example" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestSaveAndLoadSpecialChars(t *testing.T) {
	isolateConfig(t)

	tricky := `key"with"quotes` + "\nand newline"
	if err := Save(tricky, ""); err != nil {
		t.Fatal(err)
	}

	fc, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if fc.APIKey != tricky {
		t.Errorf("got %q, want %q", fc.APIKey, tricky)
	}
}

func TestSaveAndLoadNormal(t *testing.T) {
	isolateConfig(t)

	if err := Save("mykey123", ""); err != nil {
		t.Fatal(err)
	}

	fc, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if fc.APIKey != "mykey123" {
		t.Errorf("got %q, want %q", fc.APIKey, "mykey123")
	}
}

func TestConfigFileWrittenWith0600(t *testing.T) {
	dir := isolateConfig(t)

	if err := Save("k", ""); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "tabstack", "config.toml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 0600", perm)
	}
}

func TestSaveFixesPermissionsOnExistingFile(t *testing.T) {
	dir := isolateConfig(t)

	// Pre-create the file with insecure permissions (e.g. user-created or old install).
	path := filepath.Join(dir, "tabstack", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Save("newkey", ""); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm after Save = %o, want 0600 (Save must chmod existing files)", perm)
	}
}
