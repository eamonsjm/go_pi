package tools

import (
	"crypto/md5"
	"fmt"
)

// contentHash computes a 3-character content hash for a line.
// Uses the first 3 characters of the lowercase hex MD5 digest.
func contentHash(line string) string {
	hash := md5.Sum([]byte(line))
	hexHash := fmt.Sprintf("%x", hash)
	// Return first 3 characters of the hash.
	if len(hexHash) >= 3 {
		return hexHash[:3]
	}
	return hexHash
}

// formatLineWithHash formats a line with its content hash for display.
// Format: "linenum<TAB>hash<TAB>content"
func formatLineWithHash(lineNum int, content string) string {
	hash := contentHash(content)
	return fmt.Sprintf("%d\t%s\t%s", lineNum, hash, content)
}
