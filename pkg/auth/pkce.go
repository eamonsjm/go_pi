package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// PKCEChallenge holds a PKCE code verifier and its derived challenge.
type PKCEChallenge struct {
	Verifier  string // Random base64url string sent in token exchange.
	Challenge string // SHA-256 hash of Verifier, base64url-encoded.
}

// GeneratePKCE creates a new PKCE code verifier and challenge pair.
// Uses 32 bytes of cryptographic randomness (RFC 7636 recommends 32-96 bytes).
func GeneratePKCE() (*PKCEChallenge, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}

	verifier := base64URLEncode(buf)

	hash := sha256.Sum256([]byte(verifier))
	challenge := base64URLEncode(hash[:])

	return &PKCEChallenge{
		Verifier:  verifier,
		Challenge: challenge,
	}, nil
}

// base64URLEncode encodes bytes as unpadded base64url (RFC 4648 §5).
func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
