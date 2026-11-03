package analysis

// English stop-word set. Implemented as a hash-set for O(1) membership.
// Based on the Snowball English stop-word list used by Lucene / Elasticsearch.
var stopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {},
	"been": {}, "being": {}, "but": {}, "by": {}, "can": {}, "did": {},
	"do": {}, "does": {}, "doing": {}, "done": {}, "for": {}, "from": {},
	"had": {}, "has": {}, "have": {}, "having": {}, "he": {}, "her": {},
	"here": {}, "hers": {}, "herself": {}, "him": {}, "himself": {}, "his": {},
	"how": {}, "i": {}, "if": {}, "in": {}, "into": {}, "is": {}, "it": {},
	"its": {}, "itself": {}, "just": {}, "me": {}, "more": {}, "most": {},
	"my": {}, "myself": {}, "no": {}, "nor": {}, "not": {}, "now": {},
	"of": {}, "off": {}, "on": {}, "once": {}, "only": {}, "or": {}, "other": {},
	"our": {}, "ours": {}, "ourselves": {}, "out": {}, "own": {}, "s": {},
	"same": {}, "she": {}, "should": {}, "so": {}, "some": {}, "such": {},
	"t": {}, "than": {}, "that": {}, "the": {}, "their": {}, "theirs": {},
	"them": {}, "themselves": {}, "then": {}, "there": {}, "these": {},
	"they": {}, "this": {}, "those": {}, "through": {}, "to": {}, "too": {},
	"under": {}, "until": {}, "up": {}, "us": {}, "very": {}, "was": {},
	"we": {}, "were": {}, "what": {}, "when": {}, "where": {}, "which": {},
	"while": {}, "who": {}, "whom": {}, "why": {}, "will": {}, "with": {},
	"you": {}, "your": {}, "yours": {}, "yourself": {}, "yourselves": {},
}

// IsStopWord returns true if tok is a high-frequency English stop word.
func IsStopWord(tok string) bool {
	_, ok := stopWords[tok]
	return ok
}

// RemoveStopWords filters tokens in-place, returning a new slice with stop words excluded.
func RemoveStopWords(tokens []string) []string {
	out := tokens[:0] // reuse underlying array
	for _, t := range tokens {
		if !IsStopWord(t) {
			out = append(out, t)
		}
	}
	return out
}
