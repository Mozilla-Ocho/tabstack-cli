package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
)

func TestCallbackHandler(t *testing.T) {
	const state = "the-expected-state"
	const authURL = "https://console.example"

	cases := []struct {
		name     string
		path     string
		query    url.Values
		wantCode string
		wantErr  string
		want404  bool
	}{
		{
			name:     "happy path",
			path:     "/callback",
			query:    url.Values{"code": {"the-code"}, "state": {state}},
			wantCode: "the-code",
		},
		{
			name:     "issuer matches",
			path:     "/callback",
			query:    url.Values{"code": {"the-code"}, "state": {state}, "iss": {authURL + "/"}},
			wantCode: "the-code",
		},
		{
			name:    "wrong path",
			path:    "/",
			query:   url.Values{"code": {"the-code"}, "state": {state}},
			want404: true,
		},
		{
			name:    "missing code",
			path:    "/callback",
			query:   url.Values{"state": {state}},
			wantErr: "no authorization code",
		},
		{
			name:    "mismatched state",
			path:    "/callback",
			query:   url.Values{"code": {"the-code"}, "state": {"wrong"}},
			wantErr: "state did not match",
		},
		{
			name:    "empty state",
			path:    "/callback",
			query:   url.Values{"code": {"the-code"}},
			wantErr: "state did not match",
		},
		{
			name:    "mismatched issuer",
			path:    "/callback",
			query:   url.Values{"code": {"the-code"}, "state": {state}, "iss": {"https://evil.example"}},
			wantErr: "issuer",
		},
		{
			name:    "authorization server error",
			path:    "/callback",
			query:   url.Values{"error": {"access_denied"}, "error_description": {"user said no"}, "state": {state}},
			wantErr: "access_denied",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := make(chan callbackResult, 1)
			srv := httptest.NewServer(newCallbackHandler(state, authURL, results))
			t.Cleanup(srv.Close)

			resp, err := srv.Client().Get(srv.URL + tc.path + "?" + tc.query.Encode())
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()

			if tc.want404 {
				if resp.StatusCode != http.StatusNotFound {
					t.Errorf("status = %d, want 404", resp.StatusCode)
				}
				if len(results) != 0 {
					t.Error("a non-callback path produced a result")
				}
				return
			}

			body := readAll(t, resp)
			if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
				t.Errorf("Referrer-Policy = %q, want no-referrer", got)
			}
			for _, secret := range []string{"the-code", state} {
				if strings.Contains(body, secret) {
					t.Errorf("response body leaked %q: %s", secret, body)
				}
			}

			select {
			case res := <-results:
				if tc.wantErr != "" {
					if res.err == nil {
						t.Fatalf("expected an error mentioning %q, got code %q", tc.wantErr, res.code)
					}
					if !strings.Contains(res.err.Error(), tc.wantErr) {
						t.Errorf("err = %v, want it to mention %q", res.err, tc.wantErr)
					}
					if res.code != "" {
						t.Errorf("a rejected callback still yielded a code: %q", res.code)
					}
					return
				}
				if res.err != nil {
					t.Fatalf("unexpected error: %v", res.err)
				}
				if res.code != tc.wantCode {
					t.Errorf("code = %q, want %q", res.code, tc.wantCode)
				}
			default:
				t.Fatal("handler delivered no result")
			}
		})
	}
}

func TestCallbackHandlerDeliversOnlyOnce(t *testing.T) {
	results := make(chan callbackResult, 1)
	srv := httptest.NewServer(newCallbackHandler("st", "https://console.example", results))
	t.Cleanup(srv.Close)

	for range 3 {
		resp, err := srv.Client().Get(srv.URL + "/callback?code=c&state=st")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		resp.Body.Close()
	}
	if len(results) != 1 {
		t.Errorf("delivered %d results, want 1", len(results))
	}
}

