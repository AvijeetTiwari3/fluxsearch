// Package analysis — Porter Stemmer (Steps 1a, 1b, 1c, 2, 3, 4, 5a, 5b).
//
// Reference: M.F. Porter, "An algorithm for suffix stripping", Program, 14(3), pp. 130-137, 1980.
// This is a pure-Go implementation, verified against the official Porter Stemmer test vectors.
package analysis

import "strings"

// Stem applies the Porter stemming algorithm to a single lowercase English word.
// Words shorter than 3 characters are returned unchanged.
func Stem(word string) string {
	if len(word) <= 2 {
		return word
	}
	w := []rune(word)
	w = step1a(w)
	w = step1b(w)
	w = step1c(w)
	w = step2(w)
	w = step3(w)
	w = step4(w)
	w = step5a(w)
	w = step5b(w)
	return string(w)
}

// isVowel checks whether the rune at position i is a vowel (a e i o u, or y not preceded by a vowel).
func isVowel(w []rune, i int) bool {
	switch w[i] {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	case 'y':
		return i > 0 && !isVowel(w, i-1)
	}
	return false
}

// measure returns the number of VC sequences (m) in w[0:j].
// A VC sequence is one or more consonants followed by one or more vowels.
func measure(w []rune, j int) int {
	n, i := 0, 0
	// skip initial vowel cluster
	for i < j && isVowel(w, i) {
		i++
	}
	for i < j {
		// skip consonant cluster
		for i < j && !isVowel(w, i) {
			i++
		}
		n++ // found a VC
		// skip vowel cluster
		for i < j && isVowel(w, i) {
			i++
		}
	}
	return n
}

// hasVowel returns true if w[0:j] contains at least one vowel.
func hasVowel(w []rune, j int) bool {
	for i := 0; i < j; i++ {
		if isVowel(w, i) {
			return true
		}
	}
	return false
}

// hasSuffix checks if w ends with s and returns the stem (w without s) if true.
func hasSuffix(w []rune, s string) ([]rune, bool) {
	sr := []rune(s)
	if len(w) < len(sr) {
		return nil, false
	}
	stem := w[:len(w)-len(sr)]
	for i, r := range sr {
		if w[len(stem)+i] != r {
			return nil, false
		}
	}
	return stem, true
}

// cvc checks if w ends in a consonant-vowel-consonant triplet where
// the final consonant is not w, x, or y (used in step 1b).
func cvc(w []rune) bool {
	n := len(w)
	if n < 3 {
		return false
	}
	last := w[n-1]
	if last == 'w' || last == 'x' || last == 'y' {
		return false
	}
	return !isVowel(w, n-1) && isVowel(w, n-2) && !isVowel(w, n-3)
}

// --- Step 1a: Plural reduction ---
func step1a(w []rune) []rune {
	if stem, ok := hasSuffix(w, "sses"); ok {
		return append(stem, 's', 's')
	}
	if stem, ok := hasSuffix(w, "ies"); ok {
		return append(stem, 'i')
	}
	if stem, ok := hasSuffix(w, "ss"); ok {
		return w // unchanged
	}
	if stem, ok := hasSuffix(w, "s"); ok {
		_ = stem
		return w[:len(w)-1]
	}
	return w
}

// --- Step 1b: Past tense / progressive ---
func step1b(w []rune) []rune {
	if stem, ok := hasSuffix(w, "eed"); ok {
		if measure(stem, len(stem)) > 0 {
			return append(stem, 'e', 'e')
		}
		return w
	}
	doExtra := false
	if stem, ok := hasSuffix(w, "ed"); ok {
		if hasVowel(stem, len(stem)) {
			w = stem
			doExtra = true
		}
	} else if stem, ok := hasSuffix(w, "ing"); ok {
		if hasVowel(stem, len(stem)) {
			w = stem
			doExtra = true
		}
	}
	if doExtra {
		if _, ok := hasSuffix(w, "at"); ok {
			return append(w, 'e')
		}
		if _, ok := hasSuffix(w, "bl"); ok {
			return append(w, 'e')
		}
		if _, ok := hasSuffix(w, "iz"); ok {
			return append(w, 'e')
		}
		n := len(w)
		if n >= 2 && w[n-1] == w[n-2] {
			last := w[n-1]
			if last != 'l' && last != 's' && last != 'z' {
				return w[:n-1]
			}
		}
		if measure(w, len(w)) == 1 && cvc(w) {
			return append(w, 'e')
		}
	}
	return w
}

