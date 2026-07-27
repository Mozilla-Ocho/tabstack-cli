package cmd

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
)

// callbackTimeout is how long we hold the loopback listener open waiting for
// the browser to come back. Long enough to log in and pick an org on the
// consent screen, short enough that an abandoned login does not wedge a shell.
const callbackTimeout = 2 * time.Minute

// callbackPath is the only path the loopback server answers. Everything else
// gets a 404: a stray request from another tab must not be mistaken for the
// authorization response.
const callbackPath = "/callback"

// browserOpener opens a URL in the user's browser. It is a package var so the
// end-to-end login test can drive the whole flow without a browser.
type browserOpener func(url string) error

var openBrowser browserOpener = defaultOpenBrowser

// hasDisplay reports whether this machine plausibly has a browser to open. A
// var so tests can simulate a headless box.
var hasDisplay = func() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default:
		return envAny("DISPLAY", "WAYLAND_DISPLAY")
	}
}

func newAuthLoginCmd() *cobra.Command {
	var (
		keySetup string
		noKey    bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in with your browser (OAuth 2.1 + PKCE)",
		Long: "Open the Tabstack console in your browser, sign in, and store the\n" +
			"resulting session. The session is user scoped; API keys are organisation\n" +
			"scoped and set up separately at the end of login.",
		Annotations: map[string]string{"skipClient": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := keySetupModeFrom(keySetup, noKey)
			if err != nil {
				return withCode(2, err)
			}
			return runLogin(cmd.Context(), mode, flagOrg)
		},
	}

	cmd.Flags().StringVar(&keySetup, "api-key-setup", "",
		"what to do about an API key after signing in: create|existing|skip")
	cmd.Flags().BoolVar(&noKey, "no-key", false, "alias for --api-key-setup=skip")
	return cmd
}