func TestKeySetupModeFrom(t *testing.T) {
	cases := []struct {
		name    string
		flag    string
		noKey   bool
		want    keySetupMode
		wantErr bool
	}{
		{name: "unset prompts", want: keySetupPrompt},
		{name: "create", flag: "create", want: keySetupCreate},
		{name: "skip", flag: "skip", want: keySetupSkip},
		{name: "no-key means skip", noKey: true, want: keySetupSkip},
		{name: "no-key with skip agrees", flag: "skip", noKey: true, want: keySetupSkip},
		{name: "no-key conflicts with create", flag: "create", noKey: true, wantErr: true},
		{name: "nonsense", flag: "maybe", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := keySetupModeFrom(tc.flag, tc.noKey)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("keySetupModeFrom: %v", err)
			}
			if got != tc.want {
				t.Errorf("mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestKeySetupModeExistingFollowsTheFeatureGate(t *testing.T) {
	got, err := keySetupModeFrom("existing", false)
	if console.RevealEnabled {
		if err != nil || got != keySetupExisting {
			t.Errorf("mode = %q, err = %v", got, err)
		}
		return
	}
	if err == nil {
		t.Error("expected --api-key-setup=existing to be rejected while the reveal path is gated off")
	}
}

// fakeConsole is a stand-in auth host: the OAuth token endpoint plus the handful
// of /cli/* routes login touches.
type fakeConsole struct {
	srv        *httptest.Server
	tokenHits  int
	keyHits    int
	revealHits int
	form       url.Values
	orgs       string
	defaultOr  string
	keysList   string
}

func newFakeConsole(t *testing.T) *fakeConsole {
	t.Helper()
	f := &fakeConsole{
		orgs:      `[{"id":"org_a","name":"Alpha","role":"owner"}]`,
		defaultOr: "org_a",
		keysList:  `[{"id":"k1","name":"cli-test","preview":"key-…1234"}]`,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenHits++
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		f.form, _ = url.ParseQuery(string(body))
		_, _ = w.Write([]byte(`{"access_token":"tcli_access","token_type":"bearer","expires_in":3600,"refresh_token":"rt_1","scope":"cli"}`))
	})
	mux.HandleFunc("/cli/me", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"user":{"email":"user@example.test"},"session":{"expires_at":"2026-12-31T00:00:00Z"},"default_org":%q,"organizations":%s}`,
			f.defaultOr, f.orgs)
	})
	mux.HandleFunc("/cli/organizations", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(f.orgs))
	})
	mux.HandleFunc("/cli/api_keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			f.keyHits++
			_, _ = w.Write([]byte(`{"id":"k1","name":"cli-test","organization_id":"org_a","api_key":"key-created-1234"}`))
			return
		}
		_, _ = w.Write([]byte(f.keysList))
	})
	mux.HandleFunc("/cli/api_keys/k1/reveal", func(w http.ResponseWriter, r *http.Request) {
		f.revealHits++
		_, _ = w.Write([]byte(`{"api_key":"key-revealed-1234"}`))
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// stubBrowser installs a browser opener that plays the part of the user: it
// reads the authorize URL and calls the loopback redirect with a code, using the
// state it was given (or a bad one, to exercise rejection).
func stubBrowser(t *testing.T, code, stateOverride string) {
	t.Helper()
	prevOpen, prevDisplay := openBrowser, hasDisplay
	openBrowser = func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		q := u.Query()
		state := q.Get("state")
		if stateOverride != "" {
			state = stateOverride
		}
		redirect := q.Get("redirect_uri")
		resp, err := http.Get(fmt.Sprintf("%s?code=%s&state=%s", redirect, url.QueryEscape(code), url.QueryEscape(state)))
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	hasDisplay = func() bool { return true }
	t.Cleanup(func() { openBrowser, hasDisplay = prevOpen, prevDisplay })
}

func TestLoginEndToEnd(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	fake := newFakeConsole(t)
	flagAuthURL = fake.srv.URL
	stubBrowser(t, "the-code", "")

	if err := runLogin(context.Background(), keySetupCreate, ""); err != nil {
		t.Fatalf("runLogin: %v", err)
	}

	cfg := rootApp.cfg
	if cfg.Session == nil || cfg.Session.AccessToken != "tcli_access" {
		t.Fatalf("session = %+v", cfg.Session)
	}
	if cfg.Session.RefreshToken != "rt_1" {
		t.Errorf("refresh token = %q", cfg.Session.RefreshToken)
	}
	if cfg.Session.UserEmail != "user@example.test" {
		t.Errorf("email = %q", cfg.Session.UserEmail)
	}
	if cfg.ActiveOrg != "org_a" {
		t.Errorf("active org = %q, want the server's default_org", cfg.ActiveOrg)
	}
	if cfg.Orgs["org_a"] == nil || cfg.Orgs["org_a"].Name != "Alpha" {
		t.Errorf("orgs = %+v", cfg.Orgs)
	}
	if cfg.Orgs["org_a"].APIKey != "key-created-1234" || cfg.Orgs["org_a"].APIKeyID != "k1" {
		t.Errorf("stored key = %+v", cfg.Orgs["org_a"])
	}

	// The exchange was a PKCE authorization_code grant carrying our verifier.
	if fake.form.Get("grant_type") != "authorization_code" || fake.form.Get("code") != "the-code" {
		t.Errorf("token form = %v", fake.form)
	}
	if fake.form.Get("code_verifier") == "" {
		t.Error("no code_verifier in the exchange")
	}

	// Everything landed on disk, and the summary reports the essentials.
	saved, err := rootApp.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.ActiveOrg != "org_a" || saved.Session == nil || saved.Orgs["org_a"].APIKey == "" {
		t.Errorf("saved config = %+v", saved)
	}

	out := buf.String()
	for _, want := range []string{"user@example.test", "Alpha", "key-created-1234"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestLoginRejectsMismatchedState(t *testing.T) {
	isolate(t)
	setTestApp(t)
	fake := newFakeConsole(t)
	flagAuthURL = fake.srv.URL
	stubBrowser(t, "the-code", "tampered-state")

	err := runLogin(context.Background(), keySetupSkip, "")
	if err == nil {
		t.Fatal("expected the login to fail on a state mismatch")
	}
	if !strings.Contains(err.Error(), "state did not match") {
		t.Errorf("err = %v", err)
	}
	if fake.tokenHits != 0 {
		t.Errorf("token endpoint was called %d times; the code must not be exchanged", fake.tokenHits)
	}
	if rootApp.cfg.Session != nil {
		t.Error("a failed login stored a session")
	}
}

func TestLoginSkipsKeyCreationWhenAsked(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	fake := newFakeConsole(t)
	flagAuthURL = fake.srv.URL
	stubBrowser(t, "the-code", "")

	if err := runLogin(context.Background(), keySetupSkip, ""); err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	if fake.keyHits != 0 {
		t.Errorf("key endpoint called %d times, want 0 with --no-key", fake.keyHits)
	}
	if rootApp.cfg.HasKey("org_a") {
		t.Error("a key was stored despite skip")
	}
	if !strings.Contains(buf.String(), "tabstack keys create --org org_a") {
		t.Errorf("skip did not print the fix command:\n%s", buf.String())
	}
}

func TestLoginAdoptsExistingKey(t *testing.T) {
	isolate(t)
	buf := setTestApp(t)
	fake := newFakeConsole(t)
	flagAuthURL = fake.srv.URL
	stubBrowser(t, "the-code", "")

	if err := runLogin(context.Background(), keySetupExisting, ""); err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	if fake.keyHits != 0 {
		t.Errorf("create endpoint called %d times, want 0 when adopting existing", fake.keyHits)
	}
	if fake.revealHits != 1 {
		t.Errorf("reveal endpoint called %d times, want 1", fake.revealHits)
	}
	if got := rootApp.cfg.Orgs["org_a"]; got == nil || got.APIKey != "key-revealed-1234" || got.APIKeyID != "k1" {
		t.Errorf("stored key = %+v, want the revealed plaintext", got)
	}
	if !strings.Contains(buf.String(), "cli-test") {
		t.Errorf("output missing adopted key name:\n%s", buf.String())
	}
}

func TestLoginExistingErrorsWhenNoKeyToAdopt(t *testing.T) {
	isolate(t)
	setTestApp(t)
	fake := newFakeConsole(t)
	fake.keysList = `[]`
	flagAuthURL = fake.srv.URL
	stubBrowser(t, "the-code", "")

	err := runLogin(context.Background(), keySetupExisting, "")
	if codeOf(err) != 2 {
		t.Fatalf("err = %v, want exit code 2", err)
	}
	if fake.revealHits != 0 {
		t.Errorf("reveal called %d times, want 0 when there is nothing to adopt", fake.revealHits)
	}
	if rootApp.cfg.HasKey("org_a") {
		t.Error("a key was stored despite an empty adopt list")
	}
}

func TestLoginFailsWithoutADisplay(t *testing.T) {
	isolate(t)
	setTestApp(t)
	fake := newFakeConsole(t)
	flagAuthURL = fake.srv.URL

	prev := hasDisplay
	hasDisplay = func() bool { return false }
	t.Cleanup(func() { hasDisplay = prev })

	err := runLogin(context.Background(), keySetupSkip, "")
	if codeOf(err) != 2 {
		t.Fatalf("err = %v, want exit code 2", err)
	}
	if !strings.Contains(err.Error(), "TABSTACK_API_KEY") {
		t.Errorf("error should point at the non-interactive path: %v", err)
	}
}

func TestLoginPreselectsOrgOnTheConsentScreen(t *testing.T) {
	isolate(t)
	setTestApp(t)
	fake := newFakeConsole(t)
	flagAuthURL = fake.srv.URL

	var authorizeURL string
	prevOpen, prevDisplay := openBrowser, hasDisplay
	openBrowser = func(raw string) error {
		authorizeURL = raw
		u, _ := url.Parse(raw)
		q := u.Query()
		resp, err := http.Get(q.Get("redirect_uri") + "?code=c&state=" + url.QueryEscape(q.Get("state")))
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	hasDisplay = func() bool { return true }
	t.Cleanup(func() { openBrowser, hasDisplay = prevOpen, prevDisplay })

	if err := runLogin(context.Background(), keySetupSkip, "org_a"); err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	if !strings.Contains(authorizeURL, "organization_id=org_a") {
		t.Errorf("authorize URL missing the org hint: %s", authorizeURL)
	}
	// The loopback redirect is the 127.0.0.1 literal, never "localhost".
	if !strings.Contains(authorizeURL, url.QueryEscape("http://127.0.0.1:")) {
		t.Errorf("redirect_uri is not loopback by literal address: %s", authorizeURL)
	}
	if strings.Contains(authorizeURL, "localhost") {
		t.Errorf("redirect_uri used localhost: %s", authorizeURL)
	}
}

func TestPickActiveOrg(t *testing.T) {
	me := func(defaultOrg string, ids ...string) *console.Me {
		m := &console.Me{DefaultOrg: defaultOrg}
		for _, id := range ids {
			m.Organizations = append(m.Organizations, console.Org{ID: id, Name: id})
		}
		return m
	}

	cases := []struct {
		name string
		me   *console.Me
		want string
	}{
		{name: "default org wins", me: me("org_b", "org_a", "org_b"), want: "org_b"},
		{name: "single org with no default", me: me("", "org_a"), want: "org_a"},
		{name: "several orgs with no default", me: me("", "org_a", "org_b"), want: ""},
		{name: "no orgs", me: me(""), want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickActiveOrg(tc.me); got != tc.want {
				t.Errorf("pickActiveOrg = %q, want %q", got, tc.want)
			}
		})
	}
}

// readAll drains a response body as a string.
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}
