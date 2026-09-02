package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

func TestKeysCreateStoresAndPrintsOnce(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	cfg := sessionOnly(t)
	cfg.ActiveOrg = "org_a"
	cfg.UpsertOrg("org_a", "Alpha")

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"id":"k1","name":"cli-test","organization_id":"org_a","api_key":"key-created-1234"}`))
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	cmd := newKeysCreateCmd()
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("name", "cli-test"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("keys create: %v", err)
	}

	if !strings.Contains(gotBody, `"organization_id":"org_a"`) || !strings.Contains(gotBody, `"name":"cli-test"`) {
		t.Errorf("request body = %s", gotBody)
	}
	org := cfg.Org("org_a")
	if org.APIKey != "key-created-1234" || org.APIKeyID != "k1" || org.APIKeyName != "cli-test" {
		t.Errorf("stored org = %+v", org)
	}
	out := buf.String()
	if !strings.Contains(out, "key-created-1234") {
		t.Errorf("the key was never shown:\n%s", out)
	}
	if !strings.Contains(out, "only time the key is shown") {
		t.Errorf("no show-once warning:\n%s", out)
	}
}

func TestKeysCreateWithoutAnOrg(t *testing.T) {
	isolate(t)
	setTestApp(t)
	sessionOnly(t)

	cmd := newKeysCreateCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); codeOf(err) != 2 {
		t.Errorf("err = %v, want exit code 2", err)
	}
}

func TestKeysListPrintsPreviewsOnly(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	cfg := sessionOnly(t)
	cfg.ActiveOrg = "org_a"
	cfg.UpsertOrg("org_a", "Alpha").APIKeyID = "k1"

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		// The server includes a plaintext field; list must not print it.
		_, _ = w.Write([]byte(`[{"id":"k1","name":"cli-test","preview":"key-…1234","api_key":"key-plaintext-leak"}]`))
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	cmd := newKeysListCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("keys list: %v", err)
	}

	if gotQuery != "organization_id=org_a" {
		t.Errorf("query = %q", gotQuery)
	}
	out := buf.String()
	if strings.Contains(out, "key-plaintext-leak") {
		t.Errorf("list printed a plaintext key:\n%s", out)
	}
	for _, want := range []string{"cli-test", "key-…1234", "k1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// keysServer stands up an auth host that lists two keys and reveals either one.
func keysServer(t *testing.T, revealHits *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/cli/api_keys" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":"k1","name":"laptop","preview":"key-…1111"},{"id":"k2","name":"ci","preview":"key-…2222"}]`))
		case r.URL.Path == "/cli/api_keys/k2/reveal":
			*revealHits++
			_, _ = w.Write([]byte(`{"api_key":"key-revealed-2222"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestKeysUseAdoptsByIDReplacingTheStoredKey(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	cfg := sessionOnly(t)
	cfg.ActiveOrg = "org_a"
	org := cfg.UpsertOrg("org_a", "Alpha")
	org.APIKey = "key-old-1111"
	org.APIKeyID = "k1"
	org.APIKeyName = "laptop"

	var reveals int
	flagAuthURL = keysServer(t, &reveals).URL

	cmd := newKeysUseCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, []string{"k2"}); err != nil {
		t.Fatalf("keys use: %v", err)
	}

	if reveals != 1 {
		t.Errorf("reveal called %d times, want 1", reveals)
	}
	got := cfg.Org("org_a")
	if got.APIKey != "key-revealed-2222" || got.APIKeyID != "k2" || got.APIKeyName != "ci" {
		t.Errorf("stored key = %+v, want the adopted k2", got)
	}
	if !strings.Contains(buf.String(), "ci") {
		t.Errorf("output does not name the adopted key:\n%s", buf.String())
	}
}

func TestKeysUseUnknownIDIsAnError(t *testing.T) {
	isolate(t)
	setTestApp(t)
	cfg := sessionOnly(t)
	cfg.ActiveOrg = "org_a"
	cfg.UpsertOrg("org_a", "Alpha")

	var reveals int
	flagAuthURL = keysServer(t, &reveals).URL

	cmd := newKeysUseCmd()
	cmd.SetContext(context.Background())
	err := cmd.RunE(cmd, []string{"nope"})
	if codeOf(err) != 2 {
		t.Fatalf("err = %v, want exit code 2", err)
	}
	if reveals != 0 {
		t.Errorf("reveal called %d times for an unknown id, want 0", reveals)
	}
}

func TestKeysRevokeClearsTheStoredKey(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	cfg := sessionOnly(t)
	cfg.ActiveOrg = "org_a"
	org := cfg.UpsertOrg("org_a", "Alpha")
	org.APIKey = "key-alpha-1234"
	org.APIKeyID = "k1"
	org.APIKeyName = "cli-test"

	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	cmd := newKeysRevokeCmd()
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"k1"}); err != nil {
		t.Fatalf("keys revoke: %v", err)
	}

	if gotMethod != http.MethodDelete || gotPath != "/cli/api_keys/k1" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if cfg.HasKey("org_a") {
		t.Error("revoked key still stored")
	}
	if cfg.Org("org_a").APIKeyID != "" {
		t.Errorf("key id not cleared: %+v", cfg.Org("org_a"))
	}
	out := buf.String()
	if !strings.Contains(out, "Alpha now has no API key stored") {
		t.Errorf("output does not name the keyless org:\n%s", out)
	}
	if !strings.Contains(out, "tabstack keys create --org org_a") {
		t.Errorf("no fix command:\n%s", out)
	}
}

func TestKeysRevokeUnknownKeyLeavesConfigAlone(t *testing.T) {
	isolate(t)
	setTestApp(t)
	cfg := sessionOnly(t)
	cfg.ActiveOrg = "org_a"
	cfg.UpsertOrg("org_a", "Alpha").APIKey = "key-alpha-1234"
	cfg.Org("org_a").APIKeyID = "k1"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL

	cmd := newKeysRevokeCmd()
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"k-other"}); err != nil {
		t.Fatalf("keys revoke: %v", err)
	}
	if !cfg.HasKey("org_a") {
		t.Error("revoking someone else's key cleared the stored one")
	}
}

func TestKeysRequireASession(t *testing.T) {
	isolate(t)
	setTestApp(t)

	for name, cmd := range map[string]func() error{
		"create": func() error {
			c := newKeysCreateCmd()
			c.SetContext(context.Background())
			return c.RunE(c, nil)
		},
		"list": func() error {
			c := newKeysListCmd()
			c.SetContext(context.Background())
			return c.RunE(c, nil)
		},
		"revoke": func() error {
			c := newKeysRevokeCmd()
			c.SetContext(context.Background())
			return c.RunE(c, []string{"k1"})
		},
	} {
		if err := cmd(); codeOf(err) != 2 {
			t.Errorf("keys %s: err = %v, want exit code 2", name, err)
		}
	}
}

func TestDefaultKeyNameIsHostQualified(t *testing.T) {
	got := defaultKeyName()
	if !strings.HasPrefix(got, "cli-") {
		t.Errorf("defaultKeyName = %q, want a cli- prefix", got)
	}
	if strings.Contains(got, ".") {
		t.Errorf("defaultKeyName = %q, want the domain trimmed", got)
	}
}

// storeAt points the command tree's credential store at a throwaway file for the
// duration of a test.
func storeAt(t *testing.T, cfg *config.Config) config.CredentialStore {
	t.Helper()
	store := config.NewFileStoreAt(filepath.Join(t.TempDir(), "config.toml"))
	if cfg != nil {
		if err := store.Save(cfg); err != nil {
			t.Fatal(err)
		}
	}
	prev := newStore
	newStore = func() (config.CredentialStore, error) { return store, nil }
	t.Cleanup(func() { newStore = prev })
	return store
}

func TestSetupAppWithOrgOverride(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "")
	t.Setenv("TABSTACK_BASE_URL", "")
	flagOutput = "json"
	flagAPIKey = ""

	storeAt(t, &config.Config{
		ActiveOrg: "org_a",
		Orgs: map[string]*config.OrgCreds{
			"org_a": {Name: "Alpha", APIKey: "key-alpha"},
			"org_b": {Name: "Bravo", APIKey: "key-bravo"},
			"org_c": {Name: "Gamma"},
		},
	})

	t.Run("override selects the other org's key without persisting", func(t *testing.T) {
		flagOrg = "bravo"
		t.Cleanup(func() { flagOrg = "" })

		if err := setupApp(); err != nil {
			t.Fatalf("setupApp: %v", err)
		}
		if rootApp.key.APIKey != "key-bravo" {
			t.Errorf("key = %q, want org_b's", rootApp.key.APIKey)
		}
		if rootApp.orgOverride != "org_b" {
			t.Errorf("override = %q", rootApp.orgOverride)
		}
		// The override is per-invocation: the stored active org is untouched.
		saved, err := rootApp.store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if saved.ActiveOrg != "org_a" {
			t.Errorf("saved active org = %q, want org_a untouched", saved.ActiveOrg)
		}
	})

	t.Run("override without a key errors instead of borrowing", func(t *testing.T) {
		flagOrg = "org_c"
		t.Cleanup(func() { flagOrg = "" })

		err := setupApp()
		if codeOf(err) != 2 {
			t.Fatalf("err = %v, want exit code 2", err)
		}
		if !strings.Contains(err.Error(), "tabstack keys create --org org_c") {
			t.Errorf("err = %v, want the fix command", err)
		}
		for _, borrowed := range []string{"key-alpha", "key-bravo"} {
			if strings.Contains(err.Error(), borrowed) {
				t.Errorf("error mentions another org's key: %v", err)
			}
		}
	})

	t.Run("unknown override org", func(t *testing.T) {
		flagOrg = "org_missing"
		t.Cleanup(func() { flagOrg = "" })

		if err := setupApp(); codeOf(err) != 2 {
			t.Errorf("err = %v, want exit code 2", err)
		}
	})
}

func TestSetupAppUsesLegacyKeyWithoutAnActiveOrg(t *testing.T) {
	isolate(t)
	t.Setenv(config.EnvAPIKey, "")
	t.Setenv("TABSTACK_BASE_URL", "")
	flagOutput = "json"
	flagAPIKey = ""
	flagOrg = ""

	storeAt(t, &config.Config{LegacyAPIKey: "key-legacy"})

	if err := setupApp(); err != nil {
		t.Fatalf("setupApp: %v", err)
	}
	if rootApp.key.APIKey != "key-legacy" || rootApp.key.Source != config.SourceLegacy {
		t.Errorf("resolution = %+v", rootApp.key)
	}
}

// TestKeysRevokeRefusesWithoutConfirmation is the safety net: revoking is
// irreversible and breaks every service still sending the key, so on a
// non-interactive stdin (as here) it must refuse with exit 2 and name --yes,
// rather than proceeding or hanging on a prompt nobody will answer.
func TestKeysRevokeRefusesWithoutConfirmation(t *testing.T) {
	isolate(t)
	setTestApp(t)

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	flagAuthURL = srv.URL
	rootApp.cfg.Session = &config.Session{AccessToken: "t", ExpiresAt: time.Now().Add(time.Hour)}

	cmd := newKeysRevokeCmd()
	cmd.SetContext(context.Background())
	err := cmd.RunE(cmd, []string{"k1"})
	if err == nil {
		t.Fatal("revoke proceeded without confirmation")
	}
	if codeOf(err) != 2 {
		t.Errorf("exit code = %d, want 2", codeOf(err))
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error does not mention --yes: %v", err)
	}
	if called {
		t.Error("the revoke request was sent despite refusing to confirm")
	}
}
