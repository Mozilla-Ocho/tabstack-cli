package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ClientID identifies the CLI to the authorization server. It is a public
// client: there is no secret, which is exactly why PKCE is mandatory.
const ClientID = "tabstack-cli"

// DefaultScopes is the scope set requested at authorize time.
//
// Provisional value, pending product confirmation of the scope names the
// console actually registers for this client. It is overridable at runtime with
// TABSTACK_OAUTH_SCOPES so a wrong default here is a one-variable fix rather
// than a rebuild, and changing the default is a one-line change.
const DefaultScopes = "cli offline_access"

// envScopes overrides DefaultScopes.
const envScopes = "TABSTACK_OAUTH_SCOPES"

// RevealEnabled gates the API key reveal endpoint and the "use existing key"
// login option. The auth contract includes both, so it is on; the constant is
// kept as the single switch that would disable the whole reveal path cleanly if
// the product ever pulls it. When on, "use existing" is still only offered when
// the org actually has a key to adopt (see runKeySetup).
const RevealEnabled = true

// Scopes returns the scope string to request.
func Scopes() string {
	if v := strings.TrimSpace(os.Getenv(envScopes)); v != "" {
		return v
	}
	return DefaultScopes
}

// TokenResponse is a successful /oauth/token response, for both the
// authorization_code and refresh_token grants.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// OAuthError is an RFC 6749 error response from the token endpoint.
type OAuthError struct {
	StatusCode  int
	Code        string
	Description string
}

func (e *OAuthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("token endpoint returned status %d", e.StatusCode)
}

// Known token endpoint error codes.
const (
	ErrCodeInvalidGrant       = "invalid_grant"
	ErrCodeInvalidRequest     = "invalid_request"
	ErrCodeInvalidClient      = "invalid_client"
	ErrCodeUnauthorizedClient = "unauthorized_client"
	ErrCodeUnsupportedGrant   = "unsupported_grant_type"
	ErrCodeInvalidScope       = "invalid_scope"
	ErrCodeSessionExpired     = "session_expired"
	ErrCodeInvalidSession     = "invalid_session"
)

// IsInvalidGrant reports whether err is an invalid_grant from the token
// endpoint, which means the code or refresh token is spent, revoked, or wrong.
// It is unrecoverable without a fresh login.
func IsInvalidGrant(err error) bool {
	var oe *OAuthError
	return errors.As(err, &oe) && oe.Code == ErrCodeInvalidGrant
}

// AuthorizeParams are the per-login inputs to the authorize URL.
type AuthorizeParams struct {
	RedirectURI string
	Challenge   string
	State       string
	Scope       string
	// OrgID optionally preselects an organisation on the consent screen. Servers
	// that do not support it ignore the parameter.
	OrgID string
}

// AuthorizeURL builds the browser URL that starts the login.
//
// resource carries the auth host, as the authorize contract requires. It is the
// only configured value that appears in a query string here besides
// organization_id, and it is not a secret.
func (c *Client) AuthorizeURL(p AuthorizeParams) string {
	scope := p.Scope
	if scope == "" {
		scope = Scopes()
	}

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("code_challenge", p.Challenge)
	q.Set("code_challenge_method", CodeChallengeMethodS256)
	q.Set("state", p.State)
	q.Set("scope", scope)
	q.Set("resource", c.authURL)
	if p.OrgID != "" {
		q.Set("organization_id", p.OrgID)
	}
	return c.authURL + "/oauth/authorize?" + q.Encode()
}

// ExchangeCode swaps an authorization code for a session. The token endpoint is
// form encoded, not JSON.
func (c *Client) ExchangeCode(ctx context.Context, code, verifier, redirectURI string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", ClientID)
	form.Set("code_verifier", verifier)
	// label names this session in `auth sessions` and the dashboard. Without it
	// the server falls back to the User-Agent, so every device shows up as
	// "Go-http-client/1.1" and cannot be told apart when revoking one.
	if label := deviceLabel(); label != "" {
		form.Set("label", label)
	}
	return c.token(ctx, form)
}

// deviceLabel is a human-friendly name for this machine, used to label the
// session at login. Falls back to the client id when the hostname is
// unavailable.
func deviceLabel() string {
	if h, err := os.Hostname(); err == nil && strings.TrimSpace(h) != "" {
		return h
	}
	return ClientID
}

// Refresh exchanges a refresh token for a new session. The response's
// refresh_token is a rotation: callers must store it and discard the old value,
// which the server is assumed to have invalidated.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", ClientID)
	return c.token(ctx, form)
}

// token posts a form-encoded grant to /oauth/token and decodes the result.
func (c *Client) token(ctx context.Context, form url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.authURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		oe := &OAuthError{StatusCode: resp.StatusCode}
		if json.Unmarshal(data, &payload) == nil {
			oe.Code = payload.Error
			oe.Description = payload.Description
		}
		if oe.Code == "" {
			oe.Description = strings.TrimSpace(string(data))
		}
		return nil, oe
	}

	var tok TokenResponse
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, errors.New("token response contained no access_token")
	}
	return &tok, nil
}
