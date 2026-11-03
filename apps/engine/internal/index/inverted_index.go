// Package index provides a thread-safe in-memory inverted index with
// delta-compressed posting lists for FluxSearch.
//
// Architecture:
//   - InvertedIndex:  term → PostingList (RWMutex-protected)
//   - PostingList:    sorted []Posting (docID, positions, term frequency)
//   - Delta encoding: stores gaps between docIDs for memory efficiency
//
// Concurrency: One RWMutex per term bucket for maximum read throughput.
// Writes are batched during bulk indexing to minimize lock contention.
package index

import (
	"sort"
	"sync"
)

// DocID is a unique 64-bit document identifier.
type DocID uint64

// Posting represents a single document entry in a posting list.
// Positions are stored as delta-encoded offsets from the previous position
// to reduce memory footprint for high-frequency terms.
type Posting struct {
	DocID     DocID
	Frequency uint32   // term frequency in this document
	Positions []uint32 // delta-encoded within-document token positions
}

// PostingList holds all postings for a single term, sorted by DocID.
type PostingList struct {
	mu       sync.RWMutex
	postings []Posting
	// docIndex maps DocID → index in postings for O(log n) lookup
}

// Add inserts or updates a posting for docID with the given positions.
// Thread-safe: acquires write lock.
func (pl *PostingList) Add(docID DocID, positions []uint32) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	// Binary search for existing entry
	i := sort.Search(len(pl.postings), func(i int) bool {
		return pl.postings[i].DocID >= docID
	})

	if i < len(pl.postings) && pl.postings[i].DocID == docID {
		// Update existing posting
		pl.postings[i].Frequency++
		pl.postings[i].Positions = append(pl.postings[i].Positions, deltaEncode(positions)...)
		return
	}

	// Insert new posting in sorted position
	p := Posting{
		DocID:     docID,
		Frequency: uint32(len(positions)),
		Positions: deltaEncode(positions),
	}
	pl.postings = append(pl.postings, Posting{})
	copy(pl.postings[i+1:], pl.postings[i:])
	pl.postings[i] = p
}

// Get returns all postings. Thread-safe: acquires read lock.
func (pl *PostingList) Get() []Posting {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	out := make([]Posting, len(pl.postings))
	copy(out, pl.postings)
	return out
}

// Len returns the document frequency (number of documents containing this term).
func (pl *PostingList) Len() int {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return len(pl.postings)
}

// Intersect returns the intersection of two posting lists (AND query).
// Both lists must be sorted by DocID. Runs in O(m+n) via merge.
func Intersect(a, b []Posting) []Posting {
	out := make([]Posting, 0, min(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i].DocID == b[j].DocID:
			// Merge: combine frequencies and positions
			merged := Posting{
				DocID:     a[i].DocID,
				Frequency: a[i].Frequency + b[j].Frequency,
			}
			out = append(out, merged)
			i++
			j++
		case a[i].DocID < b[j].DocID:
			i++
		default:
			j++
		}
	}
	return out
}

