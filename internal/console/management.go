package console

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Org is one organisation the signed-in user belongs to. The id is the stable
// identity; the name is display only and can change.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// Me is the whoami payload: who the session belongs to, when it expires, and
// which organisations it can act for.
type Me struct {
	User struct {
		Email string `json:"email"`
	} `json:"user"`
	Session struct {
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"session"`
	DefaultOrg    string `json:"default_org"`
	Organizations []Org  `json:"organizations"`
}

// APIKey is an org-scoped product credential. APIKey (the plaintext) is only
// populated on create and reveal; list responses carry Preview instead.
type APIKey struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	OrganizationID string     `json:"organization_id"`
	APIKey         string     `json:"api_key"`
	Preview        string     `json:"preview"`
	LastUsedAt     *time.Time `json:"last_used_at"`
}

// SessionInfo is one CLI session belonging to the user.
type SessionInfo struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  *time.Time `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Current    bool       `json:"current"`
}

// Me fetches the signed-in user, their session expiry, and their orgs.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var out Me
	if err := c.do(ctx, http.MethodGet, "/cli/me", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Organizations lists the organisations the user belongs to.
func (c *Client) Organizations(ctx context.Context) ([]Org, error) {
	var out []Org
	if err := c.do(ctx, http.MethodGet, "/cli/organizations", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateAPIKey mints a new API key for an organisation. The plaintext comes
// back exactly once, in this response.
func (c *Client) CreateAPIKey(ctx context.Context, orgID, name string) (*APIKey, error) {
	body := struct {
		OrganizationID string `json:"organization_id"`
		Name           string `json:"name"`
	}{OrganizationID: orgID, Name: name}

	var out APIKey
	if err := c.do(ctx, http.MethodPost, "/cli/api_keys", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAPIKeys lists an organisation's keys. Previews only, never plaintext.
func (c *Client) ListAPIKeys(ctx context.Context, orgID string) ([]APIKey, error) {
	q := url.Values{}
	q.Set("organization_id", orgID)

	var out []APIKey
	if err := c.do(ctx, http.MethodGet, "/cli/api_keys", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RevealAPIKey fetches the plaintext of an existing key.
//
// Gated by RevealEnabled: the method stays wired so enabling the feature is a
// one-constant change, but while the constant is false no command surfaces it
// and calling it returns an error rather than hitting the endpoint.
func (c *Client) RevealAPIKey(ctx context.Context, keyID string) (*APIKey, error) {
	if !RevealEnabled {
		return nil, errors.New("revealing an existing API key is not enabled in this build")
	}
	var out APIKey
	path := "/cli/api_keys/" + url.PathEscape(keyID) + "/reveal"
	if err := c.do(ctx, http.MethodPost, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeAPIKey revokes a key server-side.
func (c *Client) RevokeAPIKey(ctx context.Context, keyID string) error {
	return c.do(ctx, http.MethodDelete, "/cli/api_keys/"+url.PathEscape(keyID), nil, nil, nil)
}

// Sessions lists the user's CLI sessions, with the current one marked.
func (c *Client) Sessions(ctx context.Context) ([]SessionInfo, error) {
	var out []SessionInfo
	if err := c.do(ctx, http.MethodGet, "/cli/sessions", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RevokeSession revokes one session by id.
func (c *Client) RevokeSession(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/cli/sessions/"+url.PathEscape(id), nil, nil, nil)
}

// RevokeAllSessions revokes every session the user has, including this one.
func (c *Client) RevokeAllSessions(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/cli/sessions", nil, nil, nil)
}

// Logout revokes the current session only.
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/cli/logout", nil, nil, nil)
}
