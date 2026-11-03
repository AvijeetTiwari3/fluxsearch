package scorer_test

import (
	"math"
	"testing"

	"github.com/sde/fluxsearch/internal/index"
	"github.com/sde/fluxsearch/internal/scorer"
)

// buildTestIndex creates a small corpus:
//
//	doc1: title="fast search engine" content="distributed full text search"
//	doc2: title="database indexing" content="inverted index for fast search"
//	doc3: title="vector similarity" content="cosine distance semantic search"
func buildTestIndex() *index.InvertedIndex {
	idx := index.NewInvertedIndex()

	type fieldDoc struct {
		docID  index.DocID
		field  index.FieldType
		terms  []string
		posns  []uint32
		title  string
		url    string
		flen   map[index.FieldType]int
	}

	docs := []fieldDoc{
		{
			docID: 1, field: index.FieldTitle,
			terms: []string{"fast", "search", "engin"},
			posns: []uint32{0, 1, 2},
			title: "fast search engine",
			flen:  map[index.FieldType]int{index.FieldTitle: 3, index.FieldContent: 4},
		},
		{
			docID: 1, field: index.FieldContent,
			terms: []string{"distribut", "full", "text", "search"},
			posns: []uint32{0, 1, 2, 3},
		},
		{
			docID: 2, field: index.FieldTitle,
			terms: []string{"databas", "index"},
			posns: []uint32{0, 1},
			title: "database indexing",
			flen:  map[index.FieldType]int{index.FieldTitle: 2, index.FieldContent: 5},
		},
		{
			docID: 2, field: index.FieldContent,
			terms: []string{"invert", "index", "fast", "search"},
			posns: []uint32{0, 1, 2, 3},
		},
		{
			docID: 3, field: index.FieldTitle,
			terms: []string{"vector", "similar"},
			posns: []uint32{0, 1},
			title: "vector similarity",
			flen:  map[index.FieldType]int{index.FieldTitle: 2, index.FieldContent: 4},
		},
		{
			docID: 3, field: index.FieldContent,
			terms: []string{"cosin", "distanc", "semant", "search"},
			posns: []uint32{0, 1, 2, 3},
		},
	}

	for _, d := range docs {
		idx.IndexDocument(d.docID, d.field, d.terms, d.posns)
	}

	// Set doc metadata
	idx.SetDocMeta(&index.DocMeta{DocID: 1, Title: "fast search engine", FieldLens: map[index.FieldType]int{index.FieldTitle: 3, index.FieldContent: 4}})
	idx.SetDocMeta(&index.DocMeta{DocID: 2, Title: "database indexing", FieldLens: map[index.FieldType]int{index.FieldTitle: 2, index.FieldContent: 5}})
	idx.SetDocMeta(&index.DocMeta{DocID: 3, Title: "vector similarity", FieldLens: map[index.FieldType]int{index.FieldTitle: 2, index.FieldContent: 4}})

	return idx
}

func TestBM25_ScoreReturnsResults(t *testing.T) {
	idx := buildTestIndex()
	sc := scorer.NewBM25Scorer(idx)

	results := sc.Score([]string{"search"})
	if len(results) == 0 {
		t.Fatal("expected non-empty results for query 'search'")
	}
	// All 3 docs contain "search" — verify all are returned
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestBM25_ScoreOrdering(t *testing.T) {
	idx := buildTestIndex()
	sc := scorer.NewBM25Scorer(idx)

	results := sc.Score([]string{"fast", "search"})
	if len(results) < 2 {
		t.Fatal("expected at least 2 results")
	}
	// Results must be in descending score order
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("scores not sorted: results[%d].Score=%f > results[%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestBM25_ScorePositive(t *testing.T) {
	idx := buildTestIndex()
	sc := scorer.NewBM25Scorer(idx)

	results := sc.Score([]string{"fast"})
	for _, r := range results {
		if r.Score < 0 {
			t.Errorf("BM25 score should be non-negative, got %f for doc %d", r.Score, r.DocID)
		}
	}
}

func TestBM25_EmptyQueryReturnsEmpty(t *testing.T) {
	idx := buildTestIndex()
	sc := scorer.NewBM25Scorer(idx)
	results := sc.Score([]string{})
	if len(results) != 0 {
		t.Errorf("expected empty results for empty query, got %d", len(results))
	}
}

func TestBM25_UnknownTermReturnsEmpty(t *testing.T) {
	idx := buildTestIndex()
	sc := scorer.NewBM25Scorer(idx)
	results := sc.Score([]string{"xyzzy"})
	if len(results) != 0 {
		t.Errorf("expected empty results for unknown term, got %d", len(results))
	}
}

// TestBM25_IDFFormula verifies IDF math: idf = ln((N-df+0.5)/(df+0.5)+1)
// For N=3, df=3 → idf = ln((3-3+0.5)/(3+0.5)+1) = ln(0.5/3.5 + 1) ≈ 0.1335
func TestBM25_IDFFormula(t *testing.T) {
	cache := scorer.NewIDFCache(3)
	idf := cache.IDF("search", 3) // "search" appears in all 3 docs
	expected := math.Log((3.0-3.0+0.5)/(3.0+0.5) + 1.0)
	if math.Abs(idf-expected) > 1e-9 {
		t.Errorf("IDF formula mismatch: got %f, want %f", idf, expected)
	}
}

func BenchmarkBM25Score(b *testing.B) {
	idx := buildTestIndex()
	sc := scorer.NewBM25Scorer(idx)
	query := []string{"fast", "search", "index"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc.Score(query)
	}
}
