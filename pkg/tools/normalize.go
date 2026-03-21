package tools

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// normalizer describes a single text normalization pass.
type normalizer struct {
	name string
	fn   func(string) string
}

// normalizers is the ordered list of fuzzy-match normalizations applied when an
// exact match fails. Each entry is tried independently against the original
// file content — they are NOT composed. If a normalizer produces exactly one
// match it wins; otherwise we continue to the next.
var normalizers = []normalizer{
	{"smart quotes → straight quotes", normalizeQuotes},
	{"Unicode NFC normalization", normalizeUnicode},
	{"whitespace normalization (tabs ↔ spaces)", normalizeWhitespace},
	{"combined (quotes + unicode + whitespace)", normalizeCombined},
}

// normalizeQuotes replaces common smart/curly quote characters with their
// ASCII equivalents.
func normalizeQuotes(s string) string {
	r := strings.NewReplacer(
		"\u2018", "'", // left single
		"\u2019", "'", // right single
		"\u201C", "\"", // left double
		"\u201D", "\"", // right double
		"\u2032", "'", // prime
		"\u2033", "\"", // double prime
		"\u00AB", "\"", // left guillemet
		"\u00BB", "\"", // right guillemet
		"\u2039", "'", // single left guillemet
		"\u203A", "'", // single right guillemet
		"\u201A", ",", // single low-9
		"\u201E", "\"", // double low-9
	)
	return r.Replace(s)
}

// normalizeUnicode applies NFC normalization to collapse combining characters
// and other Unicode equivalences.
func normalizeUnicode(s string) string {
	return norm.NFC.String(s)
}

// normalizeWhitespace converts tabs to spaces (4-space indent). This is a
// character-level substitution so it composes correctly with the back-mapping
// in mapNormalizedRange.
func normalizeWhitespace(s string) string {
	return strings.ReplaceAll(s, "\t", "    ")
}

// normalizeCombined applies all normalizations together.
func normalizeCombined(s string) string {
	s = normalizeQuotes(s)
	s = normalizeUnicode(s)
	s = normalizeWhitespace(s)
	return s
}

// fuzzyFind attempts to find old_string in content using progressively looser
// normalizations. It returns:
//   - matchedContent: the actual substring from content that matched
//   - normName: which normalizer succeeded (for reporting)
//   - found: whether a unique match was found
func fuzzyFind(content, oldString string) (matchedContent string, normName string, found bool) {
	for _, n := range normalizers {
		normContent := n.fn(content)
		normOld := n.fn(oldString)

		count := strings.Count(normContent, normOld)
		if count != 1 {
			continue
		}

		// Found unique match in normalized space. Map back to original content.
		idx := strings.Index(normContent, normOld)
		if idx < 0 {
			continue
		}

		// Map the normalized index back to the original string.
		// We walk both strings in parallel to find the corresponding range.
		original := mapNormalizedRange(content, n.fn, idx, idx+len(normOld))
		if original != "" {
			return original, n.name, true
		}
	}
	return "", "", false
}

// mapNormalizedRange maps a byte range [start, end) in the normalized version
// of s back to the corresponding substring in the original s.
//
// Strategy: walk through s character by character, normalizing the prefix so
// far, and track when we cross the start and end boundaries in normalized
// space.
func mapNormalizedRange(s string, normFn func(string) string, normStart, normEnd int) string {
	// Optimization: work line-by-line to narrow down the search.
	lines := strings.SplitAfter(s, "\n")
	normOffset := 0
	origOffset := 0

	// Find which lines contain the match.
	startLine := -1
	endLine := -1
	lineNormOffsets := make([]int, 0, len(lines)) // normOffset at start of each line

	for i, line := range lines {
		normLine := normFn(line)
		lineNormOffsets = append(lineNormOffsets, normOffset)

		if startLine < 0 && normOffset+len(normLine) > normStart {
			startLine = i
		}
		if endLine < 0 && normOffset+len(normLine) >= normEnd {
			endLine = i
		}

		normOffset += len(normLine)
		origOffset += len(line)
	}

	if startLine < 0 || endLine < 0 {
		return ""
	}

	// Reconstruct the relevant original lines and find precise byte offsets.
	var origChunk strings.Builder
	for i := startLine; i <= endLine; i++ {
		origChunk.WriteString(lines[i])
	}
	chunk := origChunk.String()
	chunkNormStart := lineNormOffsets[startLine]

	// The target in normalized space relative to chunk start.
	relStart := normStart - chunkNormStart
	relEnd := normEnd - chunkNormStart

	// Walk through the original chunk, character by character, tracking the
	// normalized position to find original byte boundaries.
	normPos := 0
	origStart := -1
	origEnd := -1

	for i := 0; i < len(chunk); {
		if normPos == relStart && origStart < 0 {
			// Check: does normalizing from here produce the right start?
			origStart = i
		}

		_, size := utf8.DecodeRuneInString(chunk[i:])
		charNorm := normFn(chunk[i : i+size])
		normPos += len(charNorm)

		if normPos >= relEnd && origEnd < 0 {
			origEnd = i + size
			break
		}

		i += size
	}

	// Handle edge case: match starts at position 0.
	if origStart < 0 && relStart == 0 {
		origStart = 0
	}

	if origStart < 0 || origEnd < 0 {
		return ""
	}

	return chunk[origStart:origEnd]
}

// detectBOM checks if data starts with a UTF-8 BOM and returns the BOM prefix
// and the content without it.
func detectBOM(data []byte) (bom []byte, content []byte) {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[:3], data[3:]
	}
	return nil, data
}

// detectLineEnding determines whether the content uses CRLF or LF line endings.
// Returns "\r\n" for CRLF, "\n" for LF.
func detectLineEnding(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}