// Union returns the union of two posting lists (OR query). O(m+n).
func Union(a, b []Posting) []Posting {
	out := make([]Posting, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i].DocID == b[j].DocID:
			out = append(out, Posting{DocID: a[i].DocID, Frequency: a[i].Frequency + b[j].Frequency})
			i++
			j++
		case a[i].DocID < b[j].DocID:
			out = append(out, a[i])
			i++
		default:
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

// deltaEncode converts absolute positions to delta-encoded gaps.
// Example: [5, 12, 20] → [5, 7, 8]
func deltaEncode(positions []uint32) []uint32 {
	if len(positions) == 0 {
		return nil
	}
	out := make([]uint32, len(positions))
	out[0] = positions[0]
	for i := 1; i < len(positions); i++ {
		out[i] = positions[i] - positions[i-1]
	}
	return out
}

// DeltaDecode converts delta-encoded gaps back to absolute positions.
func DeltaDecode(deltas []uint32) []uint32 {
	if len(deltas) == 0 {
		return nil
	}
	out := make([]uint32, len(deltas))
	out[0] = deltas[0]
	for i := 1; i < len(deltas); i++ {
		out[i] = out[i-1] + deltas[i]
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ────────────────────────────────────────────────────
// InvertedIndex — top-level thread-safe index
// ────────────────────────────────────────────────────

// FieldType distinguishes indexed document fields for field-boosting.
type FieldType uint8

const (
	FieldTitle   FieldType = iota // boost × 3.0
	FieldContent                  // boost × 1.0
	FieldTag                      // boost × 2.0
	FieldURL                      // boost × 1.5
)

// TermEntry is a (term, field) composite key.
type TermEntry struct {
	Term  string
	Field FieldType
}

// InvertedIndex is a concurrent, field-aware inverted index.
// Internally, it maintains one PostingList per (term, field) pair.
type InvertedIndex struct {
	mu    sync.RWMutex
	index map[TermEntry]*PostingList

	// Document metadata
	docsMu sync.RWMutex
	docs   map[DocID]*DocMeta
}

// DocMeta holds per-document statistics used by the BM25 scorer.
type DocMeta struct {
	DocID      DocID
	Title      string
	URL        string
	FieldLens  map[FieldType]int // token count per field
	TotalTerms int
}

// NewInvertedIndex creates a new empty index.
func NewInvertedIndex() *InvertedIndex {
	return &InvertedIndex{
		index: make(map[TermEntry]*PostingList),
		docs:  make(map[DocID]*DocMeta),
	}
}

// getOrCreate atomically retrieves or creates the PostingList for a TermEntry.
func (idx *InvertedIndex) getOrCreate(te TermEntry) *PostingList {
	idx.mu.RLock()
	pl, ok := idx.index[te]
	idx.mu.RUnlock()
	if ok {
		return pl
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if pl, ok = idx.index[te]; ok {
		return pl
	}
	pl = &PostingList{}
	idx.index[te] = pl
	return pl
}

// IndexDocument adds all terms from a document field to the index.
// positions[i] is the token position of terms[i] within the field.
func (idx *InvertedIndex) IndexDocument(docID DocID, field FieldType, terms []string, positions []uint32) {
	// Group positions by term
	termPos := make(map[string][]uint32, len(terms))
	for i, t := range terms {
		termPos[t] = append(termPos[t], positions[i])
	}

	for term, pos := range termPos {
		te := TermEntry{Term: term, Field: field}
		pl := idx.getOrCreate(te)
		pl.Add(docID, pos)
	}
}

// Lookup returns the PostingList for (term, field), or nil if not found.
func (idx *InvertedIndex) Lookup(term string, field FieldType) *PostingList {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.index[TermEntry{Term: term, Field: field}]
}

// LookupAllFields returns merged postings across all fields for a term.
func (idx *InvertedIndex) LookupAllFields(term string) []Posting {
	fields := []FieldType{FieldTitle, FieldContent, FieldTag, FieldURL}
	var merged []Posting
	for _, f := range fields {
		pl := idx.Lookup(term, f)
		if pl == nil {
			continue
		}
		merged = Union(merged, pl.Get())
	}
	return merged
}

// SetDocMeta stores metadata for a document.
func (idx *InvertedIndex) SetDocMeta(meta *DocMeta) {
	idx.docsMu.Lock()
	idx.docs[meta.DocID] = meta
	idx.docsMu.Unlock()
}

// GetDocMeta retrieves metadata for a document.
func (idx *InvertedIndex) GetDocMeta(docID DocID) (*DocMeta, bool) {
	idx.docsMu.RLock()
	defer idx.docsMu.RUnlock()
	m, ok := idx.docs[docID]
	return m, ok
}

// DocCount returns the total number of indexed documents.
func (idx *InvertedIndex) DocCount() int {
	idx.docsMu.RLock()
	defer idx.docsMu.RUnlock()
	return len(idx.docs)
}

// TermCount returns the total number of unique (term, field) pairs in the index.
func (idx *InvertedIndex) TermCount() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.index)
}

// AverageFieldLen computes the average token count per document for a given field.
func (idx *InvertedIndex) AverageFieldLen(field FieldType) float64 {
	idx.docsMu.RLock()
	defer idx.docsMu.RUnlock()
	if len(idx.docs) == 0 {
		return 0
	}
	total := 0
	for _, m := range idx.docs {
		total += m.FieldLens[field]
	}
	return float64(total) / float64(len(idx.docs))
}
