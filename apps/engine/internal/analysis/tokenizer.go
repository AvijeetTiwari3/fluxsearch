// Package analysis provides NLP text processing primitives for FluxSearch:
// tokenization, Porter stemming (Steps 1-5), stop-word removal, and n-gram generation.
package analysis

import (
	"strings"
	"unicode"
)

// Tokenize splits text into lowercase Unicode word tokens, stripping all non-alphanumeric
// characters. Handles CJK ideographs by treating each rune as its own token.
//
// Time: O(n) where n = len(text)
// Space: O(k) where k = number of tokens
func Tokenize(text string) []string {
	var tokens []string
	var buf strings.Builder

	flush := func() {
		if buf.Len() > 0 {
			tokens = append(tokens, buf.String())
			buf.Reset()
		}
	}

	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// CJK Unified Ideographs — treat each character as its own token
			if r >= 0x4E00 && r <= 0x9FFF {
				flush()
				tokens = append(tokens, strings.ToLower(string(r)))
			} else {
				buf.WriteRune(unicode.ToLower(r))
			}
		case r == '\'' || r == '\u2019': // apostrophe / right-single-quote → keep contractions
			buf.WriteRune('\'')
		default:
			flush()
		}
	}
	flush()
	return tokens
}

// NormalizeToken strips leading/trailing apostrophes and returns the cleaned token.
func NormalizeToken(tok string) string {
	return strings.Trim(tok, "'")
}
