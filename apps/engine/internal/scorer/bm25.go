// BM25 Scorer — Okapi BM25 with field-length normalization.
//
// Formula (per field f):
//
//	score(q, d) = Σ_{t∈q} IDF(t) × [ TF(t,d,f) × (k1+1) ] / [ TF(t,d,f) + k1×(1-b+b×|d_f|/avgdl_f) ]
//
// where:
//   - k1 = 1.2  (term saturation — controls TF growth ceiling)
//   - b  = 0.75 (field-length normalization strength)
//   - IDF(t) = ln( (N - df + 0.5) / (df + 0.5) + 1 )  [Robertson-Spärck Jones IDF]
//   - N = total document count
//   - df = document frequency for term t
//   - TF(t,d,f) = raw term frequency of t in field f of document d
//   - |d_f| = token count of field f in document d
//   - avgdl_f = average |d_f| across corpus
//
// Field boosts (multiplicative):
//
//	Title ×3.0 | Tag ×2.0 | URL ×1.5 | Content ×1.0
package scorer

import (
	"math"
	"sync"

	"github.com/sde/fluxsearch/internal/index"
)

const (
	K1 = 1.2  // term saturation constant
	B  = 0.75 // length normalization constant
)

// FieldBoost returns the multiplicative score boost for a field.
func FieldBoost(field index.FieldType) float64 {
	switch field {
	case index.FieldTitle:
		return 3.0
	case index.FieldTag:
		return 2.0
	case index.FieldURL:
		return 1.5
	default:
		return 1.0
	}
}

// IDFCache computes and caches Inverse Document Frequency values.
// Thread-safe for concurrent query serving.
type IDFCache struct {
	mu    sync.RWMutex
	cache map[string]float64
	N     int // total document count (updated atomically on index changes)
}

// NewIDFCache creates a fresh cache.
func NewIDFCache(totalDocs int) *IDFCache {
	return &IDFCache{
		cache: make(map[string]float64),
		N:     totalDocs,
	}
}

// IDF returns the Robertson-Spärck Jones IDF for a term.
// If df (document frequency) == 0, returns 0 (term not in corpus).
func (c *IDFCache) IDF(term string, df int) float64 {
	if df == 0 {
		return 0
	}

	c.mu.RLock()
	if v, ok := c.cache[term]; ok {
		c.mu.RUnlock()
		return v
	}
	c.mu.RUnlock()

	// Compute: ln((N - df + 0.5) / (df + 0.5) + 1)
	n := float64(c.N)
	idf := math.Log((n-float64(df)+0.5)/(float64(df)+0.5) + 1.0)
	if idf < 0 {
		idf = 0 // clamp for very common terms
	}

	c.mu.Lock()
	c.cache[term] = idf
	c.mu.Unlock()
	return idf
}

// Invalidate removes a cached IDF entry (call when document count changes significantly).
func (c *IDFCache) Invalidate(term string) {
	c.mu.Lock()
	delete(c.cache, term)
	c.mu.Unlock()
}

// InvalidateAll clears the entire cache (call after bulk re-indexing).
func (c *IDFCache) InvalidateAll() {
	c.mu.Lock()
	c.cache = make(map[string]float64)
	c.mu.Unlock()
}

// UpdateDocCount atomically updates N and flushes the IDF cache.
func (c *IDFCache) UpdateDocCount(n int) {
	c.mu.Lock()
	c.N = n
	c.cache = make(map[string]float64) // invalidate all cached IDFs
	c.mu.Unlock()
}

// ────────────────────────────────────────────────────
// BM25Scorer
// ────────────────────────────────────────────────────

// BM25Scorer computes per-document BM25 scores for a multi-term query
// against a FluxSearch InvertedIndex.
type BM25Scorer struct {
	idx      *index.InvertedIndex
	idfCache *IDFCache
}

// NewBM25Scorer creates a scorer backed by the given index.
func NewBM25Scorer(idx *index.InvertedIndex) *BM25Scorer {
	return &BM25Scorer{
		idx:      idx,
		idfCache: NewIDFCache(idx.DocCount()),
	}
}

// ScoredDoc holds a docID and its BM25 score.
type ScoredDoc struct {
	DocID index.DocID
	Score float64
}

// Score computes BM25 scores for all documents matching at least one query term.
// Returns results sorted descending by score.
func (s *BM25Scorer) Score(queryTerms []string) []ScoredDoc {
	// Update IDF cache with current doc count
	n := s.idx.DocCount()
	if n == 0 {
		return nil
	}
	s.idfCache.UpdateDocCount(n)

	// Accumulate per-doc scores across all query terms and fields
	scores := make(map[index.DocID]float64)

	fields := []index.FieldType{
		index.FieldTitle, index.FieldContent, index.FieldTag, index.FieldURL,
	}

	for _, term := range queryTerms {
		for _, field := range fields {
			pl := s.idx.Lookup(term, field)
			if pl == nil {
				continue
			}

			postings := pl.Get()
			df := len(postings)
			idf := s.idfCache.IDF(term, df)
			avgdl := s.idx.AverageFieldLen(field)
			boost := FieldBoost(field)

			for _, p := range postings {
				meta, ok := s.idx.GetDocMeta(p.DocID)
				if !ok {
					continue
				}

				fieldLen := float64(meta.FieldLens[field])
				tf := float64(p.Frequency)

				// BM25 term score for this (term, field, doc) triple
				norm := tf * (K1 + 1)
				denom := tf + K1*(1-B+B*(fieldLen/max(avgdl, 1)))
				termScore := idf * (norm / denom) * boost

				scores[p.DocID] += termScore
			}
		}
	}

	// Convert map to sorted slice
	out := make([]ScoredDoc, 0, len(scores))
	for docID, score := range scores {
		out = append(out, ScoredDoc{DocID: docID, Score: score})
	}

	// Sort descending by score, break ties by docID ascending
	sortScoredDocs(out)
	return out
}

func sortScoredDocs(docs []ScoredDoc) {
	// Insertion sort is fine for small result sets; production would use pdqsort.
	for i := 1; i < len(docs); i++ {
		key := docs[i]
		j := i - 1
		for j >= 0 && (docs[j].Score < key.Score ||
			(docs[j].Score == key.Score && docs[j].DocID > key.DocID)) {
			docs[j+1] = docs[j]
			j--
		}
		docs[j+1] = key
	}
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
