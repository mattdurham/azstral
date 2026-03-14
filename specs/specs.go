// Package specs extracts SPEC, NOTE, and TEST identifiers from Go comments.
// SPEC-002: Extract identifiers and link them to annotated code.
package specs

import (
	"regexp"
	"strings"
)

// IdentKind is the type of a spec identifier (SPEC, NOTE, TEST, BENCH).
type IdentKind string

const (
	IdentSpec  IdentKind = "SPEC"
	IdentNote  IdentKind = "NOTE"
	IdentTest  IdentKind = "TEST"
	IdentBench IdentKind = "BENCH"
)

// Identifier represents a parsed spec/note/test reference found in a comment.
type Identifier struct {
	Kind IdentKind
	ID   string // e.g. "SPEC-001"
	Full string // the full match text
}

var identPattern = regexp.MustCompile(`\b(SPEC|NOTE|TEST|BENCH)-(\d{1,4})\b`)

// Extract finds all spec identifiers in a comment string.
func Extract(comment string) []Identifier {
	matches := identPattern.FindAllStringSubmatch(comment, -1)
	var result []Identifier
	for _, m := range matches {
		result = append(result, Identifier{
			Kind: IdentKind(m[1]),
			ID:   m[0],
			Full: m[0],
		})
	}
	return result
}

// IsSpecComment returns true if the comment contains any spec identifiers.
func IsSpecComment(comment string) bool {
	return identPattern.MatchString(comment)
}

// NormalizeComment strips leading // or /* */ markers from a comment.
func NormalizeComment(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "//")
	text = strings.TrimPrefix(text, "/*")
	text = strings.TrimSuffix(text, "*/")
	return strings.TrimSpace(text)
}
