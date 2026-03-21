package tools

import (
	"os"
	"unicode/utf8"
)

const (
	binarySampleSize    = 8192
	binaryNullThreshold = 0.01 // if > 1% null bytes, consider binary
)

// isBinary returns true if the data appears to be binary content.
// It checks for a high ratio of null bytes and invalid UTF-8.
func isBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	sample := data
	if len(sample) > binarySampleSize {
		sample = sample[:binarySampleSize]
	}

	nullCount := 0
	for _, b := range sample {
		if b == 0 {
			nullCount++
		}
	}

	if float64(nullCount)/float64(len(sample)) > binaryNullThreshold {
		return true
	}

	if !utf8.Valid(sample) {
		return true
	}

	return false
}

// isBinaryFile checks if a file appears to be binary by reading its first bytes.
func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, binarySampleSize)
	n, _ := f.Read(buf)
	if n == 0 {
		return false
	}

	return isBinary(buf[:n])
}
