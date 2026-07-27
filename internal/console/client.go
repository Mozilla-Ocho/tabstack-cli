// Package console talks to the Tabstack auth and management host
// (console.tabstack.ai): the OAuth 2.1 endpoints under /oauth/* and the
// management API under /cli/*.
//
// It is kept separate from internal/client on purpose. This package sends the
// user-scoped session token and never sees an org-scoped API key;
// internal/client sends the API key and never sees the session. Splitting them
// by host makes it impossible to send the wrong credential to the wrong place.
package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

// defaultTimeout bounds every management and token request. Unlike the product
// client there is nothing streaming here, so a hard timeout is safe.
const defaultTimeout = 30 * time.Second

// maxResponseBytes caps a management response. These payloads are small lists;
// anything larger means we are talking to something that is not the console.
const maxResponseBytes = 4 << 20

// Client is the auth-host client. The zero session manager is valid: the OAuth
// endpoints (authorize URL, code exchange) need no session, which is what lets
// `auth login` run before one exists.
type Client struct {
	authURL string
	http    *http.Client
	sess    *SessionManager
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient swaps the underlying http.Client. Mostly useful for tests.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New constructs a Client against an auth host root (no trailing slash).
func New(authURL string, opts ...Option) *Client {
	c := &Client{
		authURL: strings.TrimRight(authURL, "/"),
		http:    &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// AuthURL returns the configured auth host root.
func (c *Client) AuthURL() string { return c.authURL }

// AttachSession gives the client a session to authenticate management calls
// with, wiring its own Refresh method in as the refresh transport. It returns
// the manager so callers can force a refresh or clear the session.
func (c *Client) AttachSession(store config.CredentialStore, cfg *config.Config) *SessionManager {
	c.sess = NewSessionManager(c.Refresh, store, cfg)
	return c.sess
}

// Session returns the attached session manager, or nil.
func (c *Client) Session() *SessionManager { return c.sess }

// APIError is a decoded non-2xx management response. The console returns
// {"error": "..."} bodies, matching the product API.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

// orNil returns e as an error, or a nil error when e is nil. It avoids the
// typed-nil trap: returning a nil *APIError straight into an error would be a
// non-nil interface value.
func (e *APIError) orNil() error {
	if e == nil {
		return nil
	}
	return e
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Code
	}
	if msg != "" {
		return fmt.Sprintf("console error (%d): %s", e.StatusCode, msg)
	}
	return fmt.Sprintf("console error: status %d", e.StatusCode)
}

// ErrSessionExpired means the session could not be made to work: it was
// rejected twice, or there is no refresh token left to try. The message carries
// the fix because this is the one auth error users hit routinely.
var ErrSessionExpired = errors.New("session expired, run: tabstack auth login")

// ErrInvalidSession means the server rejected the session as unknown, revoked,
// or wrong-audience (401 invalid_session). Unlike an aged-out access token a
// refresh cannot fix this, so we surface it without refreshing.
var ErrInvalidSession = errors.New("session is no longer valid, run: tabstack auth login")

// ErrNoSession means no session is stored at all.
var ErrNoSession = errors.New("not signed in, run: tabstack auth login")

// do performs an authenticated management request against /cli/*. A 401
// session_expired (an aged-out access token) is refreshed once and retried
// once; a 401 invalid_session is terminal without a refresh, because the token
// is unknown/revoked/wrong-audience and refreshing cannot fix it. It never
// loops.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	if c.sess == nil {
		return ErrNoSession
	}

	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	token, err := c.sess.Token(ctx)
	if err != nil {
		return err
	}

	status, apiErr, err := c.attempt(ctx, method, path, query, raw, token, out)
	if err != nil {
		return err
	}
	if status != http.StatusUnauthorized {
		return apiErr.orNil()
	}

	// invalid_session (and insufficient_scope, were it a 401) is not an aged-out
	// token: a refresh will hand the server the same rejected identity, so bail
	// with re-login guidance instead of burning a refresh.
	if apiErr != nil && apiErr.Code == ErrCodeInvalidSession {
		return ErrInvalidSession
	}

	// session_expired, or a 401 with no recognised code: refresh once, retry
	// once, then give up. A single retry cannot loop even if the server answers
	// 401 unconditionally.
	token, err = c.sess.ForceRefresh(ctx)
	if err != nil {
		return ErrSessionExpired
	}
	status, apiErr, err = c.attempt(ctx, method, path, query, raw, token, out)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		if apiErr != nil && apiErr.Code == ErrCodeInvalidSession {
			return ErrInvalidSession
		}
		return ErrSessionExpired
	}
	return apiErr.orNil()
}

// attempt sends one management request. On any non-2xx it returns the status
// code and the decoded *APIError (so the caller can read the error code, e.g.
// to tell session_expired from invalid_session on a 401) without treating it as
// a transport error; err is reserved for transport/decode failures. It decodes
// into out on success.
func (c *Client) attempt(ctx context.Context, method, path string, query url.Values, body []byte, token string, out any) (int, *APIError, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}

	u := c.authURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, decodeError(resp), nil
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return resp.StatusCode, nil, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("decode response: %w", err)
	}
	return resp.StatusCode, nil, nil
}

// decodeError turns a non-2xx management response into an *APIError, keeping
// the raw body as the message when it is not the expected JSON shape.
func decodeError(resp *http.Response) *APIError {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))

	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	apiErr := &APIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(data))}
	if json.Unmarshal(data, &payload) == nil && (payload.Error != "" || payload.Message != "") {
		apiErr.Code = payload.Error
		apiErr.Message = payload.Message
		if apiErr.Message == "" {
			apiErr.Message = payload.Error
		}
	}
	return apiErr
}
