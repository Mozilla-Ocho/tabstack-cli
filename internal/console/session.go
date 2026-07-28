package console

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

// refreshSkew is how early an access token is treated as expired, so a call
// never goes out with a token that dies in flight.
const refreshSkew = 60 * time.Second

// RefreshFunc exchanges a refresh token for a new session. Injected rather than
// called directly so the manager is testable without an HTTP server.
type RefreshFunc func(ctx context.Context, refreshToken string) (*TokenResponse, error)

// SessionManager hands out a valid access token, refreshing when it has
// expired, and persists the rotated refresh token.
//
// Refresh is single-flight: concurrent callers that arrive during a refresh wait
// on the one in progress instead of each firing their own. That matters because
// the server rotates the refresh token, so two simultaneous refreshes would
// leave one of them holding a value the server has already invalidated.
type SessionManager struct {
	refresh RefreshFunc
	store   config.CredentialStore
	cfg     *config.Config
	now     func() time.Time

	mu       sync.Mutex
	inflight *refreshCall
}

// refreshCall is one in-flight refresh that other callers can wait on.
type refreshCall struct {
	done  chan struct{}
	token string
	err   error
}

// NewSessionManager builds a manager over an already-loaded config. Mutations
// are written back through store.
func NewSessionManager(refresh RefreshFunc, store config.CredentialStore, cfg *config.Config) *SessionManager {
	return &SessionManager{
		refresh: refresh,
		store:   store,
		cfg:     cfg,
		now:     time.Now,
	}
}

// Config returns the config the manager mutates.
func (m *SessionManager) Config() *config.Config { return m.cfg }

// Token returns a usable access token, refreshing first if the stored one has
// expired.
func (m *SessionManager) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	s := m.cfg.Session
	if s == nil || s.AccessToken == "" {
		m.mu.Unlock()
		return "", ErrNoSession
	}
	if !s.Expired(m.now(), refreshSkew) {
		token := s.AccessToken
		m.mu.Unlock()
		return token, nil
	}
	return m.refreshLocked(ctx)
}

// ForceRefresh refreshes regardless of the recorded expiry. It is what a 401
// triggers: the server has told us the token is no good, whatever we think its
// lifetime was.
func (m *SessionManager) ForceRefresh(ctx context.Context) (string, error) {
	m.mu.Lock()
	return m.refreshLocked(ctx)
}

// refreshLocked performs or joins a refresh. It is called with m.mu held and
// releases it before doing any I/O.
func (m *SessionManager) refreshLocked(ctx context.Context) (string, error) {
	if call := m.inflight; call != nil {
		m.mu.Unlock()
		select {
		case <-call.done:
			return call.token, call.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	s := m.cfg.Session
	if s == nil || s.RefreshToken == "" {
		m.mu.Unlock()
		return "", ErrSessionExpired
	}
	refreshToken := s.RefreshToken

	call := &refreshCall{done: make(chan struct{})}
	m.inflight = call
	m.mu.Unlock()

	tok, err := m.refresh(ctx, refreshToken)

	// Cross-process rotation recovery. A sibling process shares the stored
	// session (e.g. the MCP server running in Claude Desktop while the CLI is
	// used in a terminal). If it refreshed first, the server invalidated the
	// token we just presented, so our refresh fails with invalid_grant even
	// though nothing is wrong. Re-read the store: if the persisted refresh token
	// changed, adopt the sibling's session (or retry once with its token)
	// instead of forcing a needless re-login.
	var adopted *config.Session
	if err != nil && IsInvalidGrant(err) {
		if sib, lerr := m.store.Load(); lerr == nil && sib.Session != nil &&
			sib.Session.RefreshToken != "" && sib.Session.RefreshToken != refreshToken {
			if !sib.Session.Expired(m.now(), refreshSkew) {
				adopted = sib.Session
				err = nil
			} else {
				tok, err = m.refresh(ctx, sib.Session.RefreshToken)
			}
		}
	}

	m.mu.Lock()
	switch {
	case adopted != nil:
		// A sibling already rotated and persisted; take its still-valid token
		// without another network round trip or a redundant save.
		m.cfg.Session = adopted
		call.token = adopted.AccessToken
	case err != nil && IsInvalidGrant(err):
		// The refresh token is spent or revoked. Nothing to retry.
		call.err = ErrSessionExpired
	case err != nil:
		call.err = err
	default:
		m.applyLocked(tok)
		call.token = tok.AccessToken
		if saveErr := m.store.Save(m.cfg); saveErr != nil {
			// The rotation already happened server-side, so failing to persist
			// it means the next invocation carries a dead refresh token. Better
			// to surface that now than to fail mysteriously later.
			call.err = fmt.Errorf("persist refreshed session: %w", saveErr)
		}
	}
	m.inflight = nil
	m.mu.Unlock()

	close(call.done)
	return call.token, call.err
}

// applyLocked writes a token response onto the stored session. It is called
// with m.mu held.
//
// expires_in is always read from the response and stored as an absolute
// expires_at; the token lifetime is never assumed. An omitted refresh_token
// leaves the existing one in place, anything else would throw away the only
// value that can renew this session.
func (m *SessionManager) applyLocked(tok *TokenResponse) {
	if m.cfg.Session == nil {
		m.cfg.Session = &config.Session{}
	}
	s := m.cfg.Session
	s.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		s.RefreshToken = tok.RefreshToken
	}
	if tok.ExpiresIn > 0 {
		s.ExpiresAt = m.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	if tok.Scope != "" {
		s.Scope = tok.Scope
	}
}

// Establish records a brand new session from a token response and persists it.
// Used by login, where there is nothing to rotate.
func (m *SessionManager) Establish(tok *TokenResponse, email string) error {
	m.mu.Lock()
	m.cfg.Session = &config.Session{UserEmail: email}
	m.applyLocked(tok)
	m.mu.Unlock()
	return m.store.Save(m.cfg)
}

// SetEmail records the signed-in user's email on the session without touching
// the tokens.
func (m *SessionManager) SetEmail(email string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.Session != nil {
		m.cfg.Session.UserEmail = email
	}
}

// Clear drops the session and refresh token and persists the result. API keys
// are deliberately left alone: signing out is not the same as revoking
// credentials.
func (m *SessionManager) Clear() error {
	m.mu.Lock()
	m.cfg.Session = nil
	m.mu.Unlock()
	return m.store.Save(m.cfg)
}
