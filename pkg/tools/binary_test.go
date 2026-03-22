package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIsBinary(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"empty", nil, false},
		{"plain text", []byte("hello world"), false},
		{"null bytes above threshold", make([]byte, 100), true},
		{"invalid utf8", []byte{0xff, 0xfe, 0x80, 0x81, 0x82}, true},
		{"high null ratio in short text", []byte("hello\x00world"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBinary(tt.data); got != tt.want {
				t.Errorf("isBinary() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsBinaryFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("nonexistent file", func(t *testing.T) {
		if got := isBinaryFile(context.Background(), filepath.Join(dir, "nope")); got {
			t.Error("isBinaryFile() = true for nonexistent file, want false")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		p := filepath.Join(dir, "empty")
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := isBinaryFile(context.Background(), p); got {
			t.Error("isBinaryFile() = true for empty file, want false")
		}
	})

	t.Run("text file", func(t *testing.T) {
		p := filepath.Join(dir, "text.txt")
		if err := os.WriteFile(p, []byte("hello world\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := isBinaryFile(context.Background(), p); got {
			t.Error("isBinaryFile() = true for text file, want false")
		}
	})

	t.Run("binary file", func(t *testing.T) {
		p := filepath.Join(dir, "bin")
		if err := os.WriteFile(p, make([]byte, 200), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := isBinaryFile(context.Background(), p); !got {
			t.Error("isBinaryFile() = false for binary file, want true")
		}
	})
}