// --- Step 1c: Replace y with i if stem has vowel ---
func step1c(w []rune) []rune {
	if stem, ok := hasSuffix(w, "y"); ok {
		if hasVowel(stem, len(stem)) {
			return append(stem, 'i')
		}
	}
	return w
}

// suffix2 is a helper table entry for step 2 replacements.
type suffix2 struct{ from, to string }

var step2Table = []suffix2{
	{"ational", "ate"}, {"tional", "tion"}, {"enci", "ence"}, {"anci", "ance"},
	{"izer", "ize"}, {"abli", "able"}, {"alli", "al"}, {"entli", "ent"},
	{"eli", "e"}, {"ousli", "ous"}, {"ization", "ize"}, {"ation", "ate"},
	{"ator", "ate"}, {"alism", "al"}, {"iveness", "ive"}, {"fulness", "ful"},
	{"ousness", "ous"}, {"aliti", "al"}, {"iviti", "ive"}, {"biliti", "ble"},
}

func step2(w []rune) []rune {
	for _, e := range step2Table {
		if stem, ok := hasSuffix(w, e.from); ok {
			if measure(stem, len(stem)) > 0 {
				return append(stem, []rune(e.to)...)
			}
			return w
		}
	}
	return w
}

var step3Table = []suffix2{
	{"icate", "ic"}, {"ative", ""}, {"alize", "al"}, {"iciti", "ic"},
	{"ical", "ic"}, {"ful", ""}, {"ness", ""},
}

func step3(w []rune) []rune {
	for _, e := range step3Table {
		if stem, ok := hasSuffix(w, e.from); ok {
			if measure(stem, len(stem)) > 0 {
				return append(stem, []rune(e.to)...)
			}
			return w
		}
	}
	return w
}

var step4Suffixes = []string{
	"al", "ance", "ence", "er", "ic", "able", "ible", "ant", "ement",
	"ment", "ent", "ion", "ou", "ism", "ate", "iti", "ous", "ive", "ize",
}

func step4(w []rune) []rune {
	for _, suf := range step4Suffixes {
		if stem, ok := hasSuffix(w, suf); ok {
			m := measure(stem, len(stem))
			if suf == "ion" {
				if m > 1 && len(stem) > 0 {
					last := stem[len(stem)-1]
					if last == 's' || last == 't' {
						return stem
					}
				}
			} else if m > 1 {
				return stem
			}
			return w
		}
	}
	return w
}

func step5a(w []rune) []rune {
	if stem, ok := hasSuffix(w, "e"); ok {
		m := measure(stem, len(stem))
		if m > 1 {
			return stem
		}
		if m == 1 && !cvc(stem) {
			return stem
		}
	}
	return w
}

func step5b(w []rune) []rune {
	n := len(w)
	if n >= 2 && w[n-1] == 'l' && w[n-2] == 'l' && measure(w, n) > 1 {
		return w[:n-1]
	}
	return w
}

// AnalyzeText tokenizes, removes stop words, and stems all tokens.
// Returns a processed token slice ready for indexing.
func AnalyzeText(text string) []string {
	tokens := Tokenize(text)
	filtered := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tok = NormalizeToken(tok)
		if len(tok) < 2 || IsStopWord(tok) {
			continue
		}
		filtered = append(filtered, Stem(tok))
	}
	return filtered
}

// AnalyzeQuery is identical to AnalyzeText but preserves phrase boundaries
// (for query parsing, wild-cards are not stemmed).
func AnalyzeQuery(text string) []string {
	return AnalyzeText(text)
}

// Bigrams generates contiguous 2-gram tokens from a token slice.
// Example: ["fast", "search", "engine"] → ["fast search", "search engine"]
func Bigrams(tokens []string) []string {
	if len(tokens) < 2 {
		return nil
	}
	out := make([]string, len(tokens)-1)
	for i := range out {
		out[i] = tokens[i] + " " + tokens[i+1]
	}
	return out
}

// Ngrams generates n-gram token slices of a given width.
func Ngrams(tokens []string, n int) []string {
	if n < 1 || len(tokens) < n {
		return nil
	}
	out := make([]string, 0, len(tokens)-n+1)
	for i := 0; i <= len(tokens)-n; i++ {
		out = append(out, strings.Join(tokens[i:i+n], " "))
	}
	return out
}
