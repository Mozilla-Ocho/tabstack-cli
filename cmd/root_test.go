package cmd

import (
	"testing"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// isolate snapshots the package-global flag vars and rootApp, restoring them on
// cleanup so tests that mutate globals do not leak into one another.
func isolate(t *testing.T) {
	t.Helper()
	o, a, b, nc, to := flagOutput, flagAPIKey, flagBaseURL, flagNoColor, flagTimeout
	org, auth := flagOrg, flagAuthURL
	prev := rootApp
	t.Cleanup(func() {
		flagOutput, flagAPIKey, flagBaseURL, flagNoColor, flagTimeout = o, a, b, nc, to
		flagOrg, flagAuthURL = org, auth
		rootApp = prev
	})
}

func TestResolveMode(t *testing.T) {
	isolate(t)
	cases := []struct {
		flag    string
		want    ui.OutputMode
		wantErr bool
	}{
		{"json", ui.ModeJSON, false},
		{"pretty", ui.ModePretty, false},
		{"", ui.ModeJSON, false}, // stdout is piped in tests -> json
		{"bogus", "", true},
	}
	for _, tc := range cases {
		flagOutput = tc.flag
		got, err := resolveMode()
		if tc.wantErr {
			if err == nil {
				t.Errorf("resolveMode(%q): expected error", tc.flag)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveMode(%q): %v", tc.flag, err)
		}
		if got != tc.want {
			t.Errorf("resolveMode(%q) = %q, want %q", tc.flag, got, tc.want)
		}
	}
}

func TestNewRenderer(t *testing.T) {
	isolate(t)
	flagOutput = "json"
	flagNoColor = true
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != ui.ModeJSON {
		t.Errorf("mode = %q", r.Mode)
	}
	if r.Out == nil || r.Err == nil {
		t.Error("renderer writers not set")
	}
}

func TestNewRendererInvalidOutput(t *testing.T) {
	isolate(t)
	flagOutput = "bogus"
	if _, err := newRenderer(); err == nil {
		t.Error("expected error for invalid --output")
	}
}

func TestSetupApp(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config file present
	t.Setenv("TABSTACK_API_KEY", "secret-key-123")
	t.Setenv("TABSTACK_BASE_URL", "")
	flagOutput = "json"
	flagAPIKey = ""
	flagBaseURL = ""

	if err := setupApp(); err != nil {
		t.Fatalf("setupApp: %v", err)
	}
	if rootApp.key.APIKey != "secret-key-123" {
		t.Errorf("api key = %q", rootApp.key.APIKey)
	}
	if rootApp.key.Source != config.SourceEnv {
		t.Errorf("key source = %q, want %q", rootApp.key.Source, config.SourceEnv)
	}
	if rootApp.client == nil {
		t.Error("client not built")
	}
}

func TestSetupAppMissingKey(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TABSTACK_API_KEY", "")
	flagOutput = "json"
	flagAPIKey = ""

	if err := setupApp(); codeOf(err) != 2 {
		t.Errorf("err = %v, want code 2", err)
	}
}

func TestSetupRendererOnly(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TABSTACK_API_KEY", "")
	flagOutput = "json"
	flagAPIKey = ""

	if err := setupRendererOnly(); err != nil {
		t.Fatalf("setupRendererOnly: %v", err)
	}
	if rootApp.renderer.Out == nil {
		t.Error("renderer not built")
	}
	if rootApp.client != nil {
		t.Error("client should be nil for renderer-only setup")
	}
}

func TestNewRootCmdWiring(t *testing.T) {
	sub := map[string]bool{}
	for _, c := range NewRootCmd().Commands() {
		sub[c.Name()] = true
	}
	for _, want := range []string{"agent", "extract", "generate", "schema", "auth"} {
		if !sub[want] {
			t.Errorf("root subcommand %q not registered", want)
		}
	}
}
