package transport

import (
	"testing"
)

func TestValidateSessionID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid simple", "abc123", false},
		{"valid uuid-like", "550e8400-e29b-41d4-a716-446655440000", false},
		{"valid all printable", "!\"#$%&'()*+,-./0123456789:;<=>?@ABCZ[\\]^_`abcz{|}~", false},
		{"empty", "", true},
		{"space", "abc 123", true},
		{"tab", "abc\t123", true},
		{"newline", "abc\n123", true},
		{"null byte", "abc\x00123", true},
		{"unicode", "abc\u00e9", true},
		{"too long", string(make([]byte, 1025)), true},
		{"max length", string(makeVisibleASCII(1024)), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSessionID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSessionID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

// makeVisibleASCII returns a byte slice of length n filled with visible ASCII chars.
func makeVisibleASCII(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(0x21 + (i % (0x7E - 0x21 + 1)))
	}
	return b
}
