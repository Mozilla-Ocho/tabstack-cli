package console

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// entropyBytes is the raw length of the PKCE verifier and the state value
// before base64url encoding.
const entropyBytes = 32

// CodeChallengeMethodS256 is the only challenge method we emit or accept. The
// `plain` method is not implemented anywhere in this package on purpose.
const CodeChallengeMethodS256 = "S256"

// NewVerifier returns a fresh PKCE code verifier: 32 crypto/rand bytes,
// base64url encoded without padding.
func NewVerifier() (string, error) { return randomB64(entropyBytes) }

// NewState returns a fresh OAuth state value, generated the same way as the
// verifier.
func NewState() (string, error) { return randomB64(entropyBytes) }

// Challenge derives the S256 code challenge for a verifier: base64url, no
// padding, of the SHA-256 of the verifier's ASCII bytes.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// randomB64 reads n bytes from crypto/rand and encodes them base64url without
// padding, which keeps the value safe to put in a query string unescaped.
func randomB64(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
