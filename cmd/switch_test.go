package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

// orgServer stands up a console that answers /cli/organizations with body, and
// anything else with 404.
func orgServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cli/organizations" {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// sessionOnly gives the test app a live session with no orgs or keys.
func sessionOnly(t *testing.T) *config.Config {
	t.Helper()
	cfg := rootApp.cfg
	cfg.Session = &config.Session{
		AccessToken:  "tcli_live",
		RefreshToken: "rt_live",
		ExpiresAt:    time.Now().Add(time.Hour),
		UserEmail:    "user@example.test",
	}
	return cfg
}

func TestSwitchWithOneOrgDoesNotRenderAPicker(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	cfg := sessionOnly(t)
	flagAuthURL = orgServer(t, `[{"id":"org_a","name":"Alpha","role":"owner"}]`).URL

	if err := runSwitch(context.Background(), ""); err != nil {
		t.Fatalf("runSwitch: %v", err)
	}
	if !strings.Contains(buf.String(), "You belong to one organization: Alpha") {
		t.Errorf("output = %q", buf.String())
	}
	if cfg.ActiveOrg != "" {
		t.Errorf("active org changed to %q; a single-org report must not switch", cfg.ActiveOrg)
	}
}

func TestSwitchWithNoArgumentNeedsATerminal(t *testing.T) {
	isolate(t)
	setTestApp(t)
	sessionOnly(t)
	flagAuthURL = orgServer(t, `[{"id":"org_a","name":"Alpha","role":"owner"},{"id":"org_b","name":"Bravo","role":"member"}]`).URL

	// stdin is not a TTY under `go test`, so the picker must refuse rather than block.
	err := runSwitch(context.Background(), "")
	if codeOf(err) == 0 || err == nil {
		t.Fatalf("err = %v, want a non-zero exit code", err)
	}
	if !strings.Contains(err.Error(), "requires a terminal") {
		t.Errorf("err = %v, want the terminal guidance", err)
	}
}

func TestSwitchResolvesArgumentAndSaves(t *testing.T) {
	isolate(t)
	buf := setTestAppPretty(t)
	cfg := sessionOnly(t)
	cfg.UpsertOrg("org_b", "Bravo").APIKey = "key-bravo-1234"
	flagAuthURL = orgServer(t, `[{"id":"org_a","name":"Alpha","role":"owner"},{"id":"org_b","name":"Bravo","role":"member"}]`).URL

	if err := runSwitch(context.Background(), "bra"); err != nil {
		t.Fatalf("runSwitch: %v", err)
	}
	if cfg.ActiveOrg != "org_b" {
		t.Errorf("active org = %q, want org_b", cfg.ActiveOrg)
	}
	saved, err := rootApp.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.ActiveOrg != "org_b" {
		t.Errorf("saved active org = %q", saved.ActiveOrg)
	}
	out := buf.String()
	if !strings.Contains(out, "now acting as Bravo") || !strings.Contains(out, "API key in place") {
		t.Errorf("output = %q", out)
	}
}

func TestSwitchAmbiguousAndUnknownArguments(t *testing.T) {
	orgs := `[{"id":"org_1","name":"Acme Corp","role":"owner"},{"id":"org_2","name":"Acme Labs","role":"member"}]`

	cases := []struct {
		name    string
		arg     string
		wantIn  []string
		wantOut string
	}{
		{name: "ambiguous prefix", arg: "acme", wantIn: []string{"ambiguous", "org_1", "org_2"}},
		{name: "unknown", arg: "zeta", wantIn: []string{"unknown organisation", "org_1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			setTestApp(t)
			cfg := sessionOnly(t)
			flagAuthURL = orgServer(t, orgs).URL

			err := runSwitch(context.Background(), tc.arg)
			if codeOf(err) != 2 {
				t.Fatalf("err = %v, want exit code 2", err)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err %q missing %q", err, want)
				}
			}
			if cfg.ActiveOrg != "" {
				t.Errorf("a failed switch changed the active org to %q", cfg.ActiveOrg)
			}
		})
	}
}

func TestSwitchToleratesAnUnreachableConsoleForAKnownOrg(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	cfg := sessionOnly(t)
	cfg.UpsertOrg("org_b", "Bravo").APIKey = "key-bravo-1234"
	// Nothing listening: the org list cannot be refreshed.
	flagAuthURL = "http://127.0.0.1:1"

	if err := runSwitch(context.Background(), "bravo"); err != nil {
		t.Fatalf("runSwitch: %v", err)
	}
	if cfg.ActiveOrg != "org_b" {
		t.Errorf("active org = %q, want the switch to go through offline", cfg.ActiveOrg)
	}
	if !strings.Contains(buf.String(), "could not refresh the organisation list") {
		t.Errorf("no offline warning:\n%s", buf.String())
	}
}

func TestSwitchOfflineStillRejectsUnknownOrgs(t *testing.T) {
	isolate(t)
	setTestApp(t)
	sessionOnly(t)
	flagAuthURL = "http://127.0.0.1:1"

	if err := runSwitch(context.Background(), "mystery"); codeOf(err) != 2 {
		t.Errorf("err = %v, want exit code 2", err)
	}
}

func TestSwitchTreatsARejectedRequestAsAnError(t *testing.T) {
	isolate(t)
	setTestApp(t)
	cfg := sessionOnly(t)
	cfg.UpsertOrg("org_b", "Bravo").APIKey = "key-bravo"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	// The console answered, it just said no: that is not offline tolerance.
	if err := runSwitch(context.Background(), "bravo"); codeOf(err) != 3 {
		t.Errorf("err = %v, want exit code 3", err)
	}
	if cfg.ActiveOrg != "" {
		t.Errorf("active org = %q after a rejected list", cfg.ActiveOrg)
	}
}

func TestSwitchWithoutASession(t *testing.T) {
	isolate(t)
	setTestApp(t)

	if err := runSwitch(context.Background(), "anything"); codeOf(err) != 2 {
		t.Errorf("err = %v, want exit code 2", err)
	}
}

func TestSwitchToKeylessOrgPrintsTheFixCommand(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	cfg := sessionOnly(t)
	flagAuthURL = orgServer(t, `[{"id":"org_a","name":"Alpha","role":"owner"},{"id":"org_b","name":"Bravo","role":"member"}]`).URL

	// Non-interactive: key setup is skipped with guidance, not attempted.
	if err := runSwitch(context.Background(), "org_b"); err != nil {
		t.Fatalf("runSwitch: %v", err)
	}
	if cfg.ActiveOrg != "org_b" {
		t.Errorf("active org = %q", cfg.ActiveOrg)
	}
	if cfg.HasKey("org_b") {
		t.Error("a key was created without being asked for")
	}
	out := buf.String()
	if !strings.Contains(out, "tabstack keys create --org org_b") {
		t.Errorf("no fix command:\n%s", out)
	}
	if !strings.Contains(out, "no API key stored") {
		t.Errorf("switch did not report the missing key:\n%s", out)
	}
}
