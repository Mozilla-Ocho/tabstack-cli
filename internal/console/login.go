// Package console talks to the Tabstack console: the OAuth 2.1 authorization
// server that issues CLI sessions, and the /cli/* management API those sessions
// authenticate against. It is separate from internal/client, which calls the
// product API with a static API key.
package console

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	// ClientID is the public client registered for this CLI. There is no secret;
	// PKCE is what binds the code to this process.
	ClientID = "tabstack-cli"

	// DefaultScopes: `cli` grants the /cli/* management API, `offline_access` is
	// required for the server to issue a refresh token.
	DefaultScopes = "cli offline_access"

	// loginTimeout bounds how long we wait for the user to finish in the browser.
	loginTimeout = 5 * time.Minute
)

// LoginResult is a completed authorization: the issued session plus the org the
// user selected on the consent screen.
type LoginResult struct {
	Token *oauth2.Token
}

// Login runs the OAuth 2.1 authorization-code flow with PKCE over a loopback
// redirect (RFC 8252). It starts a one-shot HTTP server on 127.0.0.1, opens the
// consent screen, waits for the redirect, and exchanges the code.
//
// The listener binds the loopback IP literal, not "localhost": RFC 8252 §8.3
// marks localhost NOT RECOMMENDED, and the console rejects it.
func Login(ctx context.Context, authURL string, scopes string, orgID string, openBrowser bool) (*LoginResult, error) {
	authURL = strings.TrimSuffix(authURL, "/")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not open a local callback port: %w", err)
	}
	defer listener.Close()

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", listener.Addr().(*net.TCPAddr).Port)

	conf := &oauth2.Config{
		ClientID:    ClientID,
		RedirectURL: redirectURI,
		Scopes:      strings.Fields(scopes),
		Endpoint: oauth2.Endpoint{
			AuthURL:   authURL + "/oauth/authorize",
			TokenURL:  authURL + "/oauth/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}

	state, err := randomString()
	if err != nil {
		return nil, err
	}
	verifier := oauth2.GenerateVerifier()

	opts := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(verifier),
		// RFC 8707: bind the session to the console as its audience.
		oauth2.SetAuthURLParam("resource", authURL),
	}
	if orgID != "" {
		opts = append(opts, oauth2.SetAuthURLParam("organization_id", orgID))
	}
	consentURL := conf.AuthCodeURL(state, opts...)

	type callback struct {
		code string
		iss  string
		err  error
	}
	results := make(chan callback, 1)

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			// Send on the channel only after the response is on the wire. Signalling
			// first lets Login return and shut the server down mid-write, which the
			// browser sees as a reset page.
			var outcome callback
			switch {
			case q.Get("error") != "":
				writeBrowserPage(w, "Authorization failed", "You can close this window and try again.")
				outcome = callback{err: fmt.Errorf("authorization failed: %s", describeOAuthError(q.Get("error")))}
			case q.Get("state") != state:
				writeBrowserPage(w, "Authorization failed", "The request could not be verified.")
				outcome = callback{err: errors.New("state mismatch: the callback did not come from the sign-in you started")}
			default:
				// Hand the browser back to the console so the user lands on the
				// dashboard with confirmation instead of a page served from here.
				http.Redirect(w, r, authURL+"/oauth/connected", http.StatusFound)
				outcome = callback{code: q.Get("code"), iss: q.Get("iss")}
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			results <- outcome
		}),
	}
	go server.Serve(listener)
	// Shutdown (not Close) waits for the callback response to finish sending.
	defer func() {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Println("Opening your browser to authorize the Tabstack CLI.")
	fmt.Printf("If it does not open, visit:\n\n  %s\n\n", consentURL)
	if openBrowser {
		if err := open(consentURL); err != nil {
			fmt.Println("Could not open a browser automatically; use the URL above.")
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	var cb callback
	select {
	case cb = <-results:
	case <-waitCtx.Done():
		return nil, errors.New("timed out waiting for the browser. Run `tabstack auth login` again")
	}
	if cb.err != nil {
		return nil, cb.err
	}
	if cb.code == "" {
		return nil, errors.New("no authorization code was returned")
	}

	// RFC 9207: the issuer in the callback must be the server we sent the user
	// to, which defends against a mix-up with another authorization server.
	if cb.iss != "" && strings.TrimSuffix(cb.iss, "/") != authURL {
		return nil, fmt.Errorf("issuer mismatch: expected %s, got %s", authURL, cb.iss)
	}

	exchangeOpts := []oauth2.AuthCodeOption{oauth2.VerifierOption(verifier)}
	// Label the session with this machine so `auth sessions` and the console list
	// are readable; without it the server falls back to the User-Agent.
	if host, err := os.Hostname(); err == nil && host != "" {
		exchangeOpts = append(exchangeOpts, oauth2.SetAuthURLParam("label", host))
	}

	token, err := conf.Exchange(ctx, cb.code, exchangeOpts...)
	if err != nil {
		return nil, fmt.Errorf("could not exchange the authorization code: %w", err)
	}
	return &LoginResult{Token: token}, nil
}

// TokenSource returns a source that refreshes the session as needed, so callers
// never hand an expired access token to the management API. Compare the returned
// token against the stored one to detect (and persist) a rotation.
func TokenSource(ctx context.Context, authURL string, token *oauth2.Token) oauth2.TokenSource {
	conf := &oauth2.Config{
		ClientID: ClientID,
		Endpoint: oauth2.Endpoint{
			TokenURL:  strings.TrimSuffix(authURL, "/") + "/oauth/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	return conf.TokenSource(ctx, token)
}

func randomString() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// describeOAuthError turns the RFC 6749 error code into something a user can act
// on. Unknown codes pass through unchanged.
func describeOAuthError(code string) string {
	switch code {
	case "access_denied":
		return "you cancelled the request"
	case "invalid_request":
		return "the console rejected the request (invalid client, redirect, or organization)"
	default:
		return code
	}
}

func writeBrowserPage(w http.ResponseWriter, heading, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><title>%s</title></head>
<body style="font-family:system-ui,sans-serif;text-align:center;padding:4rem">
<h1>%s</h1><p>%s</p></body></html>`, heading, heading, detail)
}

// open launches the platform's default browser for rawURL.
func open(rawURL string) error {
	if _, err := url.Parse(rawURL); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", rawURL).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
	default:
		return exec.Command("xdg-open", rawURL).Start()
	}
}
