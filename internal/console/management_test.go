package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

// withSession builds a client whose session is live and attached, backed by an
// in-memory store.
func withSession(t *testing.T, handler http.HandlerFunc) (*Client, *config.Config, *memStore) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "tcli_live",
		RefreshToken: "rt_live",
		ExpiresAt:    time.Now().Add(time.Hour),
	}}
	store := &memStore{cfg: cfg}
	c := New(srv.URL, WithHTTPClient(srv.Client()))
	c.AttachSession(store, cfg)
	return c, cfg, store
}

func TestManagementRequestsCarryTheSession(t *testing.T) {
	var gotAuth, gotAccept string
	c, _, _ := withSession(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		_, _ = w.Write([]byte(`{"user":{"email":"user@example.test"},"session":{"expires_at":"2026-08-01T00:00:00Z"},"default_org":"org_a","organizations":[{"id":"org_a","name":"Alpha","role":"owner"}]}`))
	})

	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if gotAuth != "Bearer tcli_live" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Errorf("accept = %q", gotAccept)
	}
	if me.User.Email != "user@example.test" || me.DefaultOrg != "org_a" || len(me.Organizations) != 1 {
		t.Errorf("me = %+v", me)
	}
}

func TestManagementEndpointRoutes(t *testing.T) {
	type call struct{ method, path, query string }

	cases := []struct {
		name string
		body string
		want call
		run  func(*Client) error
	}{
		{
			name: "organizations",
			body: `[{"id":"org_a","name":"Alpha","role":"owner"}]`,
			want: call{http.MethodGet, "/cli/organizations", ""},
			run:  func(c *Client) error { _, err := c.Organizations(context.Background()); return err },
		},
		{
			name: "create key",
			body: `{"id":"k1","name":"cli-laptop","organization_id":"org_a","api_key":"key-plain"}`,
			want: call{http.MethodPost, "/cli/api_keys", ""},
			run: func(c *Client) error {
				_, err := c.CreateAPIKey(context.Background(), "org_a", "cli-laptop")
				return err
			},
		},
		{
			name: "list keys scopes by organization_id",
			body: `[{"id":"k1","name":"cli-laptop","preview":"key-…1234"}]`,
			want: call{http.MethodGet, "/cli/api_keys", "organization_id=org_a"},
			run: func(c *Client) error {
				_, err := c.ListAPIKeys(context.Background(), "org_a")
				return err
			},
		},
		{
			name: "revoke key",
			body: ``,
			want: call{http.MethodDelete, "/cli/api_keys/k1", ""},
			run:  func(c *Client) error { return c.RevokeAPIKey(context.Background(), "k1") },
		},
		{
			name: "sessions",
			body: `[{"id":"s1","label":"laptop","current":true}]`,
			want: call{http.MethodGet, "/cli/sessions", ""},
			run:  func(c *Client) error { _, err := c.Sessions(context.Background()); return err },
		},
		{
			name: "revoke one session",
			body: ``,
			want: call{http.MethodDelete, "/cli/sessions/s1", ""},
			run:  func(c *Client) error { return c.RevokeSession(context.Background(), "s1") },
		},
		{
			name: "revoke all sessions",
			body: ``,
			want: call{http.MethodDelete, "/cli/sessions", ""},
			run:  func(c *Client) error { return c.RevokeAllSessions(context.Background()) },
		},
		{
			name: "logout",
			body: ``,
			want: call{http.MethodDelete, "/cli/logout", ""},
			run:  func(c *Client) error { return c.Logout(context.Background()) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got call
			c, _, _ := withSession(t, func(w http.ResponseWriter, r *http.Request) {
				got = call{r.Method, r.URL.Path, r.URL.RawQuery}
				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
				}
			})

			if err := tc.run(c); err != nil {
				t.Fatalf("call: %v", err)
			}
			if got != tc.want {
				t.Errorf("request = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCreateAPIKeySendsOrgAndName(t *testing.T) {
	var body struct {
		OrganizationID string `json:"organization_id"`
		Name           string `json:"name"`
	}
	c, _, _ := withSession(t, func(w http.ResponseWriter, r *http.Request) {
		if err := decodeJSON(r, &body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content type = %q, want JSON for the management API", ct)
		}
		_, _ = w.Write([]byte(`{"id":"k1","name":"cli-laptop","organization_id":"org_a","api_key":"key-plain"}`))
	})

	key, err := c.CreateAPIKey(context.Background(), "org_a", "cli-laptop")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if body.OrganizationID != "org_a" || body.Name != "cli-laptop" {
		t.Errorf("body = %+v", body)
	}
	if key.APIKey != "key-plain" {
		t.Errorf("plaintext key = %q", key.APIKey)
	}
}

func TestUnauthorizedRefreshesOnceAndRetriesOnce(t *testing.T) {
	var (
		management int32
		refreshes  int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			atomic.AddInt32(&refreshes, 1)
			_, _ = w.Write([]byte(`{"access_token":"tcli_refreshed","expires_in":600,"refresh_token":"rt_next"}`))
			return
		}
		n := atomic.AddInt32(&management, 1)
		if r.Header.Get("Authorization") != "Bearer tcli_refreshed" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"session_expired"}`))
			return
		}
		_ = n
		_, _ = w.Write([]byte(`[{"id":"org_a","name":"Alpha","role":"owner"}]`))
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "tcli_stale",
		RefreshToken: "rt_live",
		ExpiresAt:    time.Now().Add(time.Hour), // not expired as far as we know
	}}
	store := &memStore{cfg: cfg}
	c := New(srv.URL, WithHTTPClient(srv.Client()))
	c.AttachSession(store, cfg)

	orgs, err := c.Organizations(context.Background())
	if err != nil {
		t.Fatalf("Organizations: %v", err)
	}
	if len(orgs) != 1 {
		t.Errorf("orgs = %+v", orgs)
	}
	if got := atomic.LoadInt32(&refreshes); got != 1 {
		t.Errorf("refreshes = %d, want exactly 1", got)
	}
	if got := atomic.LoadInt32(&management); got != 2 {
		t.Errorf("management attempts = %d, want 2 (original + one retry)", got)
	}
	if cfg.Session.RefreshToken != "rt_next" {
		t.Errorf("refresh token = %q, want the rotation stored", cfg.Session.RefreshToken)
	}
}

func TestInvalidSessionDoesNotRefresh(t *testing.T) {
	var management, refreshes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			atomic.AddInt32(&refreshes, 1)
			_, _ = w.Write([]byte(`{"access_token":"tcli_refreshed","expires_in":600}`))
			return
		}
		atomic.AddInt32(&management, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_session"}`))
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "tcli_stale",
		RefreshToken: "rt_live",
		ExpiresAt:    time.Now().Add(time.Hour),
	}}
	c := New(srv.URL, WithHTTPClient(srv.Client()))
	c.AttachSession(&memStore{cfg: cfg}, cfg)

	_, err := c.Organizations(context.Background())
	// invalid_session means the token is unknown/revoked; a refresh hands the
	// server the same rejected identity, so we must not spend one.
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("err = %v, want ErrInvalidSession", err)
	}
	if !contains(err.Error(), "tabstack auth login") {
		t.Errorf("error does not carry the fix: %v", err)
	}
	if got := atomic.LoadInt32(&refreshes); got != 0 {
		t.Errorf("refreshes = %d, want 0 (invalid_session must not refresh)", got)
	}
	if got := atomic.LoadInt32(&management); got != 1 {
		t.Errorf("management attempts = %d, want 1 (no retry)", got)
	}
}

