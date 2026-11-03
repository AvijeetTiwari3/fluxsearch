// Package query — Reciprocal Rank Fusion (RRF) for hybrid BM25 + vector search.
//
// RRF Formula (Cormack et al., 2009):
//
//	RRF_score(d) = λ_bm25 / (rank_bm25(d) + k) + λ_vec / (rank_vec(d) + k)
//
// where:
//   - k = 60  (smoothing constant — prevents rank 1 from dominating)
//   - λ_bm25 = 0.6  (BM25 weight for keyword-heavy queries)
//   - λ_vec  = 0.4  (vector weight for semantic queries)
//
// Documents missing from one ranking get a default rank of (N + 1).
package query

import (
	"sort"

	"github.com/sde/fluxsearch/internal/index"
	"github.com/sde/fluxsearch/internal/scorer"
	"github.com/sde/fluxsearch/internal/vector"
)

const (
	RRFk       = 60.0 // smoothing constant
	LambdaBM25 = 0.6  // BM25 weight
	LambdaVec  = 0.4  // vector weight
)

// HybridResult holds a document's fused RRF score.
type HybridResult struct {
	DocID    index.DocID
	RRFScore float64
	BM25Rank int
	VecRank  int
}

// RRFFuse merges a BM25 ranked list and a vector ANN ranked list using RRF.
//
// bm25Results: ordered []ScoredDoc (descending BM25 score)
// vecResults:  ordered []vector.SearchResult (ascending cosine distance)
func RRFFuse(
	bm25Results []scorer.ScoredDoc,
	vecResults []vector.SearchResult,
) []HybridResult {
	// Build rank maps (1-indexed)
	bm25Rank := make(map[index.DocID]int, len(bm25Results))
	for i, r := range bm25Results {
		bm25Rank[r.DocID] = i + 1
	}

	vecRank := make(map[index.DocID]int, len(vecResults))
	for i, r := range vecResults {
		vecRank[index.DocID(r.ID)] = i + 1
	}

	// Collect union of all docIDs
	seen := make(map[index.DocID]struct{})
	for _, r := range bm25Results {
		seen[r.DocID] = struct{}{}
	}
	for _, r := range vecResults {
		seen[index.DocID(r.ID)] = struct{}{}
	}

	defaultBM25 := len(bm25Results) + 1
	defaultVec := len(vecResults) + 1

	results := make([]HybridResult, 0, len(seen))
	for docID := range seen {
		bRank, ok := bm25Rank[docID]
		if !ok {
			bRank = defaultBM25
		}
		vRank, ok := vecRank[docID]
		if !ok {
			vRank = defaultVec
		}

		rrf := LambdaBM25/(float64(bRank)+RRFk) + LambdaVec/(float64(vRank)+RRFk)
		results = append(results, HybridResult{
			DocID:    docID,
			RRFScore: rrf,
			BM25Rank: bRank,
			VecRank:  vRank,
		})
	}

	// Sort descending by RRF score
	sort.Slice(results, func(i, j int) bool {
		if results[i].RRFScore != results[j].RRFScore {
			return results[i].RRFScore > results[j].RRFScore
		}
		return results[i].DocID < results[j].DocID
	})

	return results
}