// runLogin performs the authorization code + PKCE flow, stores the session, and
// then sets up an organisation API key.
func runLogin(ctx context.Context, mode keySetupMode, orgHint string) error {
	r := rootApp.renderer
	cfg := rootApp.cfg

	if !hasDisplay() {
		return withCode(2, errors.New(
			"no browser available on this machine. For non-interactive environments, "+
				"create a key in the console and set TABSTACK_API_KEY instead"))
	}

	verifier, err := console.NewVerifier()
	if err != nil {
		return withCode(1, err)
	}
	state, err := console.NewState()
	if err != nil {
		return withCode(1, err)
	}

	// Bind first so the redirect URI carries a port we know is ours. Loopback
	// uses the 127.0.0.1 literal rather than "localhost", which can resolve to
	// IPv6 or to something a hosts file has redirected.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return withCode(1, fmt.Errorf("bind loopback listener: %w", err))
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, callbackPath)

	c, sm := consoleClient()
	authURL := c.AuthorizeURL(console.AuthorizeParams{
		RedirectURI: redirectURI,
		Challenge:   console.Challenge(verifier),
		State:       state,
		OrgID:       orgHint,
	})

	results := make(chan callbackResult, 1)
	srv := &http.Server{
		Handler:           newCallbackHandler(state, c.AuthURL(), results),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	fmt.Fprintf(r.Err, "Opening your browser to sign in:\n  %s\n", authURL)
	if err := openBrowser(authURL); err != nil {
		// The listener stays up: the user can open the URL by hand, including on
		// the same machine from a different browser.
		fmt.Fprintf(r.Err, "Could not open a browser (%v).\nOpen the URL above manually to continue.\n", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, callbackTimeout)
	defer cancel()

	var res callbackResult
	select {
	case res = <-results:
	case <-waitCtx.Done():
		return withCode(1, fmt.Errorf("timed out after %s waiting for the browser to complete sign-in", callbackTimeout))
	}
	if res.err != nil {
		return withCode(1, res.err)
	}

	tok, err := c.ExchangeCode(ctx, res.code, verifier, redirectURI)
	if err != nil {
		return classifyOAuthError(err)
	}
	if err := sm.Establish(tok, ""); err != nil {
		return withCode(1, fmt.Errorf("save session: %w", err))
	}

	me, err := c.Me(ctx)
	if err != nil {
		return classifyConsoleError(err)
	}

	sm.SetEmail(me.User.Email)
	for _, o := range me.Organizations {
		cfg.UpsertOrg(o.ID, o.Name)
	}
	cfg.ActiveOrg = pickActiveOrg(me)
	if !me.Session.ExpiresAt.IsZero() && cfg.Session != nil {
		// Prefer the server's view of when this session dies over our own
		// arithmetic on expires_in.
		cfg.Session.ExpiresAt = me.Session.ExpiresAt
	}
	if err := rootApp.store.Save(cfg); err != nil {
		return withCode(1, fmt.Errorf("save config: %w", err))
	}

	if cfg.ActiveOrg == "" {
		fmt.Fprintln(r.Err, "signed in, but your user has no organisations; ask an org owner for an invite")
	} else if err := runKeySetup(ctx, c, cfg.ActiveOrg, mode); err != nil {
		return err
	}

	printLoginSummary(r)
	return nil
}

// pickActiveOrg chooses the organisation to make active after login. The
// server's default_org wins; a user with exactly one org gets that one, since
// leaving them with no active org would only produce a confusing first error.
func pickActiveOrg(me *console.Me) string {
	if me.DefaultOrg != "" {
		return me.DefaultOrg
	}
	if len(me.Organizations) == 1 {
		return me.Organizations[0].ID
	}
	return ""
}

// printLoginSummary prints the four things a user needs after signing in: who
// they are, which org they are acting as, how long the session lasts, and
// whether a product credential is in place.
func printLoginSummary(r uiRenderer) {
	cfg := rootApp.cfg
	email := "(unknown email)"
	if cfg.Session != nil && cfg.Session.UserEmail != "" {
		email = cfg.Session.UserEmail
	}
	fmt.Fprintf(r.Out, "\n%s signed in as %s\n", r.Styles.Success.Render("✓"), email)
	if cfg.ActiveOrg != "" {
		fmt.Fprintf(r.Out, "%s %s (%s)\n", r.Styles.Key.Render("active org:"), cfg.OrgName(cfg.ActiveOrg), cfg.ActiveOrg)
	}
	if cfg.Session != nil && !cfg.Session.ExpiresAt.IsZero() {
		printExpiry(r, cfg.Session.ExpiresAt)
	}
	printKeyState(r, cfg)
}

// callbackResult is what the loopback handler hands back: an authorization code,
// or the reason we will not be exchanging one.
type callbackResult struct {
	code string
	err  error
}

// newCallbackHandler builds the one-shot loopback handler.
//
// The response page deliberately contains neither the code nor the state, and
// sets Referrer-Policy: no-referrer so nothing in the query string leaks onward
// through a referrer header.
func newCallbackHandler(expectedState, authURL string, results chan<- callbackResult) http.Handler {
	var once sync.Once
	deliver := func(res callbackResult) {
		once.Do(func() { results <- res })
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != callbackPath {
			http.NotFound(w, req)
			return
		}

		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		q := req.URL.Query()

		fail := func(status int, err error) {
			w.WriteHeader(status)
			fmt.Fprint(w, page("Sign-in failed", "Sign-in failed. Return to your terminal for details."))
			deliver(callbackResult{err: err})
		}

		if e := q.Get("error"); e != "" {
			msg := e
			if d := q.Get("error_description"); d != "" {
				msg += ": " + d
			}
			fail(http.StatusBadRequest, fmt.Errorf("authorisation failed: %s", msg))
			return
		}

		// Constant-time compare: state is the CSRF defence for this callback, so
		// it is treated like any other secret comparison.
		got, want := q.Get("state"), expectedState
		if len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			fail(http.StatusBadRequest, errors.New("callback state did not match; sign-in aborted without exchanging the code"))
			return
		}

		// RFC 9207 issuer identification. Servers that do not send iss are
		// accepted; one that sends the wrong iss is a mixed-up-issuer attack and
		// the code is discarded unexchanged.
		if iss := q.Get("iss"); iss != "" && !sameURL(iss, authURL) {
			fail(http.StatusBadRequest, fmt.Errorf("callback issuer %q does not match the configured auth host; sign-in aborted", iss))
			return
		}

		code := q.Get("code")
		if code == "" {
			fail(http.StatusBadRequest, errors.New("callback carried no authorization code"))
			return
		}

		fmt.Fprint(w, page("Signed in", "Signed in. You can close this tab and return to your terminal."))
		deliver(callbackResult{code: code})
	})
}

// page renders the minimal callback response body. No credentials, no query
// parameters, no external resources.
func page(title, body string) string {
	return "<!doctype html><html><head><meta charset=\"utf-8\"><title>" + title +
		"</title></head><body style=\"font-family:system-ui,sans-serif;padding:3rem\"><p>" +
		body + "</p></body></html>"
}

// sameURL compares two URLs for issuer purposes, ignoring a trailing slash.
func sameURL(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// classifyOAuthError maps token endpoint failures onto exit codes, translating
// the RFC 6749 error codes into something a user can act on.
func classifyOAuthError(err error) error {
	var oe *console.OAuthError
	if !errors.As(err, &oe) {
		return classifyError(err)
	}
	switch oe.Code {
	case console.ErrCodeInvalidGrant:
		return withCode(3, fmt.Errorf("the authorization code was rejected (%s). Run `tabstack auth login` again", oe.Error()))
	case console.ErrCodeInvalidScope:
		return withCode(3, fmt.Errorf("the requested scopes were rejected (%s). Override them with TABSTACK_OAUTH_SCOPES if your console expects different names", oe.Error()))
	case console.ErrCodeInvalidClient, console.ErrCodeUnauthorizedClient, console.ErrCodeUnsupportedGrant, console.ErrCodeInvalidRequest:
		return withCode(3, fmt.Errorf("the authorization server rejected this client (%s)", oe.Error()))
	default:
		return withCode(3, err)
	}
}

// defaultOpenBrowser launches the platform's URL handler. It starts the process
// without waiting: we care that the browser was handed the URL, not what it
// does next.
func defaultOpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
