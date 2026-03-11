package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	p, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}

	if len(p.Verifier) == 0 {
		t.Fatal("verifier is empty")
	}
	if len(p.Challenge) == 0 {
		t.Fatal("challenge is empty")
	}

	// Verify the challenge is SHA-256(verifier) base64url-encoded.
	hash := sha256.Sum256([]byte(p.Verifier))
	expected := base64.RawURLEncoding.EncodeToString(hash[:])
	if p.Challenge != expected {
		t.Errorf("challenge mismatch:\n  got  %q\n  want %q", p.Challenge, expected)
	}
}

func TestGeneratePKCE_Unique(t *testing.T) {
	p1, _ := GeneratePKCE()
	p2, _ := GeneratePKCE()
	if p1.Verifier == p2.Verifier {
		t.Error("two PKCE pairs should have different verifiers")
	}
}

func TestBase64URLEncode_NoPadding(t *testing.T) {
	// Ensure no padding characters appear.
	data := []byte{0, 1, 2, 3, 4, 5}
	encoded := base64URLEncode(data)
	for _, c := range encoded {
		if c == '=' || c == '+' || c == '/' {
			t.Errorf("base64url should not contain %q: %s", string(c), encoded)
		}
	}
}
