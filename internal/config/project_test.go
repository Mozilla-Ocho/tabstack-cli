package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestProjectKeyNamesMatchTheStruct stops the allowlist drifting from the type:
// a field added without listing it would silently be rejected as unknown.
func TestProjectKeyNamesMatchTheStruct(t *testing.T) {
	var fromTags []string
	rt := reflect.TypeOf(ProjectConfig{})
	for i := range rt.NumField() {
		tag := rt.Field(i).Tag.Get("toml")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		fromTags = append(fromTags, name)
	}
	sort.Strings(fromTags)

	got := sortedAllowed()
	if !reflect.DeepEqual(got, fromTags) {
		t.Errorf("projectKeyNames = %v, struct tags = %v", got, fromTags)
	}
}

// TestLoadProjectRejectsCredentialsAndEndpoints is the security-relevant test.
// A project file arrives by git clone, so it must not be able to carry a
// credential or redirect where one is sent.
func TestLoadProjectRejectsCredentialsAndEndpoints(t *testing.T) {
	cases := []struct {
		name string
		body string
		key  string
	}{
		{"an api key", "api_key = \"sk-leak\"\n", "api_key"},
		{"a legacy api key", "legacy_api_key = \"sk-leak\"\n", "legacy_api_key"},
		{"a session table", "[session]\naccess_token = \"t\"\n", "session"},
		{"stored org keys", "[orgs.acme]\napi_key = \"sk-leak\"\n", "orgs"},
		{"the product endpoint", "base_url = \"https://evil.test\"\n", "base_url"},
		{"the console endpoint", "auth_url = \"https://evil.test\"\n", "auth_url"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ProjectFileName)
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			pc, err := LoadProject(path)
			if err == nil {
				t.Fatalf("%s was accepted: %+v", tc.key, pc)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error does not name %q: %v", tc.key, err)
			}
			if pc != nil {
				t.Error("a rejected file must not yield a config")
			}
		})
	}
}

func TestLoadProjectRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), ProjectFileName)
	if err := os.WriteFile(path, []byte("concurency = 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadProject(path)
	if err == nil {
		t.Fatal("a typo was accepted silently")
	}
	if !strings.Contains(err.Error(), "concurency") {
		t.Errorf("error does not name the typo: %v", err)
	}
}

func TestLoadProjectAcceptsTheAllowlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ProjectFileName)
	body := `active_org = "acme"
storage = "./schemas"
output = "json"
effort = "max"
geo = "GB"
timeout = "45s"
max_duration = "10m"
concurrency = 8
retries = 0
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pc, err := LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if pc.ActiveOrg != "acme" || pc.Output != "json" || pc.Effort != "max" || pc.Geo != "GB" {
		t.Errorf("got %+v", pc)
	}
	if pc.Timeout != "45s" || pc.MaxDuration != "10m" {
		t.Errorf("durations = %q / %q", pc.Timeout, pc.MaxDuration)
	}
	if pc.Concurrency == nil || *pc.Concurrency != 8 {
		t.Errorf("concurrency = %v", pc.Concurrency)
	}
	// An explicit zero must survive, which is why these are pointers.
	if pc.Retries == nil || *pc.Retries != 0 {
		t.Errorf("retries = %v, want an explicit 0", pc.Retries)
	}
	// A relative storage path resolves against the file, not the caller.
	if pc.Storage != filepath.Join(dir, "schemas") {
		t.Errorf("storage = %q, want it relative to the file", pc.Storage)
	}
}

func TestLoadProjectEmptyPath(t *testing.T) {
	pc, err := LoadProject("")
	if err != nil || pc != nil {
		t.Errorf("got %+v, %v", pc, err)
	}
}

// TestFindProjectConfigWalksUp covers discovery and, more importantly, the
// boundaries. A stray .tabstack.toml in a shared parent or in / must not leak
// into unrelated runs.
func TestFindProjectConfigWalksUp(t *testing.T) {
	t.Setenv(EnvNoProjectConfig, "")
	t.Setenv(EnvProjectConfig, "")

	root := t.TempDir()
	// root/outside/.tabstack.toml   <- must never be found past the repo root
	// root/repo/.git/
	// root/repo/.tabstack.toml      <- the one to find
	// root/repo/a/b/                <- start here
	mk := func(parts ...string) string {
		p := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write := func(dir string) string {
		p := filepath.Join(dir, ProjectFileName)
		if err := os.WriteFile(p, []byte("concurrency = 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	outside := write(mk("outside"))
	repo := mk("repo")
	mk("repo", ".git")
	deep := mk("repo", "a", "b")
	want := write(repo)

	t.Run("found from a subdirectory", func(t *testing.T) {
		got, err := FindProjectConfig(deep)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("found at the repository root itself", func(t *testing.T) {
		got, err := FindProjectConfig(repo)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("the search stops at .git", func(t *testing.T) {
		// Remove the repo's own file: the one in root/outside is above the
		// .git boundary and must not be reached.
		if err := os.Remove(want); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { write(repo) })

		got, err := FindProjectConfig(deep)
		if err != nil {
			t.Fatal(err)
		}
		if got == outside {
			t.Errorf("walked past the repository root to %q", got)
		}
		if got != "" {
			t.Errorf("got %q, want none", got)
		}
	})
}

func TestFindProjectConfigEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "custom.toml")
	if err := os.WriteFile(explicit, []byte("retries = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file that discovery would otherwise find.
	local := t.TempDir()
	if err := os.WriteFile(filepath.Join(local, ProjectFileName), []byte("retries = 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("an explicit path skips the search", func(t *testing.T) {
		t.Setenv(EnvProjectConfig, explicit)
		got, err := FindProjectConfig(local)
		if err != nil {
			t.Fatal(err)
		}
		if got != explicit {
			t.Errorf("got %q, want %q", got, explicit)
		}
	})

	t.Run("an explicit path that cannot be read is an error", func(t *testing.T) {
		t.Setenv(EnvProjectConfig, filepath.Join(dir, "missing.toml"))
		if _, err := FindProjectConfig(local); err == nil {
			t.Error("a missing explicit path was accepted")
		}
	})

	t.Run("discovery can be switched off entirely", func(t *testing.T) {
		t.Setenv(EnvProjectConfig, "")
		t.Setenv(EnvNoProjectConfig, "1")
		got, err := FindProjectConfig(local)
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("got %q, want none", got)
		}
	})
}
