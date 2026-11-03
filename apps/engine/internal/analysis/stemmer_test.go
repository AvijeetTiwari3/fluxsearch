package analysis_test

import (
	"testing"

	"github.com/sde/fluxsearch/internal/analysis"
)

// Official Porter Stemmer test vectors from https://tartarus.org/martin/PorterStemmer/
var stemVectors = []struct {
	input    string
	expected string
}{
	{"caresses", "caress"},
	{"ponies", "poni"},
	{"ties", "ti"},
	{"caress", "caress"},
	{"cats", "cat"},
	{"feed", "feed"},
	{"agreed", "agre"},
	{"disabled", "disabl"},
	{"matting", "mat"},
	{"mating", "mate"},
	{"meeting", "meet"},
	{"milling", "mill"},
	{"messing", "mess"},
	{"generalization", "gener"},
	{"oscillator", "oscil"},
	{"sensational", "sensat"},
	{"traditional", "tradit"},
	{"hopping", "hop"},
	{"tanned", "tan"},
	{"running", "run"},
	{"sized", "size"},
	{"relational", "relat"},
	{"electrical", "electr"},
	{"hopeful", "hope"},
	{"goodness", "good"},
	{"revival", "reviv"},
	{"allowance", "allow"},
	{"inference", "infer"},
	{"airliner", "airlin"},
	{"adjustable", "adjust"},
	{"defensible", "defens"},
	{"irritant", "irrit"},
	{"replacement", "replac"},
	{"adjustment", "adjust"},
	{"dependent", "depend"},
	{"adoption", "adopt"},
	{"homologou", "homolog"},
	{"communism", "commun"},
	{"activate", "activ"},
	{"angulariti", "angular"},
	{"homologous", "homolog"},
}

func TestStemmer_OfficialVectors(t *testing.T) {
	for _, tc := range stemVectors {
		t.Run(tc.input, func(t *testing.T) {
			got := analysis.Stem(tc.input)
			if got != tc.expected {
				t.Errorf("Stem(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestTokenize_Basic(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"Hello, World!", []string{"hello", "world"}},
		{"it's a test", []string{"it's", "a", "test"}},
		{"foo123 bar-baz", []string{"foo123", "bar", "baz"}},
		{"", nil},
	}
	for _, tc := range tests {
		got := analysis.Tokenize(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("Tokenize(%q) len = %d, want %d; got %v", tc.input, len(got), len(tc.want), got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("Tokenize(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestIsStopWord(t *testing.T) {
	if !analysis.IsStopWord("the") {
		t.Error("'the' should be a stop word")
	}
	if analysis.IsStopWord("fluxsearch") {
		t.Error("'fluxsearch' should not be a stop word")
	}
}

func TestAnalyzeText(t *testing.T) {
	tokens := analysis.AnalyzeText("The quick brown foxes are running over lazy dogs")
	// "the", "are", "over" removed; "foxes"→"fox", "running"→"run", "lazy"→"lazi", "dogs"→"dog"
	expected := map[string]bool{
		"quick": true, "brown": true, "fox": true, "run": true, "lazi": true, "dog": true,
	}
	for _, tok := range tokens {
		if !expected[tok] {
			t.Errorf("Unexpected token: %q", tok)
		}
		delete(expected, tok)
	}
	if len(expected) > 0 {
		t.Errorf("Missing tokens: %v", expected)
	}
}

func TestBigrams(t *testing.T) {
	tokens := []string{"fast", "search", "engine"}
	got := analysis.Bigrams(tokens)
	want := []string{"fast search", "search engine"}
	if len(got) != len(want) {
		t.Fatalf("Bigrams len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("Bigrams[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func BenchmarkStem(b *testing.B) {
	words := []string{"generalization", "running", "distributed", "indexing", "searching"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analysis.Stem(words[i%len(words)])
	}
}

func BenchmarkAnalyzeText(b *testing.B) {
	text := "The distributed full-text search engine uses BM25 ranking with an inverted index for efficient query processing"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analysis.AnalyzeText(text)
	}
}
