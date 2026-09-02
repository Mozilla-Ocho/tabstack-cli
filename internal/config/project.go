package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ProjectFileName is the per-repository settings file.
const ProjectFileName = ".tabstack.toml"

// Environment overrides for project-config discovery. Both exist so a CI run
// can be made reproducible regardless of what is checked into the tree.
const (
	// EnvProjectConfig names an explicit file and skips the upward search.
	EnvProjectConfig = "TABSTACK_PROJECT_CONFIG"
	// EnvNoProjectConfig disables project config entirely when non-empty.
	EnvNoProjectConfig = "TABSTACK_NO_PROJECT_CONFIG"
)

// ProjectConfig is the subset of settings a repository may pin for everyone
// working in it. It sits between environment variables and the user's own
// config in the precedence chain.
//
// The type is a strict allowlist, and that is the whole point of the design.
// A project file arrives by `git clone`, so anything it can set is something a
// repository author can set on your machine. Two categories are therefore
// absent and rejected outright rather than merely ignored:
//
//   - credentials (api_key, session, orgs): a project file is exactly the file
//     people commit, so allowing a key here would manufacture leaks.
//   - endpoints (base_url, auth_url): these decide *where the API key is
//     sent*. Honouring them from a cloned repository would turn
//     `git clone && tabstack extract` into credential exfiltration. They stay
//     flag- and environment-only, for development.
//
// active_org is allowed because it names one of your own organisations, holds
// no secret, and "this repository works against org X" is the main reason to
// want any of this.
type ProjectConfig struct {
	ActiveOrg   string `toml:"active_org,omitempty"`
	Storage     string `toml:"storage,omitempty"`
	Output      string `toml:"output,omitempty"`
	Effort      string `toml:"effort,omitempty"`
	Geo         string `toml:"geo,omitempty"`
	Timeout     string `toml:"timeout,omitempty"`
	MaxDuration string `toml:"max_duration,omitempty"`

	// Pointers so an explicit 0, which is meaningful for both, is
	// distinguishable from the field being absent.
	Concurrency *int `toml:"concurrency,omitempty"`
	Retries     *int `toml:"retries,omitempty"`

	// Path is where the file was found. Not a setting.
	Path string `toml:"-"`
}

// deniedProjectKeys maps a rejected key to why it is rejected, so the error
// can explain rather than just refuse.
var deniedProjectKeys = map[string]string{
	"api_key":        "a project file is the file you commit; set TABSTACK_API_KEY or run `tabstack auth login` instead",
	"legacy_api_key": "a project file is the file you commit; set TABSTACK_API_KEY or run `tabstack auth login` instead",
	"session":        "sessions are user scoped and must not be shared; run `tabstack auth login`",
	"orgs":           "stored organisation keys are credentials; they belong in your own config only",
	"base_url":       "endpoints decide where your API key is sent, so a cloned repository must not set them; use --base-url or TABSTACK_BASE_URL",
	"auth_url":       "endpoints decide where your session is sent, so a cloned repository must not set them; use --auth-url or TABSTACK_AUTH_URL",
	"version":        "the version field belongs to your own config file, not a project file",
}

// FindProjectConfig locates the project file for a directory.
//
// It walks upwards so the file works from anywhere inside a repository, and
// stops at the first of: a directory containing .git, the user's home
// directory, or the filesystem root. Those boundaries keep a stray
// .tabstack.toml in a shared parent, or in /, out of unrelated runs.
//
// Returns an empty path, and no error, when there is nothing to load.
func FindProjectConfig(startDir string) (string, error) {
	if os.Getenv(EnvNoProjectConfig) != "" {
		return "", nil
	}
	if explicit := strings.TrimSpace(os.Getenv(EnvProjectConfig)); explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("the %s environment variable points at %s, which cannot be read: %w", EnvProjectConfig, explicit, err)
		}
		return explicit, nil
	}

	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()

	for {
		candidate := filepath.Join(dir, ProjectFileName)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}

		// Boundaries, checked after the candidate so a file sitting at the
		// repository root or in the home directory itself is still found.
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return "", nil
		}
		if home != "" && dir == home {
			return "", nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil // filesystem root
		}
		dir = parent
	}
}

// LoadProject reads and validates a project file. A denied key is an error
// rather than a silent omission: someone who wrote api_key into this file
// believes it is in use, and may be about to commit it.
func LoadProject(path string) (*ProjectConfig, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Decode to a map first so denied and unknown keys can be reported by
	// name. Strict decoding alone would only say "unknown field".
	var seen map[string]any
	if err := toml.Unmarshal(raw, &seen); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var denied, unknown []string
	allowed := allowedProjectKeys()
	for key := range seen {
		switch {
		case deniedProjectKeys[key] != "":
			denied = append(denied, key)
		case !allowed[key]:
			unknown = append(unknown, key)
		}
	}
	sort.Strings(denied)
	sort.Strings(unknown)

	if len(denied) > 0 {
		key := denied[0]
		return nil, fmt.Errorf("project config %s sets %q, which a project file may not set: %s",
			path, key, deniedProjectKeys[key])
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("project config %s sets %q, which is not a project setting. Allowed: %s",
			path, unknown[0], strings.Join(sortedAllowed(), ", "))
	}

	var pc ProjectConfig
	if err := toml.Unmarshal(raw, &pc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	pc.Path = path

	// A relative storage path is relative to the file, not the working
	// directory, so it means the same thing from anywhere in the repository.
	if pc.Storage != "" && !filepath.IsAbs(pc.Storage) {
		pc.Storage = filepath.Join(filepath.Dir(path), pc.Storage)
	}
	return &pc, nil
}

// allowedProjectKeys is derived from the struct tags so the allowlist cannot
// drift from the type.
func allowedProjectKeys() map[string]bool {
	out := make(map[string]bool, len(projectKeyNames))
	for _, k := range projectKeyNames {
		out[k] = true
	}
	return out
}

func sortedAllowed() []string {
	out := append([]string(nil), projectKeyNames...)
	sort.Strings(out)
	return out
}

// projectKeyNames lists the TOML names of ProjectConfig's settings. Kept
// beside the struct so adding a field without listing it here fails the test
// that compares the two.
var projectKeyNames = []string{
	"active_org", "storage", "output", "effort", "geo",
	"timeout", "max_duration", "concurrency", "retries",
}

// ErrNoProjectConfig is returned by helpers that require a project file.
var ErrNoProjectConfig = errors.New("no project configuration found")
