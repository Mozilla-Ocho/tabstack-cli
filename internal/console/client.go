package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrSessionExpired means the access token aged out; the caller should refresh
// and retry. ErrInvalidSession means the session is unusable (unknown, revoked,
// or issued for another audience) and the user must log in again.
var (
	ErrSessionExpired = errors.New("session expired")
	ErrInvalidSession = errors.New("session is no longer valid")
	ErrForbiddenScope = errors.New("session is not scoped for the CLI API")
	ErrNotFound       = errors.New("not found")
)

// Client calls the console management API with a Bearer session.
type Client struct {
	authURL string
	token   string
	http    *http.Client
}

func NewClient(authURL, sessionToken string) *Client {
	return &Client{
		authURL: strings.TrimSuffix(authURL, "/"),
		token:   sessionToken,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Identity is GET /cli/me.
type Identity struct {
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
	Session struct {
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"session"`
	DefaultOrg    string         `json:"default_org"`
	Organizations []Organization `json:"organizations"`
}

type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type APIKey struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	OrganizationID string     `json:"organization_id"`
	Preview        string     `json:"preview"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	CreatedAt      *time.Time `json:"created_at"`
	// Secret is only populated by Create and Reveal.
	Secret string `json:"api_key"`
}

type Session struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  *time.Time `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Current    bool       `json:"current"`
}

func (c *Client) Me(ctx context.Context) (*Identity, error) {
	var out Identity
	err := c.do(ctx, http.MethodGet, "/cli/me", nil, &out)
	return &out, err
}

func (c *Client) Organizations(ctx context.Context) ([]Organization, error) {
	var out struct {
		Organizations []Organization `json:"organizations"`
	}
	err := c.do(ctx, http.MethodGet, "/cli/organizations", nil, &out)
	return out.Organizations, err
}

func (c *Client) ListAPIKeys(ctx context.Context, orgID string) ([]APIKey, error) {
	var out struct {
		APIKeys []APIKey `json:"api_keys"`
	}
	err := c.do(ctx, http.MethodGet, "/cli/api_keys?organization_id="+orgID, nil, &out)
	return out.APIKeys, err
}

func (c *Client) CreateAPIKey(ctx context.Context, orgID, name string) (*APIKey, error) {
	body := map[string]string{"organization_id": orgID, "name": name}
	var out APIKey
	err := c.do(ctx, http.MethodPost, "/cli/api_keys", body, &out)
	return &out, err
}

// RevealAPIKey re-reads the plaintext of an existing key so the CLI can adopt it
// instead of minting another. The console audit-logs every reveal.
func (c *Client) RevealAPIKey(ctx context.Context, id string) (string, error) {
	var out struct {
		APIKey string `json:"api_key"`
	}
	err := c.do(ctx, http.MethodPost, "/cli/api_keys/"+id+"/reveal", nil, &out)
	return out.APIKey, err
}

func (c *Client) DeleteAPIKey(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/cli/api_keys/"+id, nil, nil)
}

func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	var out struct {
		Sessions []Session `json:"sessions"`
	}
	err := c.do(ctx, http.MethodGet, "/cli/sessions", nil, &out)
	return out.Sessions, err
}

func (c *Client) RevokeSession(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/cli/sessions/"+id, nil, nil)
}

func (c *Client) RevokeAllSessions(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/cli/sessions", nil, nil)
}

func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/cli/logout", nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.authURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		return c.apiError(resp.StatusCode, payload)
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("decode response from %s: %w", path, err)
		}
	}
	return nil
}

// apiError maps the console's error envelope onto typed errors so callers can
// tell "refresh and retry" from "log in again".
func (c *Client) apiError(status int, payload []byte) error {
	var env struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(payload, &env)

	switch env.Error {
	case "session_expired":
		return ErrSessionExpired
	case "invalid_session":
		return ErrInvalidSession
	case "insufficient_scope":
		return ErrForbiddenScope
	case "not_found":
		return ErrNotFound
	}

	detail := env.Description
	if detail == "" {
		detail = env.Error
	}
	if detail == "" {
		detail = strings.TrimSpace(string(payload))
	}
	if status == http.StatusNotFound {
		return ErrNotFound
	}
	return fmt.Errorf("console returned %d: %s", status, detail)
}
