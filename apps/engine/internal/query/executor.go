// Package query — QueryExecutor runs a ParsedQuery against the InvertedIndex
// and returns BM25-ranked results. The Planner optimises clause ordering for
// minimum intermediate result set sizes (shortest-posting-list first).
package query

import (
	"sort"

	"github.com/sde/fluxsearch/internal/index"
	"github.com/sde/fluxsearch/internal/scorer"
)

// SearchResult represents one ranked search hit.
type SearchResult struct {
	DocID    index.DocID
	Score    float64
	Title    string
	URL      string
	Snippet  string // populated by the caller (highlighting layer)
}

// Executor evaluates a ParsedQuery against an InvertedIndex.
type Executor struct {
	idx    *index.InvertedIndex
	scorer *scorer.BM25Scorer
}

// NewExecutor creates an executor backed by the given index.
func NewExecutor(idx *index.InvertedIndex) *Executor {
	return &Executor{
		idx:    idx,
		scorer: scorer.NewBM25Scorer(idx),
	}
}

// Execute runs the query and returns top-K ranked results.
func (e *Executor) Execute(pq *ParsedQuery, topK int) []SearchResult {
	if pq.IsEmpty() || e.idx.DocCount() == 0 {
		return nil
	}

	// 1. Collect candidate docIDs using Boolean logic
	candidates := e.resolveCandidates(pq)
	if len(candidates) == 0 {
		return nil
	}

	// 2. Collect all unique query terms for BM25 scoring
	queryTerms := e.extractQueryTerms(pq)

	// 3. Score candidates
	allScored := e.scorer.Score(queryTerms)

	// 4. Filter by candidate set
	candidateSet := make(map[index.DocID]struct{}, len(candidates))
	for _, c := range candidates {
		candidateSet[c] = struct{}{}
	}

	var results []SearchResult
	for _, sd := range allScored {
		if _, ok := candidateSet[sd.DocID]; !ok {
			continue
		}
		meta, ok := e.idx.GetDocMeta(sd.DocID)
		if !ok {
			continue
		}
		results = append(results, SearchResult{
			DocID: sd.DocID,
			Score: sd.Score,
			Title: meta.Title,
			URL:   meta.URL,
		})
	}

	// 5. Cap at topK
	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	return results
}

// resolveCandidates computes the candidate docID set using Boolean algebra:
//   - MUST clauses intersect the candidate pool
//   - SHOULD clauses union into the pool (if no MUST clauses present)
//   - MUST_NOT clauses are subtracted
func (e *Executor) resolveCandidates(pq *ParsedQuery) []index.DocID {
	var mustSet []index.DocID
	var shouldSet []index.DocID
	var mustNotSet []index.DocID

	for _, clause := range pq.Clauses {
		docs := e.fetchDocIDsForClause(clause)
		switch clause.Type {
		case ClauseMUST:
			if mustSet == nil {
				mustSet = docs
			} else {
				mustSet = intersectDocIDs(mustSet, docs)
			}
		case ClauseSHOULD:
			shouldSet = unionDocIDs(shouldSet, docs)
		case ClauseMUST_NOT:
			mustNotSet = unionDocIDs(mustNotSet, docs)
		}
	}

	var candidates []index.DocID
	if pq.HasMust {
		candidates = mustSet
		if len(shouldSet) > 0 {
			// MUST + SHOULD: must match, bonus score from should (handled in scoring)
			candidates = mustSet // pure intersection for candidates
		}
	} else {
		candidates = shouldSet
	}

	// Remove MUST_NOT documents
	if len(mustNotSet) > 0 {
		excludeSet := make(map[index.DocID]struct{}, len(mustNotSet))
		for _, id := range mustNotSet {
			excludeSet[id] = struct{}{}
		}
		filtered := candidates[:0]
		for _, id := range candidates {
			if _, excluded := excludeSet[id]; !excluded {
				filtered = append(filtered, id)
			}
		}
		candidates = filtered
	}

	return candidates
}

// fetchDocIDsForClause retrieves sorted docIDs for all terms in a clause.
func (e *Executor) fetchDocIDsForClause(clause Clause) []index.DocID {
	var result []index.DocID
	for _, term := range clause.Terms {
		postings := e.idx.LookupAllFields(term)
		ids := make([]index.DocID, len(postings))
		for i, p := range postings {
			ids[i] = p.DocID
		}
		result = unionDocIDs(result, ids)
	}
	return result
}

// extractQueryTerms collects all unique analyzed terms from MUST and SHOULD clauses.
func (e *Executor) extractQueryTerms(pq *ParsedQuery) []string {
	seen := make(map[string]struct{})
	var terms []string
	for _, clause := range pq.Clauses {
		if clause.Type == ClauseMUST_NOT {
			continue
		}
		for _, t := range clause.Terms {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				terms = append(terms, t)
			}
		}
	}
	return terms
}

// intersectDocIDs returns sorted docIDs that appear in both a and b.
func intersectDocIDs(a, b []index.DocID) []index.DocID {
	out := make([]index.DocID, 0, min(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// unionDocIDs merges two sorted docID slices, deduplicating.
func unionDocIDs(a, b []index.DocID) []index.DocID {
	seen := make(map[index.DocID]struct{}, len(a)+len(b))
	out := make([]index.DocID, 0, len(a)+len(b))
	for _, id := range append(a, b...) {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
