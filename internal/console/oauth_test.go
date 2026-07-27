package console

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newTestClient stands up a fake auth server and returns a client pointed at it.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(srv.URL, WithHTTPClient(srv.Client()))
}

func TestChallengeMatchesRFC7636Vector(t *testing.T) {
	// RFC 7636 appendix B: the canonical verifier/challenge pair.
	const (
		verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	)
	if got := Challenge(verifier); got != challenge {
		t.Errorf("Challenge = %q, want %q", got, challenge)
	}
}

func TestNewVerifierAndState(t *testing.T) {
	seen := map[string]bool{}
	for range 32 {
		v, err := NewVerifier()
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}
		s, err := NewState()
		if err != nil {
			t.Fatalf("NewState: %v", err)
		}
		for _, val := range []string{v, s} {
			if strings.Contains(val, "=") {
				t.Errorf("value is padded: %q", val)
			}
			raw, err := base64.RawURLEncoding.DecodeString(val)
			if err != nil {
				t.Fatalf("not base64url: %q (%v)", val, err)
			}
			if len(raw) != entropyBytes {
				t.Errorf("decoded %d bytes, want %d", len(raw), entropyBytes)
			}
			if seen[val] {
				t.Errorf("repeated random value %q", val)
			}
			seen[val] = true
		}
	}
}

func TestAuthorizeURL(t *testing.T) {
	c := New("https://console.example/")
	raw := c.AuthorizeURL(AuthorizeParams{
		RedirectURI: "http://127.0.0.1:54321/callback",
		Challenge:   "chal",
		State:       "st4te",
		Scope:       "cli offline_access",
		OrgID:       "org_a",
	})

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Path != "/oauth/authorize" {
		t.Errorf("path = %q", u.Path)
	}

	q := u.Query()
	want := map[string]string{
		"response_type":         "code",
		"client_id":             ClientID,
		"redirect_uri":          "http://127.0.0.1:54321/callback",
		"code_challenge":        "chal",
		"code_challenge_method": "S256",
		"state":                 "st4te",
		"scope":                 "cli offline_access",
		"resource":              "https://console.example",
		"organization_id":       "org_a",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if strings.Contains(raw, "plain") {
		t.Errorf("authorize URL mentions the plain challenge method: %s", raw)
	}
}

func TestAuthorizeURLOmitsOrgWhenUnset(t *testing.T) {
	c := New("https://console.example")
	raw := c.AuthorizeURL(AuthorizeParams{RedirectURI: "http://127.0.0.1:1/callback", Challenge: "c", State: "s"})
	if strings.Contains(raw, "organization_id") {
		t.Errorf("organization_id present without an org: %s", raw)
	}
}

func TestScopesEnvOverride(t *testing.T) {
	t.Setenv(envScopes, "")
	if got := Scopes(); got != DefaultScopes {
		t.Errorf("Scopes = %q, want the default %q", got, DefaultScopes)
	}
	t.Setenv(envScopes, " custom:one custom:two ")
	if got := Scopes(); got != "custom:one custom:two" {
		t.Errorf("Scopes = %q, want the trimmed override", got)
	}
}

func TestExchangeCodeSendsFormEncodedGrant(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotType   string
		gotForm   url.Values
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotType = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tcli_new","token_type":"bearer","expires_in":3600,"refresh_token":"rt_new","scope":"cli"}`))
	})

	tok, err := c.ExchangeCode(context.Background(), "the-code", "the-verifier", "http://127.0.0.1:9/callback")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/oauth/token" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotType != "application/x-www-form-urlencoded" {
		t.Errorf("content type = %q, want form encoded", gotType)
	}
	wantForm := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "the-code",
		"redirect_uri":  "http://127.0.0.1:9/callback",
		"client_id":     ClientID,
		"code_verifier": "the-verifier",
	}
	for k, v := range wantForm {
		if got := gotForm.Get(k); got != v {
			t.Errorf("form %s = %q, want %q", k, got, v)
		}
	}
	// A label names the session so devices are distinguishable in `auth
	// sessions`; without it the server falls back to the User-Agent.
	if gotForm.Get("label") == "" {
		t.Error("exchange did not send a session label")
	}
	if tok.AccessToken != "tcli_new" || tok.RefreshToken != "rt_new" || tok.ExpiresIn != 3600 {
		t.Errorf("token = %+v", tok)
	}
}

func TestRefreshSendsRefreshGrant(t *testing.T) {
	var gotForm url.Values
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		_, _ = w.Write([]byte(`{"access_token":"a2","token_type":"bearer","expires_in":60,"refresh_token":"r2"}`))
	})

	if _, err := c.Refresh(context.Background(), "rt_old"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if gotForm.Get("grant_type") != "refresh_token" || gotForm.Get("refresh_token") != "rt_old" {
		t.Errorf("form = %v", gotForm)
	}
	if gotForm.Get("client_id") != ClientID {
		t.Errorf("client_id = %q", gotForm.Get("client_id"))
	}
}

func TestTokenEndpointErrors(t *testing.T) {
	cases := []struct {
		name           string
		status         int
		body           string
		wantCode       string
		wantInvalidGrt bool
	}{
		{"invalid grant", 400, `{"error":"invalid_grant","error_description":"code expired"}`, ErrCodeInvalidGrant, true},
		{"invalid scope", 400, `{"error":"invalid_scope","error_description":"unknown scope"}`, ErrCodeInvalidScope, false},
		{"unauthorized client", 400, `{"error":"unauthorized_client"}`, ErrCodeUnauthorizedClient, false},
		{"non-json body", 500, `boom`, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})

			_, err := c.ExchangeCode(context.Background(), "c", "v", "http://127.0.0.1:9/callback")
			if err == nil {
				t.Fatal("expected an error")
			}
			oe, ok := err.(*OAuthError)
			if !ok {
				t.Fatalf("error type = %T, want *OAuthError", err)
			}
			if oe.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", oe.Code, tc.wantCode)
			}
			if got := IsInvalidGrant(err); got != tc.wantInvalidGrt {
				t.Errorf("IsInvalidGrant = %v, want %v", got, tc.wantInvalidGrt)
			}
		})
	}
}

func TestTokenResponseWithoutAccessTokenIsAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"token_type":"bearer","expires_in":60}`))
	})
	if _, err := c.ExchangeCode(context.Background(), "c", "v", "http://127.0.0.1:9/callback"); err == nil {
		t.Error("expected an error for a response with no access_token")
	}
}