func TestSessionExpiredRefreshesThenIsTerminal(t *testing.T) {
	var management, refreshes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			atomic.AddInt32(&refreshes, 1)
			_, _ = w.Write([]byte(`{"access_token":"tcli_refreshed","expires_in":600}`))
			return
		}
		atomic.AddInt32(&management, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"session_expired"}`))
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "tcli_stale",
		RefreshToken: "rt_live",
		ExpiresAt:    time.Now().Add(time.Hour),
	}}
	c := New(srv.URL, WithHTTPClient(srv.Client()))
	c.AttachSession(&memStore{cfg: cfg}, cfg)

	_, err := c.Organizations(context.Background())
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("err = %v, want ErrSessionExpired", err)
	}
	if !contains(err.Error(), "tabstack auth login") {
		t.Errorf("error does not carry the fix: %v", err)
	}
	// One refresh, one retry, then stop: never a loop.
	if got := atomic.LoadInt32(&refreshes); got != 1 {
		t.Errorf("refreshes = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&management); got != 2 {
		t.Errorf("management attempts = %d, want 2", got)
	}
}

func TestManagementAPIErrors(t *testing.T) {
	c, _, _ := withSession(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden","message":"not an owner"}`))
	})

	_, err := c.Organizations(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden || apiErr.Code != "forbidden" || apiErr.Message != "not an owner" {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestManagementWithoutSessionFailsClosed(t *testing.T) {
	c := New("https://console.example")
	if _, err := c.Me(context.Background()); !errors.Is(err, ErrNoSession) {
		t.Errorf("err = %v, want ErrNoSession", err)
	}
}

func TestRevealRespectsTheFeatureGate(t *testing.T) {
	var called int32
	c, _, _ := withSession(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		_, _ = w.Write([]byte(`{"api_key":"key-plain"}`))
	})

	_, err := c.RevealAPIKey(context.Background(), "k1")
	if RevealEnabled {
		if err != nil {
			t.Fatalf("RevealAPIKey: %v", err)
		}
		if atomic.LoadInt32(&called) != 1 {
			t.Error("reveal did not reach the server while enabled")
		}
		return
	}
	if err == nil {
		t.Fatal("expected an error while the reveal path is gated off")
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Error("gated reveal still hit the network")
	}
}

// decodeJSON reads a request body as JSON into out.
func decodeJSON(r *http.Request, out any) error {
	return json.NewDecoder(r.Body).Decode(out)
}

// contains is strings.Contains, spelled locally so the assertion reads plainly.
func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }
