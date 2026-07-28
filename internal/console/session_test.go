package console

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

// memStore is an in-memory CredentialStore for exercising session persistence
// without touching disk.
type memStore struct {
	mu      sync.Mutex
	cfg     *config.Config
	saves   int
	saveErr error
}

func (m *memStore) Load() (*config.Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg, nil
}

func (m *memStore) Save(cfg *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saves++
	m.cfg = cfg
	return m.saveErr
}

func (m *memStore) Path() string { return "memory" }

func (m *memStore) saveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saves
}

// fixedNow gives the manager a deterministic clock.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestTokenReturnsUnexpiredTokenWithoutRefreshing(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "tcli_live",
		RefreshToken: "rt",
		ExpiresAt:    now.Add(time.Hour),
	}}
	store := &memStore{cfg: cfg}

	var calls int32
	sm := NewSessionManager(func(context.Context, string) (*TokenResponse, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("should not refresh")
	}, store, cfg)
	sm.now = fixedNow(now)

	got, err := sm.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "tcli_live" {
		t.Errorf("token = %q", got)
	}
	if calls != 0 {
		t.Errorf("refreshed %d times, want 0", calls)
	}
}

func TestRefreshRotatesAndPersistsTheRefreshToken(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "tcli_old",
		RefreshToken: "rt_old",
		ExpiresAt:    now.Add(-time.Minute), // expired
	}}
	store := &memStore{cfg: cfg}

	var sawRefreshToken string
	sm := NewSessionManager(func(_ context.Context, rt string) (*TokenResponse, error) {
		sawRefreshToken = rt
		return &TokenResponse{
			AccessToken:  "tcli_new",
			TokenType:    "bearer",
			ExpiresIn:    900,
			RefreshToken: "rt_new",
			Scope:        "cli",
		}, nil
	}, store, cfg)
	sm.now = fixedNow(now)

	got, err := sm.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "tcli_new" {
		t.Errorf("token = %q, want the refreshed one", got)
	}
	if sawRefreshToken != "rt_old" {
		t.Errorf("refreshed with %q, want the stored token", sawRefreshToken)
	}
	if cfg.Session.RefreshToken != "rt_new" {
		t.Errorf("stored refresh token = %q, want the rotation to overwrite it", cfg.Session.RefreshToken)
	}
	// expires_at is absolute and derived from the response's expires_in, never a
	// hardcoded lifetime.
	if want := now.Add(900 * time.Second); !cfg.Session.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want %v", cfg.Session.ExpiresAt, want)
	}
	if store.saveCount() != 1 {
		t.Errorf("saves = %d, want the rotation persisted once", store.saveCount())
	}
}

func TestRefreshKeepsOldRefreshTokenWhenServerOmitsOne(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "a",
		RefreshToken: "rt_old",
		ExpiresAt:    now.Add(-time.Minute),
	}}
	sm := NewSessionManager(func(context.Context, string) (*TokenResponse, error) {
		return &TokenResponse{AccessToken: "a2", ExpiresIn: 60}, nil
	}, &memStore{cfg: cfg}, cfg)
	sm.now = fixedNow(now)

	if _, err := sm.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if cfg.Session.RefreshToken != "rt_old" {
		t.Errorf("refresh token = %q, want the existing one kept", cfg.Session.RefreshToken)
	}
}

func TestRefreshIsSingleFlight(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "a",
		RefreshToken: "rt_old",
		ExpiresAt:    now.Add(-time.Minute),
	}}

	var calls int32
	release := make(chan struct{})
	sm := NewSessionManager(func(context.Context, string) (*TokenResponse, error) {
		atomic.AddInt32(&calls, 1)
		<-release // hold the refresh open so every caller piles up behind it
		return &TokenResponse{AccessToken: "a2", ExpiresIn: 600, RefreshToken: "rt_new"}, nil
	}, &memStore{cfg: cfg}, cfg)
	sm.now = fixedNow(now)

	const callers = 8
	var wg sync.WaitGroup
	tokens := make([]string, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = sm.Token(context.Background())
		}(i)
	}

	// Give the goroutines time to arrive, then let the single refresh finish.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("refresh called %d times, want exactly 1", got)
	}
	for i := range callers {
		if errs[i] != nil {
			t.Errorf("caller %d: %v", i, errs[i])
		}
		if tokens[i] != "a2" {
			t.Errorf("caller %d token = %q, want the shared refreshed token", i, tokens[i])
		}
	}
}

func TestRefreshInvalidGrantMeansSessionExpired(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "a",
		RefreshToken: "rt_dead",
		ExpiresAt:    now.Add(-time.Minute),
	}}
	sm := NewSessionManager(func(context.Context, string) (*TokenResponse, error) {
		return nil, &OAuthError{StatusCode: 400, Code: ErrCodeInvalidGrant}
	}, &memStore{cfg: cfg}, cfg)
	sm.now = fixedNow(now)

	_, err := sm.Token(context.Background())
	if !errors.Is(err, ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

func TestRefreshAdoptsSiblingRotationOnInvalidGrant(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "tcli_stale",
		RefreshToken: "rt_old",
		ExpiresAt:    now.Add(-time.Minute),
	}}
	store := &memStore{cfg: cfg}

	var calls int32
	sm := NewSessionManager(func(_ context.Context, rt string) (*TokenResponse, error) {
		atomic.AddInt32(&calls, 1)
		// A sibling process rotated first and persisted a fresh, still-valid
		// session before our stale token was rejected.
		_ = store.Save(&config.Config{Session: &config.Session{
			AccessToken:  "tcli_sibling",
			RefreshToken: "rt_sibling",
			ExpiresAt:    now.Add(time.Hour),
		}})
		return nil, &OAuthError{StatusCode: 400, Code: ErrCodeInvalidGrant}
	}, store, cfg)
	sm.now = fixedNow(now)

	tok, err := sm.ForceRefresh(context.Background())
	if err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if tok != "tcli_sibling" {
		t.Errorf("token = %q, want the sibling's access token", tok)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("refresh calls = %d, want 1 (adopt without a retry)", got)
	}
	if sm.cfg.Session.RefreshToken != "rt_sibling" {
		t.Errorf("in-memory refresh token = %q, want the adopted rt_sibling", sm.cfg.Session.RefreshToken)
	}
}

func TestRefreshRetriesWithSiblingTokenWhenSiblingAccessAlsoStale(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "tcli_stale",
		RefreshToken: "rt_old",
		ExpiresAt:    now.Add(-time.Minute),
	}}
	store := &memStore{cfg: cfg}

	var seen []string
	sm := NewSessionManager(func(_ context.Context, rt string) (*TokenResponse, error) {
		seen = append(seen, rt)
		if rt == "rt_old" {
			// Sibling rotated, but its access token is already stale too.
			_ = store.Save(&config.Config{Session: &config.Session{
				AccessToken:  "tcli_sibling_stale",
				RefreshToken: "rt_sibling",
				ExpiresAt:    now.Add(-time.Minute),
			}})
			return nil, &OAuthError{StatusCode: 400, Code: ErrCodeInvalidGrant}
		}
		return &TokenResponse{AccessToken: "tcli_new", RefreshToken: "rt_new", ExpiresIn: 3600}, nil
	}, store, cfg)
	sm.now = fixedNow(now)

	tok, err := sm.ForceRefresh(context.Background())
	if err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if tok != "tcli_new" {
		t.Errorf("token = %q, want tcli_new from the retry", tok)
	}
	want := []string{"rt_old", "rt_sibling"}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Errorf("refresh tokens tried = %v, want %v", seen, want)
	}
}

func TestTokenWithoutSession(t *testing.T) {
	cfg := &config.Config{}
	sm := NewSessionManager(nil, &memStore{cfg: cfg}, cfg)

	if _, err := sm.Token(context.Background()); !errors.Is(err, ErrNoSession) {
		t.Errorf("err = %v, want ErrNoSession", err)
	}
}

func TestRefreshWithoutRefreshTokenIsExpired(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{Session: &config.Session{AccessToken: "a", ExpiresAt: now.Add(-time.Minute)}}
	sm := NewSessionManager(nil, &memStore{cfg: cfg}, cfg)
	sm.now = fixedNow(now)

	if _, err := sm.Token(context.Background()); !errors.Is(err, ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

func TestRefreshSurfacesPersistFailures(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{Session: &config.Session{
		AccessToken:  "a",
		RefreshToken: "rt_old",
		ExpiresAt:    now.Add(-time.Minute),
	}}
	store := &memStore{cfg: cfg, saveErr: errors.New("disk full")}
	sm := NewSessionManager(func(context.Context, string) (*TokenResponse, error) {
		return &TokenResponse{AccessToken: "a2", ExpiresIn: 60, RefreshToken: "rt_new"}, nil
	}, store, cfg)
	sm.now = fixedNow(now)

	// A rotation we cannot persist leaves the next run with a dead token, so it
	// must not be swallowed.
	if _, err := sm.Token(context.Background()); err == nil {
		t.Error("expected the persist failure to surface")
	}
}

func TestEstablishAndClear(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{}
	store := &memStore{cfg: cfg}
	sm := NewSessionManager(nil, store, cfg)
	sm.now = fixedNow(now)

	err := sm.Establish(&TokenResponse{
		AccessToken:  "tcli_a",
		RefreshToken: "rt_a",
		ExpiresIn:    120,
		Scope:        "cli",
	}, "user@example.test")
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if cfg.Session == nil || cfg.Session.AccessToken != "tcli_a" || cfg.Session.UserEmail != "user@example.test" {
		t.Fatalf("session = %+v", cfg.Session)
	}
	if want := now.Add(2 * time.Minute); !cfg.Session.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want %v", cfg.Session.ExpiresAt, want)
	}

	// Clearing drops the session but leaves API keys alone.
	cfg.Orgs = map[string]*config.OrgCreds{"org_a": {Name: "Alpha", APIKey: "key-alpha"}}
	if err := sm.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if cfg.Session != nil {
		t.Errorf("session survived Clear: %+v", cfg.Session)
	}
	if cfg.Orgs["org_a"].APIKey != "key-alpha" {
		t.Error("Clear removed a stored API key; logout must not")
	}
}
