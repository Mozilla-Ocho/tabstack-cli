package cmd

import (
	"strings"
	"testing"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

func TestAuthLoginCmd(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TABSTACK_API_KEY", "")
	flagBaseURL = ""
	buf := setTestApp(t)

	cmd := newAuthLoginCmd()
	if err := cmd.Flags().Set("key", "valid-key-1234"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(buf.String(), "saved") {
		t.Errorf("output = %q", buf.String())
	}

	// The key must now resolve from the config file.
	cfg, err := config.Resolve("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "valid-key-1234" || cfg.KeySource != config.SourceFile {
		t.Errorf("resolved cfg = %+v", cfg)
	}
}

func TestAuthLoginInvalidKey(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	setTestApp(t)

	cmd := newAuthLoginCmd()
	if err := cmd.Flags().Set("key", "short"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); codeOf(err) != 2 {
		t.Errorf("err = %v, want code 2", err)
	}
}

func TestAuthStatusConfigured(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	rootApp.cfg = config.Config{APIKey: "k", KeySource: config.SourceEnv, BaseURL: "https://x"}

	cmd := newAuthStatusCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "API key configured") || !strings.Contains(out, "environment") {
		t.Errorf("output = %q", out)
	}
}

func TestAuthStatusNoKey(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	rootApp.cfg = config.Config{}

	cmd := newAuthStatusCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No API key") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestValidateKeyFormat(t *testing.T) {
	cases := []struct {
		key     string
		wantErr bool
	}{
		{"validkey12345678", false},
		{"sk-abc123-valid-key-here", false},
		{"short", true},
		{`key"bad`, true},
		{"key\nbad", true},
		{"key\rbad", true},
		{"key\tbad", true},
		{" leading", true},
		{"trailing ", true},
	}
	for _, tc := range cases {
		err := validateKeyFormat(tc.key)
		if tc.wantErr && err == nil {
			t.Errorf("validateKeyFormat(%q): expected error, got nil", tc.key)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("validateKeyFormat(%q): unexpected error: %v", tc.key, err)
		}
	}
}
