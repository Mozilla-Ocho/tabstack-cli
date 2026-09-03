package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

// signedIn puts a live session and one keyed org into the test app's config.
func signedIn(t *testing.T) *config.Config {
	t.Helper()
	cfg := rootApp.cfg
	cfg.Session = &config.Session{
		AccessToken:  "tcli_live",
		RefreshToken: "rt_live",
		ExpiresAt:    time.Now().Add(30 * 24 * time.Hour),
		UserEmail:    "user@example.test",
	}
	cfg.ActiveOrg = "org_a"
	org := cfg.UpsertOrg("org_a", "Alpha")
	org.APIKey = "key-alpha-1234"
	org.APIKeyID = "k1"
	org.APIKeyName = "cli-test"
	return cfg
}

func TestAuthStatusSignedIn(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "")
	buf := setTestAppPretty(t)
	signedIn(t)

	// A console that still knows about the stored key: no revocation warning.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"k1","name":"cli-test","preview":"key-…1234"}]`))
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	cmd := newAuthStatusCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("status: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"signed in as user@example.test", "active org:", "Alpha", "org_a", "expires in", "api key:", "stored", "config:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "key-alpha-1234") {
		t.Errorf("status printed the key in full:\n%s", out)
	}
	if strings.Contains(out, "revoked") {
		t.Errorf("unexpected revocation warning:\n%s", out)
	}
}

func TestAuthStatusNotSignedIn(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "")
	buf := setTestAppPretty(t)

	cmd := newAuthStatusCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("status: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "not signed in") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "tabstack auth login") {
		t.Errorf("status should say how to sign in:\n%s", out)
	}
}

func TestAuthStatusReportsEnvOverride(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "key-from-env")
	buf := setTestAppPretty(t)
	signedIn(t)
	flagAuthURL = "http://127.0.0.1:1" // unreachable: the revocation check is best-effort

	cmd := newAuthStatusCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("status: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, config.EnvAPIKey) || !strings.Contains(out, "overrides stored keys") {
		t.Errorf("status did not flag the env override:\n%s", out)
	}
}

func TestAuthStatusWarnsOnRevokedKey(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "")
	buf := setTestAppPretty(t)
	signedIn(t)

	// The console no longer lists the stored key id.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"k9","name":"other","preview":"key-…9999"}]`))
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	cmd := newAuthStatusCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("status: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "revoked in the console") {
		t.Errorf("no revocation warning:\n%s", out)
	}
	if !strings.Contains(out, "tabstack keys create --org org_a") {
		t.Errorf("no fix command:\n%s", out)
	}
}

func TestAuthStatusWarnsOnLoosePermissions(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "")
	buf := setTestAppPretty(t)

	// Write a 0644 config at the store path.
	store := rootApp.store
	if err := store.Save(rootApp.cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.Path(), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newAuthStatusCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(buf.String(), "should be 0600") {
		t.Errorf("no permission warning:\n%s", buf.String())
	}
}

func TestAuthLogoutClearsSessionButKeepsKeys(t *testing.T) {
	isolate(t)
	buf := setTestAppPretty(t)
	signedIn(t)

	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	cmd := newAuthLogoutCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("logout: %v", err)
	}

	if gotMethod != http.MethodDelete || gotPath != "/cli/logout" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if rootApp.cfg.Session != nil {
		t.Error("session not cleared")
	}
	if !rootApp.cfg.HasKey("org_a") {
		t.Error("logout removed an API key; it must not")
	}
	if !strings.Contains(buf.String(), "keys were left in place") {
		t.Errorf("logout did not say what happened to keys:\n%s", buf.String())
	}
}

func TestAuthLogoutAllUsesTheSessionsEndpoint(t *testing.T) {
	isolate(t)
	setTestApp(t)
	signedIn(t)

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	cmd := newAuthLogoutCmd()
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("all", "true"); err != nil {
		t.Fatal(err)
	}
	// --all is confirmed; tests are non-interactive, so take the --yes path.
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("logout --all: %v", err)
	}
	if gotPath != "/cli/sessions" {
		t.Errorf("path = %q, want /cli/sessions", gotPath)
	}
}

func TestAuthLogoutWithoutASession(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)

	cmd := newAuthLogoutCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(buf.String(), "not signed in") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestAuthSessionsList(t *testing.T) {
	isolate(t)
	buf := setTestAppPretty(t)
	signedIn(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"s1","label":"laptop","current":true},{"id":"s2","label":"ci"}]`))
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	cmd := newAuthSessionsCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("sessions: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"laptop", "ci", "s1", "s2", "current session"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestAuthSessionsRevoke(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	signedIn(t)

	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	cmd := newAuthSessionsCmd()
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("revoke", "s2"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("sessions --revoke: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/cli/sessions/s2" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(buf.String(), "revoked") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestAuthSessionsRequiresASession(t *testing.T) {
	isolate(t)
	setTestApp(t)

	cmd := newAuthSessionsCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); codeOf(err) != 2 {
		t.Errorf("err = %v, want exit code 2", err)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{3 * 24 * time.Hour, "3 days"},
		{25 * time.Hour, "1 day"},
		{5 * time.Hour, "5 hours"},
		{30 * time.Minute, "under an hour"},
	}
	for _, tc := range cases {
		if got := humanDuration(tc.in); got != tc.want {
			t.Errorf("humanDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPrintExpiryWarnsInsideTheWindow(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)

	// Whole days are floored, so add an hour of slack to land on "3 days".
	printExpiry(rootApp.renderer, time.Now().Add(3*24*time.Hour+time.Hour))
	if !strings.Contains(buf.String(), "3 days") {
		t.Errorf("no warning for an imminent expiry:\n%s", buf.String())
	}

	buf.Reset()
	printExpiry(rootApp.renderer, time.Now().Add(-time.Hour))
	if !strings.Contains(buf.String(), "session expired") {
		t.Errorf("no message for an expired session:\n%s", buf.String())
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
