package cmd

import (
	"testing"

	"github.com/spf13/cobra"

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

// TestTimeoutFlagDefault pins the default down: unset means bounded (so a
// wedged non-streaming call cannot hang forever), and an explicit 0 disables.
// The "0 disables" half works because both call sites guard on flagTimeout > 0
// and simply omit client.WithTimeout, so it is worth asserting the default is
// not itself 0.
func TestTimeoutFlagDefault(t *testing.T) {
	isolate(t)
	extract := findCommand(t, NewRootCmd(), "extract")
	pf := extract.PersistentFlags()
	f := pf.Lookup("timeout")
	if f == nil {
		t.Fatal("no --timeout flag registered on extract")
	}
	if f.DefValue != defaultTimeout.String() {
		t.Errorf("--timeout default = %s, want %s", f.DefValue, defaultTimeout)
	}
	if defaultTimeout <= 0 {
		t.Fatal("defaultTimeout must be positive, or nothing is ever bounded")
	}

	if err := pf.Parse([]string{"--timeout", "0"}); err != nil {
		t.Fatal(err)
	}
	if flagTimeout != 0 {
		t.Errorf("--timeout 0 gave %v, want 0 (the disable path)", flagTimeout)
	}
}

// findCommand looks up a direct subcommand of root by name.
func findCommand(t *testing.T, root *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("no %q command", name)
	return nil
}

// TestFlagPlacement pins which subtrees carry the credential and endpoint
// flags. They used to sit on the root, so `schema list --help` and
// `config show --help` advertised --api-key and --timeout, which those
// commands never read.
func TestFlagPlacement(t *testing.T) {
	isolate(t)
	root := NewRootCmd()

	cases := []struct {
		cmd  string
		has  []string
		hasA []string // must NOT be present
	}{
		{"extract", []string{"api-key", "base-url", "timeout", "org", "debug"}, nil},
		{"generate", []string{"api-key", "base-url", "timeout", "org", "debug"}, nil},
		{"agent", []string{"api-key", "base-url", "timeout", "org", "debug"}, nil},
		{"mcp", []string{"api-key", "base-url", "timeout", "auth-url", "debug"}, []string{"org"}},
		{"keys", []string{"auth-url", "org"}, []string{"api-key", "base-url", "timeout"}},
		{"auth", []string{"auth-url"}, []string{"api-key", "base-url", "timeout"}},
		{"config", []string{"base-url", "auth-url"}, []string{"api-key", "timeout"}},
		{"schema", nil, []string{"api-key", "base-url", "auth-url", "timeout", "org", "debug"}},
	}

	for _, tc := range cases {
		cmd := findCommand(t, root, tc.cmd)
		for _, name := range tc.has {
			if cmd.PersistentFlags().Lookup(name) == nil {
				t.Errorf("%s: missing --%s", tc.cmd, name)
			}
		}
		for _, name := range tc.hasA {
			if cmd.PersistentFlags().Lookup(name) != nil {
				t.Errorf("%s: advertises --%s but never reads it", tc.cmd, name)
			}
		}
	}

	// Only the flags every command uses stay on the root.
	for _, name := range []string{"api-key", "base-url", "auth-url", "timeout", "org", "debug"} {
		if root.PersistentFlags().Lookup(name) != nil {
			t.Errorf("--%s is still a root persistent flag", name)
		}
	}
	for _, name := range []string{"output", "no-color"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("--%s should stay on the root", name)
		}
	}
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
