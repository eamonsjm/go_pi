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

